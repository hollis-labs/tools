// Package cli is caster's command-line transport. It calls only the service
// layer. Exit codes follow the Hollis helper protocol so caster can stand in as
// a resolver anywhere one is expected: 0 ok, 2 not found, 3 denied, 1 other.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/hollis-labs/caster/internal/resolver"
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
  caster get|resolve <ref>      print content (torque://task/CW-…, tesseract://ns/key, caster://ns/name[@rev], cas://sha256/…)
  caster head <ref>             metadata only
  caster snap [-n name] [-t tags] <ref>
                                materialise any ref and pin its bytes in CAS
  caster ls [-ns ns] [-t tag] [-json]
  caster log <[ns/]name>        revision history of a name
  caster search <fts query>
  caster scan [-json] [file|-]  find refs in text; only ones that resolve are reported
  caster expand [-budget n] [file|-]
                                replace resolvable refs in text with their contents
  caster paths                  where data lives
  caster mcp                    serve the tools over MCP stdio (for agents)

Bare "ns/name", a CW-… task id, or a 64-hex hash are accepted wherever a ref is.
Exit: 0 ok, 2 not found, 3 denied, 1 error.
`

// Run executes one command.
func Run(ctx context.Context, svc *service.Service, args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(errw, usage)
		return ExitUsage
	}
	cmd, rest := args[0], args[1:]
	a := &app{svc: svc, in: in, out: out, err: errw}
	var code int
	switch cmd {
	case "put":
		code = a.put(rest)
	case "get", "resolve":
		code = a.get(ctx, rest)
	case "head":
		code = a.head(ctx, rest)
	case "snap":
		code = a.snap(ctx, rest)
	case "ls":
		code = a.ls(rest)
	case "log":
		code = a.log(rest)
	case "search":
		code = a.search(rest)
	case "scan":
		code = a.scan(ctx, rest)
	case "expand":
		code = a.expand(ctx, rest)
	default:
		fmt.Fprintf(errw, "caster: unknown command %q\n\n%s", cmd, usage)
		return ExitUsage
	}
	return code
}

type app struct {
	svc      *service.Service
	in       io.Reader
	out, err io.Writer
}

func (a *app) fail(err error) int {
	fmt.Fprintf(a.err, "caster: %v\n", err)
	switch {
	case service.IsNotFound(err):
		return ExitNotFound
	case errors.Is(err, resolver.ErrUnavailable):
		return ExitError
	}
	return ExitError
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

func (a *app) reader(pos []string) (io.ReadCloser, error) {
	if len(pos) == 0 || pos[0] == "-" {
		return io.NopCloser(a.in), nil
	}
	return os.Open(pos[0])
}

func (a *app) put(args []string) int {
	fs := a.flags("put")
	name := fs.String("n", "", "name ([ns/]name)")
	title := fs.String("T", "", "title")
	tags := fs.String("t", "", "space separated tags")
	mime := fs.String("m", "", "mime type")
	src := fs.String("s", "", "source ref (provenance)")
	pos, ok := a.parse(fs, args)
	if !ok {
		return ExitUsage
	}
	r, err := a.reader(pos)
	if err != nil {
		return a.fail(err)
	}
	defer r.Close()
	res, err := a.svc.Put(service.PutInput{Name: *name, Title: *title, Tags: *tags, Mime: *mime, Source: *src, R: r})
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintln(a.out, res.Ref)
	if res.Name != nil {
		fmt.Fprintf(a.err, "%s  cas://sha256/%s  %d bytes\n", res.Ref, res.Blob.Hash[:12], res.Blob.Size)
	}
	return ExitOK
}

// stateCode maps a resolver state to the helper-protocol exit code.
func stateCode(state string) int {
	switch state {
	case resolver.StateResolved:
		return ExitOK
	case resolver.StateDenied:
		return ExitDenied
	}
	return ExitNotFound
}

func (a *app) get(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.err, "usage: caster get <ref>")
		return ExitUsage
	}
	c, err := a.svc.Get(ctx, args[0])
	if err != nil {
		return a.fail(err)
	}
	if c.State != resolver.StateResolved {
		fmt.Fprintf(a.err, "caster: %s: %s\n", c.Ref, c.State)
		return stateCode(c.State)
	}
	_, _ = a.out.Write(c.Data)
	return ExitOK
}

func (a *app) head(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.err, "usage: caster head <ref>")
		return ExitUsage
	}
	m, err := a.svc.Head(ctx, args[0])
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.out, "ref:      %s\nstate:    %s\nfloating: %v\n", m.Ref, m.State, m.Ref.Floating())
	if m.State == resolver.StateResolved {
		fmt.Fprintf(a.out, "kind:     %s\ntitle:    %s\nmime:     %s\nsize:     %d\nrevision: %s\nhash:     %s\n", m.Kind, m.Title, m.Mime, m.Size, m.Revision, m.ContentHash)
	}
	return stateCode(m.State)
}

func (a *app) snap(ctx context.Context, args []string) int {
	fs := a.flags("snap")
	name := fs.String("n", "", "also name the snapshot ([ns/]name)")
	tags := fs.String("t", "", "tags")
	pos, ok := a.parse(fs, args)
	if !ok || len(pos) != 1 {
		fmt.Fprintln(a.err, "usage: caster snap [-n name] [-t tags] <ref>")
		return ExitUsage
	}
	res, meta, err := a.svc.Snapshot(ctx, pos[0], *name, *tags)
	if err != nil {
		if meta.State != "" && meta.State != resolver.StateResolved {
			fmt.Fprintf(a.err, "caster: %v\n", err)
			return stateCode(meta.State)
		}
		return a.fail(err)
	}
	fmt.Fprintf(a.out, "cas://sha256/%s\n", res.Blob.Hash)
	if res.Name != nil {
		fmt.Fprintln(a.out, res.Ref)
	}
	return ExitOK
}

func (a *app) ls(args []string) int {
	fs := a.flags("ls")
	ns := fs.String("ns", "", "namespace")
	tag := fs.String("t", "", "tag")
	asJSON := fs.Bool("json", false, "json output")
	if _, ok := a.parse(fs, args); !ok {
		return ExitUsage
	}
	names, err := a.svc.List(*ns, *tag)
	if err != nil {
		return a.fail(err)
	}
	if *asJSON {
		_ = json.NewEncoder(a.out).Encode(names)
		return ExitOK
	}
	tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	for _, n := range names {
		fmt.Fprintf(tw, "%s/%s\t@%d\t%s\t%s\t%s\n", n.NS, n.Name, n.Rev, n.CreatedAt.Local().Format("2006-01-02 15:04"), n.Title, n.Tags)
	}
	_ = tw.Flush()
	return ExitOK
}

func (a *app) log(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.err, "usage: caster log <[ns/]name>")
		return ExitUsage
	}
	h, err := a.svc.History(args[0])
	if err != nil {
		return a.fail(err)
	}
	for _, n := range h {
		fmt.Fprintf(a.out, "@%d  %s  %s  %s\n", n.Rev, n.CreatedAt.Local().Format("2006-01-02 15:04:05"), n.Hash[:12], n.Title)
	}
	return ExitOK
}

func (a *app) search(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.err, "usage: caster search <query>")
		return ExitUsage
	}
	hits, err := a.svc.Search(strings.Join(args, " "), 20)
	if err != nil {
		return a.fail(err)
	}
	for _, h := range hits {
		who := "cas://sha256/" + h.Hash[:12]
		if len(h.Names) > 0 {
			who = strings.Join(h.Names, ", ")
		}
		fmt.Fprintf(a.out, "%s\n    %s\n", who, strings.ReplaceAll(h.Snippet, "\n", " "))
	}
	if len(hits) == 0 {
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
	r, err := a.reader(pos)
	if err != nil {
		return a.fail(err)
	}
	defer r.Close()
	text, err := io.ReadAll(r)
	if err != nil {
		return a.fail(err)
	}
	type row struct {
		Ref      string `json:"ref"`
		Kind     string `json:"kind"`
		Title    string `json:"title"`
		Revision string `json:"revision,omitempty"`
		Floating bool   `json:"floating"`
	}
	var rows []row
	for _, f := range a.svc.Scan(ctx, string(text)) {
		if f.Err != nil {
			fmt.Fprintf(a.err, "caster: %s: %v\n", f.Ref, f.Err)
			continue
		}
		if f.Meta.State != resolver.StateResolved { // quiet by default
			continue
		}
		rows = append(rows, row{f.Ref.String(), f.Meta.Kind, f.Meta.Title, f.Meta.Revision, f.Ref.Floating()})
	}
	if *asJSON {
		_ = json.NewEncoder(a.out).Encode(rows)
		return ExitOK
	}
	for _, r := range rows {
		fmt.Fprintf(a.out, "%s\t%s\t%s\n", r.Ref, r.Kind, r.Title)
	}
	return ExitOK
}

func (a *app) expand(ctx context.Context, args []string) int {
	fs := a.flags("expand")
	budget := fs.Int("budget", 0, "max bytes inserted per ref (default 16384)")
	pos, ok := a.parse(fs, args)
	if !ok {
		return ExitUsage
	}
	r, err := a.reader(pos)
	if err != nil {
		return a.fail(err)
	}
	defer r.Close()
	text, err := io.ReadAll(r)
	if err != nil {
		return a.fail(err)
	}
	out, _ := a.svc.Expand(ctx, string(text), *budget)
	fmt.Fprint(a.out, out)
	return ExitOK
}
