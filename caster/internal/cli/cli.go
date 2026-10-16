// Package cli is caster's command-line transport: parse flags, call the service
// layer, print. Exit codes follow the Hollis helper protocol so caster can
// stand in as a resolver anywhere one is expected: 0 ok, 2 not found, 3 denied,
// 1 error, 64 bad usage or input.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"

	"github.com/hollis-labs/caster/internal/service"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitError    = 1
	ExitNotFound = 2
	ExitDenied   = 3
	ExitUsage    = 64
)

const usage = `caster — content-addressed store and ref resolver

  caster put [-n [ns/]name] [-T title] [-t "tags"] [-m mime] [-s source] [file|-]
                                store text/bytes; prints the ref (caster://… if named, else cas://…)
  caster get|resolve <ref>      print content (torque://task/CW-…, tesseract://item/<id>, caster://ns/name[@rev], cas://sha256/…)
  caster head <ref>             metadata only
  caster snap [-n name] [-t tags] <ref>
                                materialise any ref and pin its bytes in CAS
  caster ls [-ns ns] [-t tag] [-json]
  caster log <[ns/]name>        revision history of a name
  caster search <fts query>
  caster scan [-json] [file|-]  find refs in text; only ones that resolve are reported
  caster expand [-budget n] [file|-]
                                replace resolvable refs in text with their contents
  caster actions <ref>          actions that apply to a ref
  caster act [-ns namespace] [-session id] [-agent id] [-workstream id] [-dry-run] <action> <ref>
                                run an action, e.g. save-handoff (context defaults from
                                CASTER_NAMESPACE, CASTER_SESSION_ID, CASTER_AGENT_ID, CASTER_WORKSTREAM_ID)
  caster shadow observe [-source s] [file|-]
                                detect refs in text and record what a card UI would show (counts only)
  caster shadow report [-since 24h] [-json]
                                per-extractor measurement of shadow detection
  caster shadow label <ref> true|false
                                record whether a detected ref was a true or false positive
  caster whoami                 who the service takes you for: identity, scopes, expiry
  caster renew                  extend this device's token another 30 days (remote mode)
  caster pair [-scope read,resolve,…] [-ttl 5m] [-label l] [-token-file path]
                                one-use pairing code for a client (local terminal only); with
                                -token-file, redeem it here and save the device token to that file
  caster devices                paired devices
  caster revoke <dev_…|grt_…>   revoke a device or an unused pairing code
  caster paths                  where data lives
  caster mcp                    serve the tools over MCP stdio (for agents)
  caster serve [-addr a]        serve the HTTP API (bearer auth; see README)
  caster token                  path of the API token file (created if missing)

Bare "ns/name", a CW-… task id, or a 64-hex hash are accepted wherever a ref is.
Exit: 0 ok, 2 not found, 3 denied, 1 error, 64 bad input.
`

// Run executes one command against the service layer.
func Run(ctx context.Context, svc *service.Service, args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(errw, usage)
		return ExitUsage
	}
	ctx = service.AsLocalOperator(ctx) // the CLI's caller is the process owner
	a := &app{svc: svc, in: in, out: out, err: errw}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "put":
		return a.put(ctx, rest)
	case "get", "resolve":
		return a.get(ctx, rest)
	case "head":
		return a.head(ctx, rest)
	case "snap":
		return a.snap(ctx, rest)
	case "ls":
		return a.ls(ctx, rest)
	case "log":
		return a.log(ctx, rest)
	case "search":
		return a.search(ctx, rest)
	case "scan":
		return a.scan(ctx, rest)
	case "expand":
		return a.expand(ctx, rest)
	case "actions":
		return a.actions(ctx, rest)
	case "act":
		return a.act(ctx, rest)
	case "shadow":
		return a.shadow(ctx, rest)
	case "whoami":
		return a.whoami(ctx)
	case "renew":
		return a.renew(ctx)
	case "pair":
		return a.pair(ctx, rest)
	case "devices":
		return a.devices(ctx, rest)
	case "revoke":
		return a.revoke(ctx, rest)
	}
	fmt.Fprintf(errw, "caster: unknown command %q\n\n%s", cmd, usage)
	return ExitUsage
}

type app struct {
	svc      *service.Service
	in       io.Reader
	out, err io.Writer
}

