// Package policy decides allow, ask or deny for a parsed command.
//
// One decision function, one order: Deny > Ask > Allow, default Ask. Every
// segment of the command (including those unwrapped from sh -c, ssh, xargs and
// substitutions) is decided on its own and the most restrictive verdict wins.
// Anything the parser could not attest is never auto-allowed. Rules match
// parsed commands (binary and subcommand), never substrings of the raw text.
package policy

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/tools/conduit/internal/cmdparse"
	"github.com/hollis-labs/tools/conduit/internal/cmdrun"
)

// Verdict is a policy outcome.
type Verdict string

const (
	Allow Verdict = "allow"
	Ask   Verdict = "ask"
	Deny  Verdict = "deny"
)

var rank = map[Verdict]int{Allow: 0, Ask: 1, Deny: 2}

// Stricter returns the more restrictive of a and b.
func Stricter(a, b Verdict) Verdict {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Rule matches segments by parsed command. An empty field matches anything.
type Rule struct {
	ID      string   `yaml:"id" json:"id"`
	Bin     string   `yaml:"bin" json:"bin,omitempty"`
	Sub     string   `yaml:"sub" json:"sub,omitempty"`
	Effect  string   `yaml:"effect" json:"effect,omitempty"` // match segments of this effect class
	Flags   []string `yaml:"flags" json:"flags,omitempty"`   // all must be present
	Verdict Verdict  `yaml:"verdict" json:"verdict"`
	Reason  string   `yaml:"reason" json:"reason,omitempty"`
	// Sandbox is an obligation: segments matching this rule run at least this
	// confined (see cmdrun.Sandbox*), whatever the verdict.
	Sandbox string `yaml:"sandbox" json:"sandbox,omitempty"`
}

func (r Rule) matches(s *cmdparse.Segment) bool {
	if r.Bin != "" && r.Bin != s.Bin {
		return false
	}
	if r.Sub != "" && r.Sub != s.Sub {
		return false
	}
	if r.Effect != "" && cmdparse.Effect(r.Effect) != s.Effect {
		return false
	}
	for _, f := range r.Flags {
		found := false
		for _, sf := range s.Flags {
			if sf == f {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return r.Bin != "" || r.Effect != ""
}

// Decision is the outcome for a whole command.
type Decision struct {
	Verdict Verdict         `json:"verdict"`
	Reasons []Reason        `json:"reasons"`
	Effect  cmdparse.Effect `json:"effect"`
	Sandbox string          `json:"sandbox,omitempty"` // strongest sandbox obligation of any matching rule
}

// Reason ties a verdict to the segment and rule that produced it.
type Reason struct {
	Segment string  `json:"segment"` // raw text of the segment
	Verdict Verdict `json:"verdict"`
	Rule    string  `json:"rule"`
	Why     string  `json:"why"`
}

// Policy is a rule set plus the built-in baseline.
type Policy struct {
	Rules []Rule
}

// Baseline is the built-in policy: read is allowed, everything else asks, a
// short list of hard denies never runs. It is the floor user rules sit on.
func Baseline() *Policy { return &Policy{} }

// Decide evaluates a parsed command.
func (p *Policy) Decide(c *cmdparse.Parsed) Decision {
	d := Decision{Verdict: Allow, Effect: c.Effect}
	add := func(seg, rule, why string, v Verdict) {
		d.Reasons = append(d.Reasons, Reason{Segment: seg, Verdict: v, Rule: rule, Why: why})
		d.Verdict = Stricter(d.Verdict, v)
	}

	if c.Err != "" {
		add(c.Raw, "parse-error", "command did not parse: "+c.Err, Ask)
		return d
	}
	if len(c.Segments) == 0 {
		return d
	}

	for i := range c.Segments {
		s := &c.Segments[i]
		for _, r := range p.Rules {
			if r.Sandbox != "" && r.matches(s) {
				d.Sandbox = cmdrun.Stricter(d.Sandbox, r.Sandbox)
			}
		}
		v, rule, why := p.segment(c, i, s)
		if v != Allow || rule != "" {
			add(s.Raw, rule, why, v)
		}
	}
	if len(d.Reasons) == 0 {
		d.Reasons = append(d.Reasons, Reason{Verdict: Allow, Rule: "baseline-read", Why: "read-only commands are allowed"})
	}
	return d
}

// segment decides one segment. Deny rules look through wrappers (they match
// the peeled command); allow rules apply only to a segment that has no
// wrapper, risk or opacity, so `timeout 5 X` never inherits an allow for X.
func (p *Policy) segment(c *cmdparse.Parsed, i int, s *cmdparse.Segment) (Verdict, string, string) {
	if v, rule, why, hit := builtinDeny(c, i, s); hit {
		return v, rule, why
	}
	for _, r := range p.Rules {
		if r.Verdict == Deny && r.matches(s) {
			return Deny, r.ID, orDefault(r.Reason, "denied by rule")
		}
	}
	if s.Opaque {
		return Ask, "opaque", "unattested construct: " + strings.Join(s.Risks, ", ")
	}
	for _, r := range p.Rules {
		if r.Verdict == Ask && r.matches(s) {
			return Ask, r.ID, orDefault(r.Reason, "ask by rule")
		}
	}
	if s.Has("sudo") {
		return Ask, "sudo", "elevated privileges"
	}
	// Risks that change what a command is, however read-only it looks: never
	// auto-allowed, and no allow rule below can cover them.
	for _, risk := range []string{"env-sensitive", "embedded-substitution", "path-command", "flag-exec"} {
		if s.Has(risk) {
			return Ask, risk, riskWhy[risk]
		}
	}
	for _, r := range p.Rules {
		if r.Verdict == Allow && r.matches(s) && len(s.Wrappers) == 0 && len(s.Risks) == 0 {
			return Allow, r.ID, orDefault(r.Reason, "allowed by rule")
		}
	}
	switch s.Effect {
	case cmdparse.EffectNone, cmdparse.EffectRead:
		if s.Has("dynamic-arg") && s.Effect == cmdparse.EffectRead && !cmdparse.FlagSafeBin(s.Bin) {
			// the argument is only known at run time and may be a flag (find . $X)
			return Ask, "dynamic-arg", "a runtime-expanded argument could be an option for " + s.Bin
		}
		return Allow, "", ""
	case cmdparse.EffectUnknown:
		return Ask, "unknown-binary", fmt.Sprintf("%q is not in the effect table", s.Bin)
	default:
		return Ask, "effect-" + string(s.Effect), string(s.Effect) + " effect needs approval"
	}
}

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}

// Load reads a rules file. A missing file is the baseline; a malformed one is
// an error, never a silently empty policy (fail closed).
func Load(path string) (*Policy, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the policy path comes from the operator's config or env
	if os.IsNotExist(err) {
		return Baseline(), nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Rules []Rule `yaml:"rules"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("policy %s: %w", path, err)
	}
	for i, r := range f.Rules {
		switch r.Verdict {
		case Allow, Ask, Deny:
		default:
			return nil, fmt.Errorf("policy %s: rule %d (%s): verdict %q must be allow, ask or deny", path, i, r.ID, r.Verdict)
		}
		if r.Sandbox != "" && !cmdrun.ValidSandbox(r.Sandbox) {
			return nil, fmt.Errorf("policy %s: rule %d (%s): unknown sandbox %q", path, i, r.ID, r.Sandbox)
		}
		if r.Bin == "" && r.Effect == "" {
			return nil, fmt.Errorf("policy %s: rule %d (%s): needs bin or effect", path, i, r.ID)
		}
	}
	return &Policy{Rules: f.Rules}, nil
}

var riskWhy = map[string]string{
	"env-sensitive":         "sets an environment variable that changes how programs run or load code",
	"embedded-substitution": "text that a later shell step could evaluate as a command",
	"path-command":          "runs a program by path, not the system tool its name suggests",
	"flag-exec":             "an option makes this command run another program",
}
