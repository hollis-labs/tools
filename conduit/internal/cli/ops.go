package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/tools/conduit/internal/api"
	"github.com/hollis-labs/tools/conduit/internal/mcp"
	"github.com/hollis-labs/tools/conduit/internal/service"
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

// approve shows the operator the parsed, normalised command and asks on the
// terminal before approving; -yes skips the question for scripted operators.
func (c *cmd) approve(args []string) int {
	fs := flag.NewFlagSet("approve", flag.ContinueOnError)
	fs.SetOutput(c.err)
	yes := fs.Bool("yes", false, "approve without asking")
	exec := fs.Bool("x", false, "also execute it here, in this terminal, instead of leaving it to the submitting process")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if fs.NArg() != 1 {
		return c.fail("approve: want one run id")
	}
	run, err := c.svc.Get(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(c.err, "conduit: %v\n", err)
		return exitFor(err)
	}
	if run.State != store.StatePending {
		return c.fail("run %s is %s, not awaiting approval", shortID(run.ID), run.State)
	}
	fmt.Fprintln(c.out, renderPending(run))
	kind := "flag"
	if !*yes {
		ok := c.confirm("approve and allow it to run? [y/N] ")
		if !ok {
			fmt.Fprintln(c.err, "conduit: not approved")
			return ExitAsk
		}
		kind = "tty"
	}
	run, err = c.svc.Approve(c.ctx, run.ID, kind)
	if err != nil {
		fmt.Fprintf(c.err, "conduit: %v\n", err)
		return exitForApproval(err)
	}
	if !*exec {
		fmt.Fprintf(c.out, "approved %s. The process that submitted it will run it (use -x to run it here).\n", shortID(run.ID))
		return ExitOK
	}
	run, err = c.svc.Execute(c.ctx, run.ID, service.ExecOpts{Stdout: c.out, Stderr: c.err})
	if run == nil {
		return c.fail("%v", err)
	}
	c.footer(run, err)
	if n := len(run.Attempts); n > 0 {
		return run.Attempts[n-1].ExitCode
	}
	return ExitOK
}

func exitForApproval(err error) int {
	switch {
	case errors.Is(err, service.ErrDenied), errors.Is(err, service.ErrSelfApproval):
		return ExitDenied
	}
	return exitFor(err)
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

// confirm asks on /dev/tty so piped stdin cannot answer for the operator.
func (c *cmd) confirm(prompt string) bool {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(c.err, "conduit: confirmation needs a terminal; pass -yes to skip")
		return false
	}
	defer func() { _ = tty.Close() }()
	fmt.Fprint(tty, prompt)
	line, _ := bufio.NewReader(tty).ReadString('\n')
	l := strings.ToLower(strings.TrimSpace(line))
	return l == "y" || l == "yes"
}

