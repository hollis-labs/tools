// Package cmdparse turns a shell command string into a normalised structure:
// segments split on &&, ||, ;, pipes and newlines, with env prefixes and
// process wrappers peeled off, sh -c / ssh / xargs unwrapped to a depth cap,
// redirects set aside, and every segment given a canonical command and an
// effect class.
//
// The parse is built for policy, not for execution: anything it cannot
// attest (a dynamic command word, a function definition, a heredoc, a parse
// error) is marked Opaque so a policy can fail closed. The original string is
// what gets executed; this package never rewrites it.
package cmdparse

import (
	"fmt"
	"path"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/hollis-labs/libs/core/panics"
)

// MaxDepth caps nested unwrapping (sh -c inside ssh inside xargs ...) and the
// number of stacked wrappers on one segment.
const MaxDepth = 3

// MaxWrappers caps stacked wrapper prefixes on one segment. Beyond it the
// segment is opaque.
const MaxWrappers = 32

// Effect classifies what a segment can do to the world.
type Effect string

const (
	EffectNone        Effect = "none" // pure assignment, no-op
	EffectRead        Effect = "read"
	EffectWrite       Effect = "write"
	EffectNetwork     Effect = "network"
	EffectExec        Effect = "exec"
	EffectUnknown     Effect = "unknown" // binary not in the table
	EffectDestructive Effect = "destructive"
)

var effectRank = map[Effect]int{
	EffectNone: 0, EffectRead: 1, EffectWrite: 2, EffectNetwork: 3,
	EffectExec: 4, EffectUnknown: 4, EffectDestructive: 5,
}

// Rank orders effects by how much they can do; unknown ranks with exec.
func (e Effect) Rank() int { return effectRank[e] }

// MaxEffect returns the more severe of a and b.
func MaxEffect(a, b Effect) Effect {
	if b.Rank() > a.Rank() {
		return b
	}
	return a
}

// Redirect is a redirection set aside from the argv.
type Redirect struct {
	Op      string `json:"op"`      // ">", ">>", "<", "<<", ...
	Target  string `json:"target"`  // static text, or "" when dynamic
	Dynamic bool   `json:"dynamic"` // target is not statically known
}

// Segment is one simple command.
type Segment struct {
	Op        string     `json:"op"`                  // joins to the previous segment: "", "&&", "||", ";", "|", "|&", "&"
	Via       string     `json:"via,omitempty"`       // how it was reached: "sh -c", "ssh", "xargs", "$(", "<(", compound kind
	Depth     int        `json:"depth"`               // unwrap depth; 0 is the command as typed
	Raw       string     `json:"raw"`                 // source text of the segment (display view)
	Env       []string   `json:"env,omitempty"`       // names assigned as a prefix
	Wrappers  []string   `json:"wrappers,omitempty"`  // peeled process wrappers, outermost first
	Argv      []string   `json:"argv"`                // argv after wrappers; dynamic words are kept as their source text
	DynArg    []bool     `json:"dyn,omitempty"`       // parallel to Argv: word is not statically known
	Bin       string     `json:"bin"`                 // basename of argv[0]
	Sub       string     `json:"sub,omitempty"`       // subcommand, by the arity table
	Flags     []string   `json:"flags,omitempty"`     // words starting with '-'
	Redirects []Redirect `json:"redirects,omitempty"` // set aside
	Effect    Effect     `json:"effect"`
	Risks     []string   `json:"risks,omitempty"` // sudo, interactive, heredoc, dynamic-arg, ...
	Opaque    bool       `json:"opaque,omitempty"`
}

// Has reports whether the segment carries the risk flag.
func (s *Segment) Has(risk string) bool {
	for _, r := range s.Risks {
		if r == risk {
			return true
		}
	}
	return false
}

func (s *Segment) risk(r string) {
	if !s.Has(r) {
		s.Risks = append(s.Risks, r)
	}
}

// Parsed is the whole command.
type Parsed struct {
	Raw      string    `json:"raw"`
	Segments []Segment `json:"segments"`
	Effect   Effect    `json:"effect"`             // max over segments
	Opaque   bool      `json:"opaque,omitempty"`   // any segment opaque, or the parse failed
	Err      string    `json:"err,omitempty"`      // parse error; Segments is empty
	Internal bool      `json:"internal,omitempty"` // the parser itself failed (a bug), not the input
}

