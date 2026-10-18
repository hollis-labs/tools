// Package ref parses and formats caster references: typed, canonical URIs that
// name a thing in some system. A ref is identity, not content and not permission.
//
//	cas://sha256/<hex>              immutable blob (pinned)
//	caster://<ns>/<name>[@<rev>]    named scratchpad/draft/note (floating, or pinned with @rev)
//	torque://task/<CW-id>           a Torque task
//	tesseract://<ns>/<key>[@<rev>]  a Tesseract context record
//	tesseract://item/<id>           a current Tesseract item (floating); revision/<id> is an exact revision (pinned)
//	tesseract://<domain>/<ns>/<key> memory|knowledge|event|workspace|context by namespace and key
package ref

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	SchemeCAS       = "cas"
	SchemeCaster    = "caster"
	SchemeTorque    = "torque"
	SchemeTesseract = "tesseract"
)

// Ref is a parsed reference. Path holds the segments after the authority.
type Ref struct {
	Scheme string
	Auth   string // first segment: "sha256", a namespace, "task", "item"
	Path   string // rest, joined with "/"
	Rev    int    // 0 = floating
}

var (
	hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	taskRe = regexp.MustCompile(`^CW-\d{8}-\d{4}$`)
	segRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Parse accepts a full URI, or a bare shorthand: a 64-hex hash is cas://,
// a Torque task id is torque://task/, and "ns/name" is caster://.
func Parse(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("empty ref")
	}
	if !strings.Contains(s, "://") {
		switch {
		case hashRe.MatchString(s):
			s = "cas://sha256/" + s
		case taskRe.MatchString(s):
			s = "torque://task/" + s
		default:
			s = "caster://" + s
		}
	}
	scheme, rest, _ := strings.Cut(s, "://")
	scheme = strings.ToLower(scheme)
	rev := 0
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		n, err := strconv.Atoi(rest[i+1:])
		if err != nil || n < 1 {
			return Ref{}, fmt.Errorf("bad revision in %q", s)
		}
		rev, rest = n, rest[:i]
	}
	auth, path, _ := strings.Cut(strings.Trim(rest, "/"), "/")
	r := Ref{Scheme: scheme, Auth: auth, Path: path, Rev: rev}
	return r, r.validate()
}

func (r Ref) validate() error {
	switch r.Scheme {
	case SchemeCAS:
		if r.Auth != "sha256" || !hashRe.MatchString(r.Path) || r.Rev != 0 {
			return fmt.Errorf("cas ref must be cas://sha256/<64 hex>")
		}
	case SchemeCaster:
		if r.Auth == "" || r.Path == "" {
			return fmt.Errorf("caster ref must be caster://<ns>/<name>")
		}
		for _, seg := range strings.Split(r.Auth+"/"+r.Path, "/") {
			if !segRe.MatchString(seg) {
				return fmt.Errorf("invalid segment %q (plain names only)", seg)
			}
		}
	case SchemeTorque:
		if r.Auth != "task" || !taskRe.MatchString(r.Path) {
			return fmt.Errorf("torque ref must be torque://task/CW-YYYYMMDD-NNNN")
		}
	case SchemeTesseract:
		if r.Auth == "" || r.Path == "" {
			return fmt.Errorf("tesseract ref must be tesseract://<ns>/<key> or tesseract://item/<id>")
		}
	default:
		return fmt.Errorf("unsupported scheme %q", r.Scheme)
	}
	return nil
}

// String is the canonical URI form.
func (r Ref) String() string {
	s := r.Scheme + "://" + r.Auth + "/" + r.Path
	if r.Rev > 0 {
		s += "@" + strconv.Itoa(r.Rev)
	}
	return s
}

// Name returns "<auth>/<path>" for caster refs.
func (r Ref) Name() string { return r.Auth + "/" + r.Path }

// Floating reports whether the ref follows the current revision.
func (r Ref) Floating() bool {
	if r.Scheme == SchemeCAS || (r.Scheme == SchemeTesseract && r.Auth == "revision") {
		return false
	}
	return r.Rev == 0
}
