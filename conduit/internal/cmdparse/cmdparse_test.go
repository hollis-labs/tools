package cmdparse

import (
	"strings"
	"testing"
)

func bins(p *Parsed) string {
	var parts []string
	for _, s := range p.Segments {
		parts = append(parts, s.Op+s.Bin)
	}
	return strings.Join(parts, " ")
}

func TestSegmentsAndOperators(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"ls", "ls"},
		{"ls -la && echo hi", "ls &&echo"},
		{"false || echo no; date", "false ||echo ;date"},
		{"cat f | grep x | wc -l", "cat |grep |wc"},
		{"a=1 b=2", ""}, // pure assignment keeps a segment with no bin
		{"git -C /p log --oneline", "git"},
		{"ls\npwd", "ls ;pwd"},
	}
	for _, c := range cases {
		p := Parse(c.in)
		if p.Err != "" {
			t.Fatalf("%q: %s", c.in, p.Err)
		}
		got := bins(p)
		if c.in == "a=1 b=2" {
			if len(p.Segments) != 1 || len(p.Segments[0].Env) != 2 || p.Effect != EffectNone {
				t.Errorf("assignment: %+v", p.Segments)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestGlobalFlagSkipAndSubcommand(t *testing.T) {
	p := Parse("git -C /p --no-pager log --oneline")
	s := p.Segments[0]
	if s.Bin != "git" || s.Sub != "log" || s.Effect != EffectRead {
		t.Fatalf("%+v", s)
	}
	// -c makes git run configured programs: not a read
	p = Parse("git -C /p -c core.fsmonitor=x log")
	if s := p.Segments[0]; s.Sub != "log" || s.Effect != EffectExec || !s.Has("flag-exec") {
		t.Fatalf("%+v", s)
	}
}

func TestWrappersPeeled(t *testing.T) {
	cases := []struct {
		in       string
		bin      string
		wrappers []string
		sudo     bool
		effect   Effect
	}{
		{"timeout 300 go test ./...", "go", []string{"timeout"}, false, EffectExec},
		{"env FOO=1 BAR=2 ls", "ls", []string{"env"}, false, EffectRead},
		{"nice -n 5 rm -rf build", "rm", []string{"nice"}, false, EffectDestructive},
		{"sudo -u bob systemctl restart x", "systemctl", []string{"sudo"}, true, EffectWrite},
		{"nohup timeout -k 5 10 sleep 1", "sleep", []string{"nohup", "timeout"}, false, EffectRead},
		{"FOO=1 ls", "ls", nil, false, EffectRead},
	}
	for _, c := range cases {
		p := Parse(c.in)
		s := p.Segments[0]
		if s.Bin != c.bin || strings.Join(s.Wrappers, ",") != strings.Join(c.wrappers, ",") || s.Has("sudo") != c.sudo || s.Effect != c.effect {
			t.Errorf("%q: bin=%q wrappers=%v sudo=%v effect=%v opaque=%v", c.in, s.Bin, s.Wrappers, s.Has("sudo"), s.Effect, s.Opaque)
		}
	}
}

func TestShellCUnwrapsAndSeesInner(t *testing.T) {
	p := Parse(`bash -c 'ls && rm -rf /tmp/x'`)
	if p.Effect != EffectDestructive {
		t.Fatalf("effect %v; segments %+v", p.Effect, p.Segments)
	}
	var inner []string
	for _, s := range p.Segments {
		if s.Depth == 1 {
			inner = append(inner, s.Bin)
		}
	}
	if strings.Join(inner, ",") != "ls,rm" {
		t.Errorf("inner %v", inner)
	}
}

func TestSSHAndXargsUnwrap(t *testing.T) {
	p := Parse(`ssh -p 2222 worker-1 'systemctl status foo | head'`)
	if p.Effect != EffectNetwork {
		t.Errorf("ssh effect %v", p.Effect)
	}
	found := false
	for _, s := range p.Segments {
		if s.Via == "ssh" && s.Bin == "systemctl" {
			found = true
		}
	}
	if !found {
		t.Errorf("no inner systemctl: %+v", p.Segments)
	}

	p = Parse("find . -name '*.tmp' | xargs rm -f")
	if p.Effect != EffectDestructive {
		t.Errorf("xargs rm effect %v: %+v", p.Effect, p.Segments)
	}
}

func TestDepthCap(t *testing.T) {
	p := Parse(`sh -c "sh -c \"sh -c \\\"sh -c 'ls'\\\"\""`)
	if !p.Opaque {
		t.Errorf("expected opaque past depth cap: %+v", p.Segments)
	}
}

func TestOpaqueConstructs(t *testing.T) {
	for _, in := range []string{
		"$CMD arg",
		"cat <<EOF\nhi\nEOF",
		"f() { ls; }; f",
		"echo $(",
		"ls &&",
		`"$(which rm)" -rf x`,
	} {
		p := Parse(in)
		if !p.Opaque {
			t.Errorf("%q should be opaque: %+v", in, p.Segments)
		}
	}
}

func TestSubstitutionIsClassified(t *testing.T) {
	p := Parse("echo $(rm -rf x)")
	if p.Effect != EffectDestructive {
		t.Errorf("inner rm in $() not seen: %v %+v", p.Effect, p.Segments)
	}
}

func TestRedirectsRaiseEffect(t *testing.T) {
	cases := map[string]Effect{
		"echo hi > out.txt":   EffectWrite,
		"echo hi >> out.txt":  EffectWrite,
		"ls 2>&1":             EffectRead,
		"ls > /dev/null 2>&1": EffectRead,
		"cat < in.txt":        EffectRead,
		"echo hi > $OUT":      EffectWrite,
	}
	for in, want := range cases {
		if got := Parse(in).Effect; got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestFlagsRaiseEffect(t *testing.T) {
	cases := map[string]Effect{
		"git push":                  EffectNetwork,
		"git push --force origin x": EffectDestructive,
		"git push -f":               EffectDestructive,
		"git reset --hard HEAD~1":   EffectDestructive,
		"git status":                EffectRead,
		"git -C x commit -m y":      EffectWrite,
		"find . -name x":            EffectRead,
		"find . -delete":            EffectDestructive,
		"find . -exec ls {} ;":      EffectExec,
		"sed -i s/a/b/ f":           EffectWrite,
		"sed s/a/b/ f":              EffectRead,
		"rm -rf x":                  EffectDestructive,
		"mytool --flag":             EffectUnknown,
		"kubectl get pods":          EffectRead,
		"kubectl delete pod x":      EffectDestructive,
	}
	for in, want := range cases {
		if got := Parse(in).Effect; got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

// The display view: every segment's Raw is a verbatim slice of the input.
func TestRawIsVerbatim(t *testing.T) {
	in := "ls   -la  &&   echo 'a  b' | wc -l"
	p := Parse(in)
	for _, s := range p.Segments {
		if !strings.Contains(in, s.Raw) || s.Raw == "" {
			t.Errorf("segment raw %q not verbatim in %q", s.Raw, in)
		}
	}
}

// Wrapping a command must never make it look less severe.
func TestWrappingNeverLessSevere(t *testing.T) {
	base := []string{"ls", "rm -rf x", "git push --force", "curl http://x", "mytool", "echo hi > f", "go test ./..."}
	wrap := []string{"timeout 5 %s", "env A=1 %s", "nice %s", "nohup %s", "sudo %s", "bash -c '%s'", "xargs %s"}
	for _, b := range base {
		bp := Parse(b)
		for _, w := range wrap {
			wp := Parse(strings.Replace(w, "%s", b, 1))
			if wp.Effect.Rank() < bp.Effect.Rank() && !wp.Opaque {
				t.Errorf("%q (%v) less severe than %q (%v)", strings.Replace(w, "%s", b, 1), wp.Effect, b, bp.Effect)
			}
		}
	}
}
