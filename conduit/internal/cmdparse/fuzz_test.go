package cmdparse

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzParse hunts for inputs that panic or hang the parser, or that break its
// own invariants. Parse is on the path of every command a caller submits, so a
// crash here is a denial of service for the whole daemon. Run it longer with:
//
//	go test -fuzz=FuzzParse -fuzztime=2m ./internal/cmdparse
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"ls", "ls -la && echo hi | wc -l", "bash -c 'sh -c \"ls\"'", "ssh h 'a; b'", "xargs -I{} rm {}",
		"env -iu x y", "timeout 5 nice -n 3 sudo -u x cmd", "echo $(echo $(echo `x`))", "cat <<EOF\nhi\nEOF",
		"f() { ls; }; f", "case $x in a) ;; esac", "x=($(a))", "{ a; } > f", "(( 1+1 ))", "[[ -f x ]]",
		"for i in 1 2; do echo $i; done", "a=1 b=2", "'unterminated", "\"", "$(", "<(", "${", "\x00", "\xff\xfe",
		strings.Repeat("(", 200), strings.Repeat("$(", 100) + strings.Repeat(")", 100), "sed 's/a/b/' f", "awk '{print}' f",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			t.Skip()
		}
		p := Parse(s)
		if p == nil {
			t.Fatal("nil result")
		}
		if p.Internal {
			t.Fatalf("parser panicked on %q: %s", s, p.Err)
		}
		if p.Err != "" && !p.Opaque {
			t.Fatalf("a parse failure must be opaque: %q", s)
		}
		for _, seg := range p.Segments {
			if !utf8.ValidString(seg.Bin) && utf8.ValidString(s) {
				t.Fatalf("invalid UTF-8 produced from valid input %q", s)
			}
			if seg.Effect == "" {
				t.Fatalf("segment without an effect for %q", s)
			}
		}
	})
}

func TestParserPanicFailsClosed(t *testing.T) {
	orig := parseImpl
	t.Cleanup(func() { parseImpl = orig })
	parseImpl = func(string, int, string) ([]Segment, error) {
		panic("synthetic parser bug with password=hunter2hunter2")
	}
	p := Parse("ls")
	if !p.Opaque || !p.Internal || p.Effect != EffectUnknown || len(p.Segments) != 0 {
		t.Fatalf("%+v", p)
	}
	if strings.Contains(p.Err, "hunter2") || !strings.Contains(p.Err, "ref pnc_") {
		t.Errorf("the error must carry a ref, not panic detail: %q", p.Err)
	}
}