// parseImpl is the parser entry; a variable so tests can make it misbehave.
var parseImpl = parseAt

// Parse parses a command string. It never returns an error and never panics: a
// failure, including a panic inside the parser, is recorded in Parsed.Err and
// makes the result Opaque, which policy treats as needing a human.
func Parse(src string) *Parsed {
	p := &Parsed{Raw: src, Effect: EffectNone}
	var segs []Segment
	err := panics.Call("cmdparse.Parse", func() error {
		var e error
		segs, e = parseImpl(src, 0, "")
		return e
	})
	if ref, panicked := panics.Ref(err); panicked {
		p.Internal = true
		err = fmt.Errorf("internal parser error (ref %s)", ref)
	}
	if err != nil {
		p.Err = err.Error()
		p.Opaque = true
		p.Effect = EffectUnknown
		return p
	}
	p.Segments = segs
	for i := range segs {
		p.Effect = MaxEffect(p.Effect, segs[i].Effect)
		if segs[i].Opaque {
			p.Opaque = true
		}
	}
	return p
}

func parseAt(src string, depth int, via string) ([]Segment, error) {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		return nil, err
	}
	w := &walker{src: src, depth: depth, seen: map[syntax.Node]bool{}}
	for i, st := range f.Stmts {
		op := ";"
		if i == 0 {
			op = ""
		}
		w.stmt(st, op, via)
	}
	// Fail closed on anything the walk did not account for.
	if len(f.Stmts) > 0 && len(w.out) == 0 {
		w.out = append(w.out, Segment{Via: via, Depth: depth, Raw: src, Effect: EffectUnknown, Opaque: true, Risks: []string{"no-segments"}})
	}
	unseen := false
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			if !w.seen[n] {
				unseen = true
			}
		}
		return true
	})
	if unseen {
		w.out = append(w.out, Segment{Op: ";", Via: via, Depth: depth, Raw: src, Effect: EffectUnknown, Opaque: true, Risks: []string{"unanalyzed-substitution"}})
	}
	return w.out, nil
}

type walker struct {
	src   string
	depth int
	out   []Segment
	// pendingInner holds segments unwrapped from the call being built (sh -c,
	// ssh, xargs); stmt appends them right after the call itself.
	pendingInner []Segment
	// seen records the substitution nodes the walk analyzed, so any it did not
	// reach (an unusual AST position) can be reported instead of ignored.
	seen map[syntax.Node]bool
}

func (w *walker) text(n syntax.Node) string {
	s, e := n.Pos().Offset(), n.End().Offset()
	if e > uint(len(w.src)) || s > e {
		return ""
	}
	return w.src[s:e]
}

// stmt flattens a statement. op is the operator joining it to the previous
// one; via names the construct that contains it.
func (w *walker) stmt(st *syntax.Stmt, op, via string) {
	w.stmtInner(st, op, via)
	if _, simple := st.Cmd.(*syntax.CallExpr); !simple && st.Cmd != nil && len(st.Redirs) > 0 {
		// Redirects on a compound command ({ ...; } > f, ( ... ) > f, if ... fi > f)
		// apply to everything inside it; give them a segment of their own.
		seg := Segment{Op: ";", Via: via, Depth: w.depth, Raw: w.text(st), Effect: EffectNone}
		w.redirects(&seg, st.Redirs)
		seg.Effect = redirectEffect(seg.Redirects)
		if seg.Opaque {
			seg.Effect = MaxEffect(seg.Effect, EffectUnknown)
		}
		w.out = append(w.out, seg)
	}
}