func (a *app) fail(err error) int {
	fmt.Fprintf(a.err, "caster: %v\n", err)
	switch service.KindOf(err) {
	case service.KindNotFound:
		return ExitNotFound
	case service.KindDenied, service.KindUnauthenticated:
		return ExitDenied
	case service.KindInvalid:
		return ExitUsage
	}
	return ExitError
}

// stateCode maps a resolution state to the helper-protocol exit code.
func stateCode(state string) int {
	switch state {
	case service.StateResolved:
		return ExitOK
	case service.StateDenied:
		return ExitDenied
	}
	return ExitNotFound
}

func (a *app) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.err)
	return fs
}

// parse lets flags appear before or after positional arguments.
func (a *app) parse(fs *flag.FlagSet, args []string) ([]string, bool) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, false
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, true
}

func (a *app) input(pos []string) (io.ReadCloser, error) {
	if len(pos) == 0 || pos[0] == "-" {
		return io.NopCloser(a.in), nil
	}
	return os.Open(pos[0])
}

func (a *app) text(pos []string) (string, error) {
	r, err := a.input(pos)
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	return string(b), err
}

func (a *app) one(pos []string, ok bool, usage string) (string, bool) {
	if !ok || len(pos) != 1 {
		fmt.Fprintln(a.err, "usage: "+usage)
		return "", false
	}
	return pos[0], true
}

func (a *app) put(ctx context.Context, args []string) int {
	fs := a.flags("put")
	req := service.PutRequest{}
	fs.StringVar(&req.Name, "n", "", "name ([ns/]name)")
	fs.StringVar(&req.Title, "T", "", "title")
	fs.StringVar(&req.Tags, "t", "", "space separated tags")
	fs.StringVar(&req.Mime, "m", "", "mime type")
	fs.StringVar(&req.Source, "s", "", "source ref (provenance)")
	pos, ok := a.parse(fs, args)
	if !ok {
		return ExitUsage
	}
	r, err := a.input(pos)
	if err != nil {
		return a.fail(err)
	}
	defer r.Close()
	req.R = r
	res, err := a.svc.Notes.Put(ctx, req)
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintln(a.out, res.Ref)
	if req.Name != "" {
		fmt.Fprintf(a.err, "%s  %s  %d bytes\n", res.Ref, res.CAS[:len("cas://sha256/")+12], res.Size)
	}
	return ExitOK
}

func (a *app) get(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.err, "usage: caster get <ref>")
		return ExitUsage
	}
	res, err := a.svc.Refs.Get(ctx, service.GetRequest{Ref: args[0], MaxBytes: -1})
	if err != nil {
		return a.fail(err)
	}
	if res.State != service.StateResolved {
		fmt.Fprintf(a.err, "caster: %s: %s\n", res.Ref, res.State)
		return stateCode(res.State)
	}
	_, _ = a.out.Write(res.Data)
	return ExitOK
}

func (a *app) head(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.err, "usage: caster head <ref>")
		return ExitUsage
	}
	m, err := a.svc.Refs.Head(ctx, service.RefRequest{Ref: args[0]})
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.out, "ref:      %s\nstate:    %s\nfloating: %v\n", m.Ref, m.State, m.Floating)
	if m.State == service.StateResolved {
		fmt.Fprintf(a.out, "kind:     %s\ntitle:    %s\nmime:     %s\nsize:     %d\nrevision: %s\nhash:     %s\n", m.Kind, m.Title, m.Mime, m.Size, m.Revision, m.SHA256)
	}
	return stateCode(m.State)
}

func (a *app) snap(ctx context.Context, args []string) int {
	fs := a.flags("snap")
	req := service.SnapRequest{}
	fs.StringVar(&req.Name, "n", "", "also name the snapshot ([ns/]name)")
	fs.StringVar(&req.Tags, "t", "", "tags")
	pos, ok := a.parse(fs, args)
	r, ok := a.one(pos, ok, "caster snap [-n name] [-t tags] <ref>")
	if !ok {
		return ExitUsage
	}
	req.Ref = r
	res, err := a.svc.Refs.Snapshot(ctx, req)
	if err != nil {
		return a.fail(err)
	}
	if res.State != service.StateResolved {
		fmt.Fprintf(a.err, "caster: %s: %s\n", res.Source, res.State)
		return stateCode(res.State)
	}
	fmt.Fprintln(a.out, res.CAS)
	if res.Ref != "" {
		fmt.Fprintln(a.out, res.Ref)
	}
	return ExitOK
}

