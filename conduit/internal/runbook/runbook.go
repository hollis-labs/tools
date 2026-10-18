// Package runbook defines named operations with a fixed argv template and typed
// parameters. A runbook is the reviewed unit of trust: its argv is fixed, its
// parameters are validated, and every argument is quoted individually, so a
// parameter can never add a word, an operator or a substitution to the command.
package runbook

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"mvdan.cc/sh/v3/syntax"

	"github.com/hollis-labs/conduit/internal/cmdrun"
)

// Param describes one parameter.
type Param struct {
	Type     string   `yaml:"type" json:"type"` // string (default), int, bool, enum
	Required bool     `yaml:"required" json:"required,omitempty"`
	Default  string   `yaml:"default" json:"default,omitempty"`
	Enum     []string `yaml:"enum" json:"enum,omitempty"`
	Pattern  string   `yaml:"pattern" json:"pattern,omitempty"` // must match the whole value
	Max      int      `yaml:"max" json:"max,omitempty"`         // max length (string) or max value (int)
	Min      int      `yaml:"min" json:"min,omitempty"`
	Desc     string   `yaml:"description" json:"description,omitempty"`

	re *regexp.Regexp
}

// Runbook is a named, parameterised command.
type Runbook struct {
	Name        string           `yaml:"name" json:"name"`
	Description string           `yaml:"description" json:"description"`
	Argv        []string         `yaml:"argv" json:"argv"`
	Params      map[string]Param `yaml:"params" json:"params,omitempty"`
	Verdict     string           `yaml:"verdict" json:"verdict"` // allow or ask; deny always wins from policy
	Sandbox     string           `yaml:"sandbox" json:"sandbox,omitempty"`
	Timeout     time.Duration    `yaml:"timeout" json:"timeout,omitempty"`
	Dir         string           `yaml:"dir" json:"dir,omitempty"`
	Retries     int              `yaml:"retries" json:"retries,omitempty"`
}

var (
	nameRe  = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)
	placeRe = regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)
)

// Set is a loaded runbook file.
type Set struct {
	byName map[string]*Runbook
}