func (w *walker) stmtInner(st *syntax.Stmt, op, via string) {
	switch c := st.Cmd.(type) {
	case nil:
		return
	case *syntax.CallExpr:
		seg := w.call(st, c, op, via)
		if st.Background {
			seg.risk("background")
		}
		w.out = append(w.out, seg)
		w.out = append(w.out, w.pendingInner...)
		w.pendingInner = nil
	case *syntax.BinaryCmd:
		w.stmt(c.X, op, via)
		w.stmt(c.Y, c.Op.String(), via)
	case *syntax.Subshell:
		w.list(c.Stmts, op, "(")
	case *syntax.Block:
		w.list(c.Stmts, op, "{")
	case *syntax.IfClause:
		w.list(c.Cond, op, "if")
		w.list(c.Then, ";", "if")
		if c.Else != nil {
			w.stmt(&syntax.Stmt{Cmd: c.Else}, ";", "if")
		}
	case *syntax.WhileClause:
		w.list(c.Cond, op, "while")
		w.list(c.Do, ";", "while")
	case *syntax.ForClause:
		w.list(c.Do, op, "for")
		w.opaque(st, op, "for", "loop word list is not attested")
	case *syntax.CaseClause:
		for _, it := range c.Items {
			w.list(it.Stmts, op, "case")
		}
	case *syntax.TimeClause:
		if c.Stmt != nil {
			w.stmt(c.Stmt, op, via)
		}
	default:
		// FuncDecl, DeclClause, TestClause, ArithmCmd, LetClause, Coproc ...
		w.opaque(st, op, via, "unattested construct")
	}
}

func (w *walker) list(stmts []*syntax.Stmt, op, via string) {
	for i, s := range stmts {
		o := ";"
		if i == 0 {
			o = op
		}
		w.stmt(s, o, via)
	}
}

func (w *walker) opaque(st *syntax.Stmt, op, via, why string) {
	seg := Segment{Op: op, Via: via, Depth: w.depth, Raw: w.text(st), Effect: EffectUnknown, Opaque: true}
	seg.risk(why)
	w.out = append(w.out, seg)
}

// word reports a word's static text, or its source and dynamic=true.
func (w *walker) word(wd *syntax.Word) (text string, dynamic bool) {
	if isStatic(wd) {
		s, err := expand.Literal(nil, wd)
		if err == nil {
			return s, false
		}
	}
	return w.text(wd), true
}

func isStatic(wd *syntax.Word) bool {
	for _, p := range wd.Parts {
		switch x := p.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			for _, q := range x.Parts {
				if _, ok := q.(*syntax.Lit); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// substitutions appends the commands inside $(...) and <(...) of a word as
// segments of their own, so policy sees them.
func (w *walker) substitutions(wd *syntax.Word) {
	syntax.Walk(wd, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.CmdSubst:
			w.seen[x] = true
			w.list(x.Stmts, ";", "$(")
			return false
		case *syntax.ProcSubst:
			w.seen[x] = true
			w.list(x.Stmts, ";", "<(")
			return false
		}
		return true
	})
}

func (w *walker) call(st *syntax.Stmt, c *syntax.CallExpr, op, via string) Segment {
	seg := Segment{Op: op, Via: via, Depth: w.depth, Raw: w.text(st), Effect: EffectNone}

	for _, a := range c.Assigns {
		seg.Env = append(seg.Env, a.Name.Value)
		if envSensitive(a.Name.Value) {
			seg.risk("env-sensitive")
		}
		if a.Value != nil {
			w.substitutions(a.Value)
			if t, dyn := w.word(a.Value); !dyn && embedsSubstitution(t) {
				// The value is data now but a later $((x)), ${x@P}, printf -v or
				// read can re-evaluate it as code.
				seg.risk("embedded-substitution")
			}
		}
	}

	var argv []string
	var dyn []bool
	for _, wd := range c.Args {
		t, d := w.word(wd)
		argv = append(argv, t)
		dyn = append(dyn, d)
		if d {
			w.substitutions(wd)
		} else if embedsSubstitution(t) {
			seg.risk("embedded-substitution")
		}
	}

	w.redirects(&seg, st.Redirs)

	if len(argv) == 0 {
		// Pure assignment (or a bare redirect): no command runs.
		if len(seg.Redirects) > 0 {
			seg.Effect = redirectEffect(seg.Redirects)
		}
		return seg
	}

	// Peel env prefixes and process wrappers.
	argv, dyn = w.peel(&seg, argv, dyn)

	seg.Argv, seg.DynArg = argv, dyn
	if len(argv) == 0 {
		seg.Opaque = true
		seg.Effect = EffectUnknown
		seg.risk("empty-after-wrappers")
		return seg
	}
	if dyn[0] {
		seg.Opaque = true
		seg.Effect = EffectUnknown
		seg.risk("dynamic-command")
		return seg
	}
	for i := 1; i < len(dyn); i++ {
		if dyn[i] {
			seg.risk("dynamic-arg")
			break
		}
	}

	seg.Bin = path.Base(argv[0])
	if strings.Contains(argv[0], "/") {
		if trustedDirs[path.Dir(argv[0])] {
			seg.Bin = path.Base(argv[0])
		} else {
			// ./ls, ~/bin/git, /tmp/x/cat: whatever file sits there, not the tool
			// the basename names.
			seg.Bin = argv[0]
			seg.risk("path-command")
		}
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") && a != "-" {
			seg.Flags = append(seg.Flags, a)
		}
	}
	seg.Sub = subcommand(seg.Bin, argv[1:])
	seg.Effect = classify(&seg)
	seg.Effect = MaxEffect(seg.Effect, redirectEffect(seg.Redirects))

	w.unwrapInner(&seg)
	return seg
}