func (a *app) ls(ctx context.Context, args []string) int {
	fs := a.flags("ls")
	req := service.ListRequest{}
	fs.StringVar(&req.NS, "ns", "", "namespace")
	fs.StringVar(&req.Tag, "t", "", "tag")
	asJSON := fs.Bool("json", false, "json output")
	if _, ok := a.parse(fs, args); !ok {
		return ExitUsage
	}
	res, err := a.svc.Notes.List(ctx, req)
	if err != nil {
		return a.fail(err)
	}
	if *asJSON {
		_ = json.NewEncoder(a.out).Encode(res)
		return ExitOK
	}
	tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	for _, it := range res.Items {
		fmt.Fprintf(tw, "%s\t@%d\t%s\t%s\t%s\n", strings.TrimPrefix(it.Ref, "caster://"), it.Rev, it.Updated.Local().Format("2006-01-02 15:04"), it.Title, it.Tags)
	}
	_ = tw.Flush()
	return ExitOK
}

func (a *app) log(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.err, "usage: caster log <[ns/]name>")
		return ExitUsage
	}
	res, err := a.svc.Notes.Log(ctx, service.LogRequest{Name: args[0]})
	if err != nil {
		return a.fail(err)
	}
	for _, r := range res.Revisions {
		fmt.Fprintf(a.out, "@%d  %s  %s  %s\n", r.Rev, r.At.Local().Format("2006-01-02 15:04:05"), r.CAS[len("cas://sha256/"):][:12], r.Title)
	}
	return ExitOK
}

func (a *app) search(ctx context.Context, args []string) int {
	res, err := a.svc.Notes.Search(ctx, service.SearchRequest{Query: strings.Join(args, " ")})
	if err != nil {
		return a.fail(err)
	}
	for _, h := range res.Hits {
		who := h.CAS
		if len(h.Names) > 0 {
			who = strings.Join(h.Names, ", ")
		}
		fmt.Fprintf(a.out, "%s\n    %s\n", who, strings.ReplaceAll(h.Snippet, "\n", " "))
	}
	if len(res.Hits) == 0 {
		return ExitNotFound
	}
	return ExitOK
}

func (a *app) scan(ctx context.Context, args []string) int {
	fs := a.flags("scan")
	asJSON := fs.Bool("json", false, "json output")
	pos, ok := a.parse(fs, args)
	if !ok {
		return ExitUsage
	}
	text, err := a.text(pos)
	if err != nil {
		return a.fail(err)
	}
	res, err := a.svc.Refs.Scan(ctx, service.ScanRequest{Text: text})
	if err != nil {
		return a.fail(err)
	}
	for _, p := range res.Problems {
		fmt.Fprintf(a.err, "caster: %s: %s\n", p.Ref, p.Error)
	}
	if *asJSON {
		_ = json.NewEncoder(a.out).Encode(res.Refs)
		return ExitOK
	}
	for _, m := range res.Refs {
		fmt.Fprintf(a.out, "%s\t%s\t%s\n", m.Ref, m.Kind, m.Title)
	}
	return ExitOK
}

func (a *app) expand(ctx context.Context, args []string) int {
	fs := a.flags("expand")
	req := service.ExpandRequest{}
	fs.IntVar(&req.Budget, "budget", 0, "max bytes inserted per ref (default 16384)")
	pos, ok := a.parse(fs, args)
	if !ok {
		return ExitUsage
	}
	text, err := a.text(pos)
	if err != nil {
		return a.fail(err)
	}
	req.Text = text
	res, err := a.svc.Refs.Expand(ctx, req)
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprint(a.out, res.Text)
	return ExitOK
}

func (a *app) actions(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.err, "usage: caster actions <ref>")
		return ExitUsage
	}
	res, err := a.svc.Actions.List(ctx, service.ActionsRequest{Ref: args[0]})
	if err != nil {
		return a.fail(err)
	}
	if res.State != service.StateResolved {
		fmt.Fprintf(a.err, "caster: %s: %s\n", res.Ref, res.State)
		return stateCode(res.State)
	}
	for _, ac := range res.Actions {
		fmt.Fprintf(a.out, "%s\t%s\t%s\tneeds: %s\n", ac.ID, ac.Effect, ac.Label, strings.Join(ac.Needs, ", "))
	}
	return ExitOK
}