// Load reads a runbook file. A missing file is an empty set; a malformed or
// inconsistent one is an error naming the runbook.
func Load(path string) (*Set, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path comes from the operator's config dir or env
	if os.IsNotExist(err) {
		return &Set{byName: map[string]*Runbook{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Runbooks []*Runbook `yaml:"runbooks"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("runbooks %s: %w", path, err)
	}
	set := &Set{byName: map[string]*Runbook{}}
	for _, rb := range f.Runbooks {
		if err := rb.validate(); err != nil {
			return nil, fmt.Errorf("runbooks %s: %w", path, err)
		}
		if _, dup := set.byName[rb.Name]; dup {
			return nil, fmt.Errorf("runbooks %s: duplicate runbook %q", path, rb.Name)
		}
		set.byName[rb.Name] = rb
	}
	return set, nil
}

// NewSet builds a set from runbooks, validating each.
func NewSet(rbs ...*Runbook) (*Set, error) {
	set := &Set{byName: map[string]*Runbook{}}
	for _, rb := range rbs {
		if err := rb.validate(); err != nil {
			return nil, err
		}
		set.byName[rb.Name] = rb
	}
	return set, nil
}

func (rb *Runbook) validate() error {
	if !nameRe.MatchString(rb.Name) {
		return fmt.Errorf("runbook name %q must match %s", rb.Name, nameRe)
	}
	if len(rb.Argv) == 0 {
		return fmt.Errorf("runbook %s: argv is empty", rb.Name)
	}
	if placeRe.MatchString(rb.Argv[0]) {
		return fmt.Errorf("runbook %s: the command word (argv[0]) must be fixed, not a parameter", rb.Name)
	}
	switch rb.Verdict {
	case "":
		rb.Verdict = "ask"
	case "allow", "ask":
	default:
		return fmt.Errorf("runbook %s: verdict %q must be allow or ask", rb.Name, rb.Verdict)
	}
	if rb.Sandbox != "" && !cmdrun.ValidSandbox(rb.Sandbox) {
		return fmt.Errorf("runbook %s: unknown sandbox %q", rb.Name, rb.Sandbox)
	}
	used := map[string]bool{}
	for _, a := range rb.Argv {
		if strings.Contains(a, "{{") && !placeRe.MatchString(a) {
			return fmt.Errorf("runbook %s: malformed placeholder in %q", rb.Name, a)
		}
		for _, m := range placeRe.FindAllStringSubmatch(a, -1) {
			if _, ok := rb.Params[m[1]]; !ok {
				return fmt.Errorf("runbook %s: argv uses undeclared parameter %q", rb.Name, m[1])
			}
			used[m[1]] = true
		}
	}
	for name, p := range rb.Params {
		if !used[name] {
			return fmt.Errorf("runbook %s: parameter %q is declared but never used", rb.Name, name)
		}
		switch p.Type {
		case "", "string", "int", "bool":
		case "enum":
			if len(p.Enum) == 0 {
				return fmt.Errorf("runbook %s: enum parameter %q has no values", rb.Name, name)
			}
		default:
			return fmt.Errorf("runbook %s: parameter %q has unknown type %q", rb.Name, name, p.Type)
		}
		if p.Pattern != "" {
			re, err := regexp.Compile(`^(?:` + p.Pattern + `)$`)
			if err != nil {
				return fmt.Errorf("runbook %s: parameter %q pattern: %w", rb.Name, name, err)
			}
			p.re = re
			rb.Params[name] = p
		}
		if p.Type == "" || p.Type == "string" {
			if p.Pattern == "" && len(p.Enum) == 0 {
				// An unconstrained string is allowed, but must be bounded.
				if p.Max == 0 {
					p.Max = 256
					rb.Params[name] = p
				}
			}
		}
	}
	return nil
}

// Get returns a runbook by name.
func (s *Set) Get(name string) (*Runbook, bool) { rb, ok := s.byName[name]; return rb, ok }

// List returns runbooks sorted by name.
func (s *Set) List() []*Runbook {
	out := make([]*Runbook, 0, len(s.byName))
	for _, rb := range s.byName {
		out = append(out, rb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ErrParam is returned for an invalid or unknown parameter.
var ErrParam = errors.New("invalid runbook parameter")

// Render validates params and returns the command string. Every argv word is
// quoted for the shell individually.
func (rb *Runbook) Render(params map[string]string) (string, error) {
	for k := range params {
		if _, ok := rb.Params[k]; !ok {
			return "", fmt.Errorf("%w: %q is not a parameter of %s", ErrParam, k, rb.Name)
		}
	}
	vals := map[string]string{}
	for name, p := range rb.Params {
		v, given := params[name]
		if !given {
			if p.Default != "" {
				v = p.Default
			} else if p.Required {
				return "", fmt.Errorf("%w: %q is required", ErrParam, name)
			} else {
				v = ""
			}
		}
		if err := p.check(name, v); err != nil {
			return "", err
		}
		vals[name] = v
	}
	words := make([]string, len(rb.Argv))
	for i, a := range rb.Argv {
		w := placeRe.ReplaceAllStringFunc(a, func(m string) string { return vals[placeRe.FindStringSubmatch(m)[1]] })
		q, err := syntax.Quote(w, syntax.LangBash)
		if err != nil {
			return "", fmt.Errorf("%w: cannot quote %q: %w", ErrParam, w, err)
		}
		words[i] = q
	}
	return strings.Join(words, " "), nil
}

func (p Param) check(name, v string) error {
	if strings.ContainsRune(v, 0) || strings.ContainsAny(v, "\n\r") {
		return fmt.Errorf("%w: %q contains a control character", ErrParam, name)
	}
	if v == "" && !p.Required && p.Default == "" {
		return nil // optional and unset
	}
	switch p.Type {
	case "int":
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%w: %q must be an integer", ErrParam, name)
		}
		if p.Max != 0 && n > p.Max || n < p.Min {
			return fmt.Errorf("%w: %q out of range", ErrParam, name)
		}
	case "bool":
		if v != "true" && v != "false" {
			return fmt.Errorf("%w: %q must be true or false", ErrParam, name)
		}
	case "enum":
		if !contains(p.Enum, v) {
			return fmt.Errorf("%w: %q must be one of %s", ErrParam, name, strings.Join(p.Enum, ", "))
		}
	default:
		if p.Max != 0 && len(v) > p.Max {
			return fmt.Errorf("%w: %q longer than %d", ErrParam, name, p.Max)
		}
		if len(p.Enum) > 0 && !contains(p.Enum, v) {
			return fmt.Errorf("%w: %q must be one of %s", ErrParam, name, strings.Join(p.Enum, ", "))
		}
	}
	if p.re != nil && !p.re.MatchString(v) {
		return fmt.Errorf("%w: %q does not match the required pattern", ErrParam, name)
	}
	if strings.HasPrefix(v, "-") && p.Type != "int" && p.Pattern == "" && len(p.Enum) == 0 {
		return fmt.Errorf("%w: %q must not start with '-' (it would read as a flag)", ErrParam, name)
	}
	return nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