func (w *walker) redirects(seg *Segment, rs []*syntax.Redirect) {
	for _, r := range rs {
		rd := Redirect{Op: r.Op.String()}
		switch {
		case r.Hdoc != nil:
			seg.risk("heredoc")
			seg.Opaque = true
			rd.Dynamic = true
		case r.Word != nil:
			t, d := w.word(r.Word)
			rd.Target, rd.Dynamic = t, d
			if d {
				w.substitutions(r.Word)
			}
		}
		seg.Redirects = append(seg.Redirects, rd)
	}
}

// redirectEffect: an output redirect to anything but /dev/null or an fd dup is
// a write; a dynamic target is a write too (it cannot be shown otherwise).
func redirectEffect(rs []Redirect) Effect {
	e := EffectNone
	for _, r := range rs {
		switch r.Op {
		case ">", ">>", ">|", "&>", "&>>", "<>":
			if !r.Dynamic && r.Target == "/dev/null" {
				continue
			}
			if !r.Dynamic && strings.HasPrefix(r.Target, "/dev/std") {
				continue
			}
			e = MaxEffect(e, EffectWrite)
		case ">&", "<&":
			// fd duplication ("2>&1"); a file target after >& is rare, treat as write.
			if !r.Dynamic && !isFD(r.Target) {
				e = MaxEffect(e, EffectWrite)
			}
		case "<<", "<<-", "<<<":
			// stdin text; no effect by itself
		}
	}
	return e
}

func isFD(s string) bool {
	if s == "-" {
		return true
	}
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// trustedDirs are the system directories whose binaries a path-qualified
// command word may be classified by basename.
var trustedDirs = map[string]bool{"/bin": true, "/usr/bin": true, "/usr/local/bin": true, "/sbin": true, "/usr/sbin": true}

// envSensitive names variables that change how other programs run or load code:
// a prefix like PATH=. or GIT_EXTERNAL_DIFF=... turns an otherwise read-only
// command into something else.
func envSensitive(name string) bool {
	switch name {
	case "PATH", "BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "PS4", "PS0", "PS1", "PROMPT_COMMAND", "IFS", "CDPATH",
		"PAGER", "MANPAGER", "EDITOR", "VISUAL", "LESSOPEN", "LESSCLOSE", "HOME", "XDG_CONFIG_HOME", "TMPDIR",
		"PYTHONPATH", "PYTHONSTARTUP", "NODE_OPTIONS", "PERL5OPT", "PERL5LIB", "RUBYOPT", "RUBYLIB", "GOFLAGS", "GOROOT",
		"SSH_ASKPASS", "SSH_AUTH_SOCK", "GCONV_PATH", "LOCPATH", "NLSPATH", "HOSTALIASES", "TERMINFO", "MAIL", "MAILPATH":
		return true
	}
	for _, p := range []string{"LD_", "DYLD_", "GIT_", "BASH_FUNC_", "BASH_", "LESS", "SSL_", "CURL_", "NPM_CONFIG_", "PIP_", "CARGO_"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// embedsSubstitution reports static text that a later shell step could evaluate
// as code: command substitution syntax inside single quotes, or the @P
// prompt-expansion operator.
func embedsSubstitution(s string) bool {
	return strings.Contains(s, "$(") || strings.Contains(s, "`") || strings.Contains(s, "@P}") || strings.Contains(s, "${")
}