func (a *app) act(ctx context.Context, args []string) int {
	fs := a.flags("act")
	req := service.ActRequest{Context: service.ActionContext{
		Namespace: os.Getenv("CASTER_NAMESPACE"), SessionID: os.Getenv("CASTER_SESSION_ID"),
		AgentID: os.Getenv("CASTER_AGENT_ID"), WorkstreamID: os.Getenv("CASTER_WORKSTREAM_ID")}}
	fs.StringVar(&req.Context.Namespace, "ns", req.Context.Namespace, "Tesseract workspace namespace")
	fs.StringVar(&req.Context.SessionID, "session", req.Context.SessionID, "authoring session id")
	fs.StringVar(&req.Context.AgentID, "agent", req.Context.AgentID, "authoring agent id")
	fs.StringVar(&req.Context.WorkstreamID, "workstream", req.Context.WorkstreamID, "workstream id")
	fs.BoolVar(&req.DryRun, "dry-run", false, "plan only: store and write nothing")
	pos, ok := a.parse(fs, args)
	if !ok || len(pos) != 2 {
		fmt.Fprintln(a.err, "usage: caster act [flags] <action> <ref>")
		return ExitUsage
	}
	req.Action, req.Ref = pos[0], pos[1]
	res, err := a.svc.Actions.Run(ctx, req)
	if err != nil {
		return a.fail(err)
	}
	if res.State != service.StateResolved {
		fmt.Fprintf(a.err, "caster: %s: %s\n", req.Ref, res.State)
		return stateCode(res.State)
	}
	if res.Plan != "" {
		fmt.Fprintf(a.out, "dry run: %s\nkey: %s\n", res.Plan, res.Key)
		return ExitOK
	}
	fmt.Fprintln(a.out, res.Outcome)
	fmt.Fprintf(a.err, "%s %s  source %s  key %s\n", res.Action, res.Status, res.Source, res.Key)
	return ExitOK
}

func (a *app) shadow(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: caster shadow observe|report|label …")
		return ExitUsage
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "observe":
		fs := a.flags("shadow observe")
		req := service.ObserveRequest{}
		fs.StringVar(&req.Source, "source", "", "opaque label for where the text came from")
		pos, ok := a.parse(fs, rest)
		if !ok {
			return ExitUsage
		}
		text, err := a.text(pos)
		if err != nil {
			return a.fail(err)
		}
		req.Text = text
		res, err := a.svc.Shadow.Observe(ctx, req)
		if err != nil {
			return a.fail(err)
		}
		fmt.Fprintf(a.out, "%s: %d candidates, %d resolved, %d would show (cached: %v)\n", res.Mode, res.Candidates, res.Resolved, res.WouldShow, res.Cached)
	case "report":
		fs := a.flags("shadow report")
		since := fs.Duration("since", 0, "only observations seen within this duration, e.g. 24h")
		asJSON := fs.Bool("json", false, "json output")
		if _, ok := a.parse(fs, rest); !ok {
			return ExitUsage
		}
		req := service.ReportRequest{}
		if *since > 0 {
			req.Since = time.Now().Add(-*since)
		}
		res, err := a.svc.Shadow.Report(ctx, req)
		if err != nil {
			return a.fail(err)
		}
		if *asJSON {
			_ = json.NewEncoder(a.out).Encode(res)
			return ExitOK
		}
		fmt.Fprintf(a.out, "%s, generation %d: %d observations (%d repeats served from cache)\n\n", res.Mode, res.Generation, res.Observations, res.Repeats)
		tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "extractor\tconf\tcandidates\tresolved\trate\tshow\tdistinct\terrors\tprecision")
		for _, e := range res.Extractors {
			prec := "-"
			if e.Precision != nil {
				prec = fmt.Sprintf("%.0f%% (%d/%d)", *e.Precision*100, e.LabeledTrue, e.LabeledTrue+e.LabeledFalse)
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%.0f%%\t%d\t%d\t%d\t%s\n", e.Extractor, e.Confidence, e.Candidates, e.Resolved, e.ResolveRate*100, e.WouldShow, e.DistinctRefs, e.Errors, prec)
		}
		_ = tw.Flush()
		if len(res.TopUnresolved) > 0 {
			fmt.Fprintln(a.out, "\ntop unresolved:")
			for _, c := range res.TopUnresolved {
				fmt.Fprintf(a.out, "  %4d  %s\n", c.Count, c.Ref)
			}
		}
	case "label":
		if len(rest) != 2 {
			fmt.Fprintln(a.err, "usage: caster shadow label <ref> true|false")
			return ExitUsage
		}
		res, err := a.svc.Shadow.Label(ctx, service.LabelRequest{Ref: rest[0], Verdict: rest[1]})
		if err != nil {
			return a.fail(err)
		}
		fmt.Fprintf(a.out, "labelled %d detections\n", res.Updated)
	default:
		fmt.Fprintf(a.err, "caster shadow: unknown subcommand %q\n", sub)
		return ExitUsage
	}
	return ExitOK
}

