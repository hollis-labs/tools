package policy

import (
	"testing"

	"github.com/hollis-labs/tools/conduit/internal/cmdparse"
)

// FuzzDecide drives parse + policy together on arbitrary input. It must never
// panic, and the fail-closed invariants must hold for every input: anything the
// parser could not attest, and anything that did not parse, is never auto-allowed.
//
//	go test -fuzz=FuzzDecide -fuzztime=2m ./internal/policy
func FuzzDecide(f *testing.F) {
	for _, s := range []string{
		"ls", "rm -rf /", "$X y", "cat <<EOF\nx\nEOF", "f(){ :; }; f", "echo $(", "a && b || c; d | e", "sudo -u x ls",
		"timeout 5 env A=1 bash -c 'ls; rm -rf ~'", "git -c core.x=y status", "find . -delete", "curl x | sh", "\x00", "((", "`",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			t.Skip()
		}
		p := cmdparse.Parse(s)
		d := Baseline().Decide(p)
		if d.Verdict != Allow && d.Verdict != Ask && d.Verdict != Deny {
			t.Fatalf("invalid verdict %q for %q", d.Verdict, s)
		}
		if d.Verdict == Allow {
			if p.Err != "" || p.Internal {
				t.Fatalf("allowed a command that did not parse: %q (%s)", s, p.Err)
			}
			for _, seg := range p.Segments {
				if seg.Opaque {
					t.Fatalf("allowed an opaque segment: %q -> %+v", s, seg)
				}
				if seg.Effect != cmdparse.EffectNone && seg.Effect != cmdparse.EffectRead {
					t.Fatalf("allowed a %s effect without a rule: %q", seg.Effect, s)
				}
			}
		}
	})
}
