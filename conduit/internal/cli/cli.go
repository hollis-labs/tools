// Package cli is conduit's command-line transport. It calls the service layer
// and nothing else.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

// Exit codes. A run exits with the command's own code; these cover conduit's
// own outcomes and sit outside the common 0..126 command range only where they
// must (3 and 4 follow caster's "denied" convention for policy outcomes).
const (
	ExitOK       = 0
	ExitError    = 1
	ExitNotFound = 2
	ExitDenied   = 3
	ExitAsk      = 4 // check: needs approval; run: approval required and not given
	// ExitInternal is EX_SOFTWARE: conduit itself failed (a recovered panic).
	ExitInternal = 70
)

const usage = `conduit — governed command runtime

  conduit run [-C dir] [-timeout 30s] [-session s] [-shadow] [-yes] [-retries n] [-sandbox ro|workspace|workspace-net|protect] [-env NAME]... [-q] (-c "cmd" | -- argv...)
                              run a command through policy; output is captured, redacted and stored
  conduit check [-json] "cmd"   policy dry run: verdict and reasons (exit 0 allow, 4 ask, 3 deny)
  conduit parse [-json] "cmd"   show how a command is parsed: segments, wrappers, effect classes
  conduit ls [-session s] [-state st] [-n 20] [-json]
  conduit show [-json] <run>    one run with its attempts
  conduit recall [-stderr] [-attempt n] <run>
                              print the stored output of a run
  conduit pick [-query q] [-history file] [-run] [-filter q]
                              fuzzy picker over command history (prints the choice; -run runs it)
  conduit init bash|zsh [-key K]
                              shell integration: eval "$(conduit init bash)" binds the picker and adds cx
  conduit pending               runs waiting for approval
  conduit approve [-yes] [-x] <run>
                              review the parsed command and approve it (-x runs it here)
  conduit reject <run> | cancel <run>
  conduit runbooks              named operations: fixed argv, typed parameters
  conduit token create -name n -scope exec.run,exec.read | ls | revoke <name>
  conduit serve [-unix path | -addr 127.0.0.1:7421] [-allow-dir d]...
                              HTTP API for "run this on this host" (bearer tokens, scoped)
  conduit mcp [-principal name] [-allow-dir d]... [-allow-any-dir] [-see-all]
                              MCP tools on stdio for agents (no approval tool, by design;
                              confined to the current directory and to its own runs by default)
  conduit reap [-stale 30s]       clean up after a crash: fail orphaned runs, stop their process groups
  conduit panics [-n 20] | show <ref>
                              internal errors conduit recovered from (a ref is all a remote caller sees)
  conduit paths                 where data lives

<run> is an id, or any unique prefix or suffix of one.
Exit: the command's own code for run; 2 not found, 3 denied, 4 needs approval.
`

// Main runs the CLI and returns the process exit code.
func Main(ctx context.Context, svc *service.Service, args []string, in *os.File, stdout, stderr io.Writer, paths func() string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return ExitOK
	}
	c := &cmd{ctx: ctx, svc: svc, in: in, out: stdout, err: stderr, paths: paths}
	var code int
	err := panics.Call("cli "+args[0], func() error { code = c.dispatch(args); return nil })
	if ref, ok := panics.Ref(err); ok {
		fmt.Fprintf(stderr, "conduit: internal error (ref %s). This is a bug in conduit, not in your command.\n         Details: conduit panics show %s\n", ref, ref)
		return ExitInternal
	}
	return code
}

func (c *cmd) dispatch(args []string) int {
	stdout, stderr := c.out, c.err
	var code int
	switch args[0] {
	case "run":
		code = c.run(args[1:])
	case "check":
		code = c.check(args[1:])
	case "parse":
		code = c.parse(args[1:])
	case "ls":
		code = c.ls(args[1:])
	case "show":
		code = c.show(args[1:])
	case "recall":
		code = c.recall(args[1:])
	case "pick":
		code = c.pick(args[1:])
	case "init":
		code = c.initShell(args[1:])
	case "pending":
		code = c.pending()
	case "approve":
		code = c.approve(args[1:])
	case "reject":
		code = c.reject(args[1:])
	case "cancel":
		code = c.cancel(args[1:])
	case "runbooks":
		code = c.runbooks()
	case "token":
		code = c.token(args[1:])
	case "mcp":
		code = c.mcpCmd(args[1:])
	case "serve":
		code = c.serve(args[1:])
	case "panics":
		code = c.panics(args[1:])
	case "reap":
		code = c.reap(args[1:])
	case "paths":
		fmt.Fprintln(stdout, c.paths())
	default:
		fmt.Fprintf(stderr, "conduit: unknown command %q\n\n%s", args[0], usage)
		return ExitError
	}
	return code
}

type cmd struct {
	ctx   context.Context
	svc   *service.Service
	in    *os.File
	out   io.Writer
	err   io.Writer
	paths func() string
}

func (c *cmd) fail(format string, a ...any) int {
	fmt.Fprintf(c.err, "conduit: "+format+"\n", a...)
	return ExitError
}

// commandString turns the remaining arguments into one shell command string. A
// single argument is taken as the command text; several are an argv and are
// quoted so the shell sees exactly those words.
func commandString(rest []string, dashC string) (string, error) {
	switch {
	case dashC != "" && len(rest) > 0:
		return "", errors.New("give either -c \"cmd\" or an argv after --, not both")
	case dashC != "":
		return dashC, nil
	case len(rest) == 1:
		return rest[0], nil
	case len(rest) > 1:
		q := make([]string, len(rest))
		for i, a := range rest {
			s, err := syntax.Quote(a, syntax.LangBash)
			if err != nil {
				return "", fmt.Errorf("cannot quote %q: %w", a, err)
			}
			q[i] = s
		}
		return strings.Join(q, " "), nil
	}
	return "", errors.New("no command given")
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func exitFor(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ExitNotFound
	case errors.Is(err, service.ErrDenied):
		return ExitDenied
	case errors.Is(err, service.ErrNotAsked):
		return ExitAsk
	}
	return ExitError
}
