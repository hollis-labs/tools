package cli

import (
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/tools/conduit/internal/store"
)

// Version is reported by the MCP server.
const Version = "0.2.0-spike"

func (c *cmd) pending() int {
	runs, err := c.svc.Pending()
	if err != nil {
		return c.fail("%v", err)
	}
	if len(runs) == 0 {
		fmt.Fprintln(c.out, "nothing awaiting approval")
		return ExitOK
	}
	tw := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tAGE\tFROM\tEFFECT\tSANDBOX\tCOMMAND")
	for _, r := range runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", shortID(r.ID), time.Since(r.CreatedAt).Round(time.Second), r.Principal, r.Effect, dash(r.Sandbox), oneLineSafe(r.Command, 70))
	}
	return flushOK(tw)
}

func oneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-3]) + "..."
	}
	return s
}

func renderPending(r *store.Run) string {
	d := decisionOf(r)
	var b strings.Builder
	fmt.Fprintf(&b, "run      %s  from %s on %s\ncommand  %s\ncwd      %s\neffect   %s   sandbox %s\n", shortID(r.ID), visible(r.Principal), visible(r.Host), block(r.Command, "         "), visible(r.Cwd), d.Effect, dash(r.Sandbox))
	for _, rs := range d.Reasons {
		fmt.Fprintf(&b, "  - [%s] %s: %s\n", rs.Verdict, visible(rs.Rule), oneLineSafe(rs.Why, 200))
		if rs.Segment != "" && rs.Segment != r.Command {
			fmt.Fprintf(&b, "      in: %s\n", oneLineSafe(rs.Segment, 200))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *cmd) cancel(args []string) int {
	if len(args) != 1 {
		return c.fail("cancel: want one run id")
	}
	run, err := c.svc.Cancel(c.ctx, args[0], true) // the local operator may cancel any run
	if err != nil {
		fmt.Fprintf(c.err, "conduit: %v\n", err)
		return exitFor(err)
	}
	fmt.Fprintf(c.out, "cancel requested for %s (state %s)\n", shortID(run.ID), run.State)
	return ExitOK
}

func (c *cmd) runbooks() int {
	rbs := c.svc.Runbooks()
	if len(rbs) == 0 {
		fmt.Fprintln(c.out, "no runbooks (see README for the file format)")
		return ExitOK
	}
	tw := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVERDICT\tSANDBOX\tPARAMS\tCOMMAND")
	for _, rb := range rbs {
		var ps []string
		for n, p := range rb.Params {
			if p.Required {
				n += "*"
			}
			ps = append(ps, n)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", rb.Name, rb.Verdict, dash(rb.Sandbox), dash(strings.Join(ps, ",")), strings.Join(rb.Argv, " "))
	}
	return flushOK(tw)
}

func (c *cmd) reap(args []string) int {
	fs := flag.NewFlagSet("reap", flag.ContinueOnError)
	fs.SetOutput(c.err)
	stale := fs.Duration("stale", 30*time.Second, "treat a running run as orphaned after this long without a heartbeat (and no live owner)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if *stale < time.Second {
		return c.fail("reap: -stale must be at least 1s")
	}
	rep, err := c.svc.ReapStale(time.Now(), *stale)
	if err != nil {
		return c.fail("reap: %v", err)
	}
	fmt.Fprintln(c.out, rep)
	return ExitOK
}

func (c *cmd) panics(args []string) int {
	if len(args) > 0 && args[0] == "show" {
		if len(args) != 2 {
			return c.fail("panics show: want a ref")
		}
		p, err := c.svc.PanicByRef(args[1])
		if err != nil {
			fmt.Fprintf(c.err, "conduit: %v\n", err)
			return exitFor(err)
		}
		fmt.Fprintf(c.out, "ref    %s\nat     %s\nwhere  %s\nrun    %s\nvalue  %s\n\nstack (redacted):\n%s\n",
			p.Ref, p.At.Local().Format(time.RFC3339), visible(p.Where), dash(p.RunID), block(p.Value, "       "), visible(p.Stack))
		return ExitOK
	}
	fs := flag.NewFlagSet("panics", flag.ContinueOnError)
	fs.SetOutput(c.err)
	n := fs.Int("n", 20, "how many")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	ps, err := c.svc.Panics(*n)
	if err != nil {
		return c.fail("%v", err)
	}
	if len(ps) == 0 {
		fmt.Fprintln(c.out, "no recovered panics")
		return ExitOK
	}
	tw := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REF\tWHEN\tWHERE\tRUN\tVALUE")
	for _, p := range ps {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Ref, p.At.Local().Format("01-02 15:04:05"), oneLineSafe(p.Where, 30), dash(shortID(p.RunID)), oneLineSafe(p.Value, 60))
	}
	return flushOK(tw)
}
