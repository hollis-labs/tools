package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/conduit/internal/service"
	"github.com/hollis-labs/conduit/internal/store"
)

func (c *cmd) run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(c.err)
	var (
		dir     = fs.String("C", "", "working directory")
		timeout = fs.Duration("timeout", 0, "kill the command's process group after this long")
		session = fs.String("session", "", "session label")
		parent  = fs.String("parent", "", "parent reference (turn, tool call, message)")
		shadow  = fs.Bool("shadow", false, "record the verdict but do not require approval (deny still blocks)")
		yes     = fs.Bool("yes", false, "approve an ask verdict without prompting (recorded as flag approval)")
		retries = fs.Int("retries", 0, "extra attempts for transient failures of read-only commands")
		quiet   = fs.Bool("q", false, "do not echo the command's output live")
		dashC   = fs.String("c", "", "command string")
		sbx     = fs.String("sandbox", "", "confine the command: ro, workspace, workspace-net, protect (policy may require more)")
		pass    multiFlag
	)
	fs.Var(&pass, "env", "variable name exempt from credential scrubbing (repeatable)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	command, err := commandString(fs.Args(), *dashC)
	if err != nil {
		return c.fail("%v", err)
	}

	rq := service.RunRequest{
		Command: command, Dir: *dir, Session: *session, Parent: *parent, Timeout: *timeout,
		Shadow: *shadow, PassEnv: pass, Retries: *retries, Sandbox: *sbx,
	}
	if !*quiet {
		rq.Stdout, rq.Stderr = c.out, c.err
	}
	rq.Approve = c.approver(*yes)

	run, err := c.svc.Run(c.ctx, rq)
	if run == nil {
		return c.fail("%v", err)
	}
	c.footer(run, err)
	if err != nil {
		return exitFor(err)
	}
	if n := len(run.Attempts); n > 0 {
		a := run.Attempts[n-1]
		switch {
		case a.Class == "timeout":
			return 124 // as coreutils timeout(1)
		case a.StartErr != "":
			return 127
		}
		return a.ExitCode // a signal death is already 128+signal
	}
	return ExitOK
}

// approver asks the operator on the terminal. A prompt is read from /dev/tty so
// piped stdin cannot answer it; with no terminal and no -yes, nothing is approved.
func (c *cmd) approver(yes bool) service.Approver {
	return func(ctx context.Context, p service.Preview) (bool, string) {
		if yes {
			return true, "flag"
		}
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			fmt.Fprintln(c.err, "conduit: approval needed but no terminal; pass -yes to approve")
			return false, ""
		}
		defer func() { _ = tty.Close() }()
		fmt.Fprintf(tty, "\n%s\n", renderPreview(p))
		fmt.Fprint(tty, "run it? [y/N] ")
		line, _ := bufio.NewReader(tty).ReadString('\n')
		if strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes") {
			return true, "tty"
		}
		return false, ""
	}
}

func renderPreview(p service.Preview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "command  %s\neffect   %s\nverdict  %s\n", block(p.Command, "         "), p.Decision.Effect, strings.ToUpper(string(p.Decision.Verdict)))
	for _, r := range p.Decision.Reasons {
		if r.Verdict == "allow" && r.Segment == "" {
			continue
		}
		fmt.Fprintf(&b, "  - [%s] %s: %s\n", r.Verdict, visible(r.Rule), oneLineSafe(r.Why, 200))
		if r.Segment != "" && r.Segment != p.Command {
			fmt.Fprintf(&b, "      in: %s\n", oneLineSafe(r.Segment, 200))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *cmd) footer(run *store.Run, err error) {
	switch run.State {
	case store.StateDenied:
		why := "approval required and not given"
		if err != nil && strings.Contains(err.Error(), "policy") {
			why = "denied by policy"
		}
		fmt.Fprintf(c.err, "conduit: %s — %s (run %s)\n", oneLineSafe(run.Command, 200), why, run.ID)
		var preview []string
		for _, l := range strings.Split(renderPreview(service.Preview{Command: run.Command, Decision: decisionOf(run)}), "\n")[1:] {
			preview = append(preview, "  "+l)
		}
		fmt.Fprintln(c.err, strings.Join(preview, "\n"))
		return
	}
	n := len(run.Attempts)
	if n == 0 {
		fmt.Fprintf(c.err, "conduit: run %s %s\n", run.ID, run.State)
		return
	}
	a := run.Attempts[n-1]
	dur := a.Ended.Sub(a.Started).Round(time.Millisecond)
	extra := ""
	if n > 1 {
		extra += fmt.Sprintf(", %d attempts", n)
	}
	if a.Truncated {
		extra += ", output truncated"
	}
	if a.Redactions > 0 {
		extra += fmt.Sprintf(", %d redacted", a.Redactions)
	}
	if a.Class != "ok" {
		extra += ", " + a.Class
	}
	fmt.Fprintf(c.err, "conduit: run %s %s exit %d in %s%s  [full output: conduit recall %s]\n",
		run.ID, run.State, a.ExitCode, dur, extra, shortID(run.ID))
}

func shortID(id string) string {
	if i := strings.LastIndexByte(id, '-'); i >= 0 {
		return id[i+1:]
	}
	return id
}