func (c *cmd) reject(args []string) int {
	if len(args) != 1 {
		return c.fail("reject: want one run id")
	}
	run, err := c.svc.Reject(c.ctx, args[0])
	if err != nil {
		fmt.Fprintf(c.err, "conduit: %v\n", err)
		return exitFor(err)
	}
	fmt.Fprintf(c.out, "rejected %s\n", shortID(run.ID))
	return ExitOK
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

func (c *cmd) token(args []string) int {
	if len(args) == 0 {
		return c.fail("token: want create, ls or revoke")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("token create", flag.ContinueOnError)
		fs.SetOutput(c.err)
		name := fs.String("name", "", "token name (also the principal: token:<name>)")
		scopes := fs.String("scope", "", "comma-separated: exec.read, exec.run, exec.approve, exec.admin; add :host to bind to a host")
		if err := fs.Parse(args[1:]); err != nil {
			return ExitError
		}
		secret, t, err := c.svc.CreateToken(*name, splitList(*scopes))
		if err != nil {
			return c.fail("%v", err)
		}
		fmt.Fprintf(c.err, "token %s (%s) created with scopes: %s\nThis is the only time the secret is shown.\n", t.Name, t.ID, t.Scopes)
		fmt.Fprintln(c.out, secret)
	case "ls":
		toks, err := c.svc.Tokens()
		if err != nil {
			return c.fail("%v", err)
		}
		tw := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tSCOPES\tCREATED\tLAST USED\tSTATUS")
		for _, t := range toks {
			status, last := "active", "never"
			if !t.RevokedAt.IsZero() {
				status = "revoked"
			}
			if !t.LastUsed.IsZero() {
				last = t.LastUsed.Local().Format("01-02 15:04")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Scopes, t.CreatedAt.Local().Format("01-02 15:04"), last, status)
		}
		return flushOK(tw)
	case "revoke":
		if len(args) != 2 {
			return c.fail("token revoke: want a token id or name")
		}
		ok, err := c.svc.RevokeToken(args[1])
		if err != nil {
			return c.fail("%v", err)
		}
		if !ok {
			fmt.Fprintf(c.err, "conduit: no active token %q\n", args[1])
			return ExitNotFound
		}
		fmt.Fprintf(c.out, "revoked %s\n", args[1])
	default:
		return c.fail("token: unknown subcommand %q", args[0])
	}
	return ExitOK
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// mcpCmd serves the MCP tools on stdio for an agent. The principal names the
// agent in every run it submits.
func (c *cmd) mcpCmd(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(c.err)
	who := fs.String("principal", "", "name recorded for this agent's runs (default mcp:<user>)")
	var dirs multiDir
	fs.Var(&dirs, "allow-dir", "directory the agent's commands may run under (repeatable; default: the current directory)")
	anyDir := fs.Bool("allow-any-dir", false, "do not restrict the agent's working directory")
	seeAll := fs.Bool("see-all", false, "let the agent read other principals' runs and output (default: its own only)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	p := *who
	if p == "" {
		name := "agent"
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
		p = "mcp:" + name
	} else if !strings.Contains(p, ":") {
		p = "mcp:" + p
	}
	c.svc.StartReaper(c.ctx, 30*time.Second, func(f string, a ...any) { fmt.Fprintf(c.err, f+"\n", a...) })
	if err := mcp.Serve(c.ctx, c.svc, p, mcp.Options{AllowDirs: dirs, AnyDir: *anyDir, SeeAll: *seeAll}, c.in, c.out, Version); err != nil {
		return c.fail("mcp: %v", err)
	}
	return ExitOK
}

type multiDir = multiFlag

// serve runs the HTTP API. It binds loopback or a unix socket unless told
// otherwise, and refuses to start without an allowed working directory.
func (c *cmd) serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(c.err)
	var dirs multiDir
	sock := fs.String("unix", "", "listen on this unix socket (mode 0600)")
	addr := fs.String("addr", "127.0.0.1:7421", "listen on this host:port (loopback unless -unsafe-bind)")
	unsafe := fs.Bool("unsafe-bind", false, "allow a non-loopback address (no TLS: put it on an authenticated network)")
	rate := fs.Int("rate", 240, "requests per minute per token")
	fs.Var(&dirs, "allow-dir", "directory commands may run under (repeatable; default: the current directory)")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if len(dirs) == 0 {
		wd, err := os.Getwd()
		if err != nil {
			return c.fail("%v", err)
		}
		dirs = multiDir{wd}
	}
	h, err := api.New(c.ctx, c.svc, api.Config{AllowDirs: dirs, RatePerMinute: *rate})
	if err != nil {
		return c.fail("%v", err)
	}
	ln, err := (api.Listen{Unix: *sock, Addr: *addr, Unsafe: *unsafe}).Bind()
	if err != nil {
		return c.fail("%v", err)
	}
	fmt.Fprintf(c.err, "conduit: serving %s on %s as host %s; commands confined to %s\n", Version, ln.Addr(), c.svc.Host(), strings.Join(dirs, ", "))
	if toks, _ := c.svc.Tokens(); len(toks) == 0 {
		fmt.Fprintln(c.err, "conduit: no tokens yet: `conduit token create -name <n> -scope exec.run,exec.read`")
	}
	c.svc.StartReaper(c.ctx, 30*time.Second, func(f string, a ...any) { fmt.Fprintf(c.err, f+"\n", a...) })
	if err := api.Serve(c.ctx, ln, h); err != nil {
		return c.fail("%v", err)
	}
	return ExitOK
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
