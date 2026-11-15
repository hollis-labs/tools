// Package command implements the bounded CLI. Fixture hosts are selected only
// by --fixture; there is no production provider, credential or endpoint fallback.
package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hollis-labs/libs/agent/sdk"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/tools/citadel-operative/internal/fixturehost"
)

const (
	OK        = 0
	Failure   = 1
	Usage     = 2
	Denied    = 3
	Pending   = 4
	Cancelled = 130
)

const help = `citadel-operative: experimental SDK definition and synthetic fixture consumer

  validate --definition FILE
  run --fixture allow|deny|pending|cancel --definition FILE --run-id ID --input TEXT

validate checks agentdef v2 structure and reports its semantic digest; it grants
no authority. run requires explicit fixture selection. Fixtures use only in-memory
models, tools and host records, with no external endpoints or production grants.
Production host configuration/adapters are not implemented; unconfigured run fails.
`

type Runner struct{ ledger *fixturehost.Ledger }

// New shares fixture reservations with other runners only when given the same
// ledger. Nil starts a fresh in-memory fixture universe. No persistence is implied.
func New(ledger *fixturehost.Ledger) *Runner {
	if ledger == nil {
		ledger = fixturehost.NewLedger()
	}
	return &Runner{ledger: ledger}
}

func (r *Runner) Execute(ctx context.Context, args []string, out, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if _, err := io.WriteString(out, help); err != nil {
			return Failure
		}
		return OK
	}
	if args[0] != "validate" && args[0] != "run" {
		fmt.Fprintln(stderr, "unknown command; use --help")
		return Usage
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	definition := flags.String("definition", "", "agentdef v2 frontmatter/Markdown file")
	var mode, runID, input *string
	if args[0] == "run" {
		mode = flags.String("fixture", "", "explicit synthetic scenario: allow, deny, pending, cancel")
		runID = flags.String("run-id", "", "single-use fixture conversation run ID")
		input = flags.String("input", "", "explicit input text")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return OK
		}
		return Usage
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return Usage
	}
	if mode != nil && *mode == "" {
		fmt.Fprintln(stderr, "production host is not configured; explicitly select --fixture for synthetic execution")
		return Usage
	}
	if mode != nil && !fixturehost.Supported(*mode) {
		fmt.Fprintln(stderr, "unsupported fixture; use allow, deny, pending or cancel")
		return Usage
	}
	if strings.TrimSpace(*definition) == "" {
		fmt.Fprintln(stderr, "--definition is required")
		return Usage
	}
	if runID != nil && (strings.TrimSpace(*runID) == "" || strings.TrimSpace(*input) == "") {
		fmt.Fprintln(stderr, "--run-id and --input are required")
		return Usage
	}
	def, err := readDefinition(*definition)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return Failure
	}
	if args[0] == "validate" {
		digest, err := agentdef.Digest(def)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return Failure
		}
		if err = json.NewEncoder(out).Encode(struct {
			Status       string `json:"status"`
			DefinitionID string `json:"definition_id"`
			Revision     string `json:"revision"`
			Digest       string `json:"digest"`
		}{"valid", def.DefinitionID, def.Revision, digest}); err != nil {
			return Failure
		}
		return OK
	}
	host, err := fixturehost.New(*mode, r.ledger)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return Failure
	}
	operative, err := sdk.New(*def, host.Ports())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return Failure
	}
	result, runErr := operative.Run(ctx, host.Input(*runID, *input))
	stats := host.Stats()
	if err = json.NewEncoder(out).Encode(struct {
		Fixture       string     `json:"fixture"`
		RunID         string     `json:"run_id"`
		Status        sdk.Status `json:"status"`
		Text          string     `json:"text,omitempty"`
		ModelCalls    int64      `json:"model_calls"`
		ToolEffects   int64      `json:"tool_effects"`
		Events        int64      `json:"events"`
		PendingTicket bool       `json:"pending_ticket"`
	}{*mode, result.RunID, result.Status, result.Text, stats.ModelCalls, stats.ToolEffects, stats.Events, result.PendingTicket != nil}); err != nil {
		return Failure
	}
	if runErr != nil {
		fmt.Fprintln(stderr, runErr)
	}
	switch {
	case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded), result.Status == sdk.Cancelled:
		return Cancelled
	case errors.Is(runErr, sdk.ErrPending):
		return Pending
	case errors.Is(runErr, gate.ErrDenied):
		return Denied
	case runErr != nil:
		return Failure
	default:
		return OK
	}
}

func readDefinition(path string) (*agentdef.Definition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const maximum = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximum {
		return nil, fmt.Errorf("definition exceeds 1 MiB")
	}
	return agentdef.Parse(data)
}