func (a *app) pair(ctx context.Context, args []string) int {
	fs := a.flags("pair")
	req := service.PairRequest{}
	var scopes string
	var ttl time.Duration
	fs.StringVar(&scopes, "scope", "", "comma-separated scopes: "+strings.Join(service.AllScopes, ", "))
	fs.DurationVar(&ttl, "ttl", 0, "how long the code lives (default 5m, max 1h)")
	fs.StringVar(&req.Label, "label", "", "device label")
	tokenFile := fs.String("token-file", "", "redeem the code here and write the device token to this file (0600)")
	if _, ok := a.parse(fs, args); !ok {
		return ExitUsage
	}
	req.Scopes, req.TTLSeconds = []string{scopes}, int(ttl.Seconds())
	g, err := a.svc.Access.CreateGrant(ctx, req)
	if err != nil {
		return a.fail(err)
	}
	if *tokenFile == "" {
		fmt.Fprintln(a.out, g.Code)
		fmt.Fprintf(a.err, "grant %s  scopes %s  expires %s\nThe code is shown once. Redeem it with POST /v1/pair/exchange {\"code\":…} before it expires.\n",
			g.GrantID, strings.Join(g.Scopes, ","), g.ExpiresAt.Local().Format(time.RFC3339))
		return ExitOK
	}
	ex, err := a.svc.Access.Exchange(ctx, service.ExchangeRequest{Code: g.Code, Label: req.Label})
	if err != nil {
		return a.fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(*tokenFile), 0o700); err != nil {
		return a.fail(err)
	}
	if err := tokenfile.Write(*tokenFile, ex.Token); err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.err, "device %s  scopes %s  expires %s\ntoken written to %s\n", ex.DeviceID, strings.Join(ex.Scopes, ","), ex.ExpiresAt.Local().Format("2006-01-02"), *tokenFile)
	return ExitOK
}

func (a *app) devices(ctx context.Context, _ []string) int {
	res, err := a.svc.Access.Devices(ctx, struct{}{})
	if err != nil {
		return a.fail(err)
	}
	tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	for _, d := range res.Devices {
		state, used := "active", "never"
		if d.Revoked {
			state = "revoked"
		} else if time.Now().After(d.ExpiresAt) {
			state = "expired"
		}
		if d.LastUsedAt != nil {
			used = d.LastUsedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\texpires %s\tused %s\t%s\n", d.ID, state, d.Label, d.ExpiresAt.Local().Format("2006-01-02"), used, strings.Join(d.Scopes, ","))
	}
	_ = tw.Flush()
	return ExitOK
}

func (a *app) revoke(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.err, "usage: caster revoke <dev_…|grt_…>")
		return ExitUsage
	}
	res, err := a.svc.Access.Revoke(ctx, service.RevokeRequest{ID: args[0]})
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.out, "revoked %s\n", res.Revoked)
	return ExitOK
}

func (a *app) whoami(ctx context.Context) int {
	w, err := a.svc.Access.Whoami(ctx, struct{}{})
	if err != nil {
		return a.fail(err)
	}
	exp := "never"
	if w.ExpiresAt != nil {
		exp = w.ExpiresAt.Local().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(a.out, "id:      %s\nkind:    %s\nlabel:   %s\nscopes:  %s\nexpires: %s\n", w.ID, w.Kind, w.Label, strings.Join(w.Scopes, ", "), exp)
	return ExitOK
}

func (a *app) renew(ctx context.Context) int {
	d, err := a.svc.Access.Renew(ctx, service.RenewRequest{})
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.out, "%s renewed until %s\n", d.ID, d.ExpiresAt.Local().Format("2006-01-02"))
	return ExitOK
}
