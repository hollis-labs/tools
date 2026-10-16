package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/conduit/internal/cmdparse"
	"github.com/hollis-labs/conduit/internal/policy"
	"github.com/hollis-labs/conduit/internal/store"
)

func decisionOf(run *store.Run) policy.Decision {
	d := policy.Decision{Verdict: policy.Verdict(run.PolicyVerdict), Effect: cmdparse.Effect(run.Effect)}
	_ = json.Unmarshal([]byte(run.PolicyReasons), &d.Reasons)
	return d
}

func (c *cmd) json(v any) int {
	enc := json.NewEncoder(c.out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return c.fail("%v", err)
	}
	return ExitOK
}

func oneCommand(fs *flag.FlagSet) (string, bool) {
	if fs.NArg() < 1 {
		return "", false
	}
	return strings.Join(fs.Args(), " "), true
}

func (c *cmd) parse(args []string) int {
	fs := flag.NewFlagSet("parse", flag.ContinueOnError)
	fs.SetOutput(c.err)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	command, ok := oneCommand(fs)
	if !ok {
		return c.fail("parse: no command given")
	}
	p := c.svc.Parse(command)
	if *asJSON {
		return c.json(p)
	}
	if p.Err != "" {
		fmt.Fprintf(c.out, "parse error: %s\n(opaque: policy fails closed)\n", p.Err)
		return ExitOK
	}
	tw := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "OP\tDEPTH\tBIN\tSUB\tEFFECT\tNOTES\tRAW")
	for _, s := range p.Segments {
		var notes []string
		if len(s.Wrappers) > 0 {
			notes = append(notes, "wrappers="+strings.Join(s.Wrappers, ">"))
		}
		if s.Via != "" {
			notes = append(notes, "via="+s.Via)
		}
		notes = append(notes, s.Risks...)
		if s.Opaque {
			notes = append(notes, "OPAQUE")
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n", dash(s.Op), s.Depth, visible(dash(s.Bin)), visible(dash(s.Sub)), s.Effect, visible(strings.Join(notes, " ")), oneLineSafe(s.Raw, 120))
	}
	_ = tw.Flush()
	fmt.Fprintf(c.out, "\neffect: %s  opaque: %v\n", p.Effect, p.Opaque)
	return ExitOK
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (c *cmd) check(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(c.err)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	command, ok := oneCommand(fs)
	if !ok {
		return c.fail("check: no command given")
	}
	p, d := c.svc.Check(command)
	if *asJSON {
		c.json(map[string]any{"parsed": p, "decision": d})
	} else {
		fmt.Fprintln(c.out, renderPreviewDecision(command, d))
	}
	switch d.Verdict {
	case policy.Deny:
		return ExitDenied
	case policy.Ask:
		return ExitAsk
	case policy.Allow:
	}
	return ExitOK
}

func renderPreviewDecision(command string, d policy.Decision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  (effect %s", strings.ToUpper(string(d.Verdict)), d.Effect)
	if d.Sandbox != "" {
		fmt.Fprintf(&b, ", sandbox %s", d.Sandbox)
	}
	b.WriteString(")\n")
	for _, r := range d.Reasons {
		fmt.Fprintf(&b, "  [%s] %s: %s\n", r.Verdict, visible(r.Rule), oneLineSafe(r.Why, 200))
		if r.Segment != "" && r.Segment != command {
			fmt.Fprintf(&b, "      in: %s\n", oneLineSafe(r.Segment, 200))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *cmd) ls(args []string) int {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(c.err)
	session := fs.String("session", "", "only this session")
	state := fs.String("state", "", "only this state (done, failed, denied, ...)")
	n := fs.Int("n", 20, "how many")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	runs, err := c.svc.List(*session, *state, *n)
	if err != nil {
		return c.fail("%v", err)
	}
	if *asJSON {
		return c.json(runs)
	}
	tw := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tWHEN\tSTATE\tEXIT\tEFFECT\tVERDICT\tCOMMAND")
	for _, r := range runs {
		exit := "-"
		if k := len(r.Attempts); k > 0 {
			exit = fmt.Sprint(r.Attempts[k-1].ExitCode)
		}
		cmdline := oneLineSafe(r.Command, 60)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", shortID(r.ID), r.CreatedAt.Local().Format("01-02 15:04:05"), r.State, exit, r.Effect, r.PolicyVerdict, cmdline)
	}
	return flushOK(tw)
}

func flushOK(tw *tabwriter.Writer) int {
	_ = tw.Flush()
	return ExitOK
}

func (c *cmd) show(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(c.err)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if fs.NArg() != 1 {
		return c.fail("show: want one run id")
	}
	r, err := c.svc.Get(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(c.err, "conduit: %v\n", err)
		return exitFor(err)
	}
	if *asJSON {
		return c.json(r)
	}
	fmt.Fprintf(c.out, "run       %s\nstate     %s\ncommand   %s\ncwd       %s\nprincipal %s@%s\nsession   %s\ncreated   %s\n",
		r.ID, r.State, block(r.Command, "          "), visible(r.Cwd), visible(r.Principal), visible(r.Host), visible(dash(r.Session)), r.CreatedAt.Local().Format(time.RFC3339))
	fmt.Fprintf(c.out, "policy    %s (%s mode), effect %s, approval %s, sandbox %s\n", r.PolicyVerdict, r.PolicyMode, r.Effect, dash(r.Approval), dash(r.Sandbox))
	for _, rs := range decisionOf(r).Reasons {
		fmt.Fprintf(c.out, "          [%s] %s: %s\n", rs.Verdict, visible(rs.Rule), oneLineSafe(rs.Why, 200))
	}
	for _, a := range r.Attempts {
		link := ""
		if a.RetryOf > 0 {
			link = fmt.Sprintf(" (retry of %d)", a.RetryOf)
		}
		fmt.Fprintf(c.out, "attempt %d%s  exit %d  %s  %s  stdout %dB stderr %dB", a.N, link, a.ExitCode, a.Class,
			a.Ended.Sub(a.Started).Round(time.Millisecond), a.StdoutBytes, a.StderrBytes)
		if a.Truncated {
			fmt.Fprint(c.out, "  truncated")
		}
		if a.Redactions > 0 {
			fmt.Fprintf(c.out, "  %d redacted", a.Redactions)
		}
		fmt.Fprintln(c.out)
		if a.StdoutRef != "" {
			fmt.Fprintf(c.out, "          stdout %s\n", a.StdoutRef)
		}
		if a.StderrRef != "" {
			fmt.Fprintf(c.out, "          stderr %s\n", a.StderrRef)
		}
	}
	return ExitOK
}

func (c *cmd) recall(args []string) int {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	fs.SetOutput(c.err)
	errStream := fs.Bool("stderr", false, "print stderr instead of stdout")
	attempt := fs.Int("attempt", 0, "attempt number (default: the last)")
	raw := fs.Bool("raw", false, "write the stored bytes unchanged even to a terminal")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if fs.NArg() != 1 {
		return c.fail("recall: want one run id")
	}
	stream := "stdout"
	if *errStream {
		stream = "stderr"
	}
	b, err := c.svc.Recall(c.ctx, fs.Arg(0), stream, *attempt)
	if err != nil {
		fmt.Fprintf(c.err, "conduit: %v\n", err)
		return exitFor(err)
	}
	if !*raw && c.isTerminal() {
		b = []byte(visible(string(b)))
	}
	_, _ = c.out.Write(b)
	return ExitOK
}
