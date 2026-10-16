package runbook

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/conduit/internal/cmdparse"
)

func restart(t *testing.T) *Runbook {
	t.Helper()
	set, err := NewSet(&Runbook{
		Name: "restart-service", Argv: []string{"systemctl", "restart", "{{unit}}"},
		Params: map[string]Param{"unit": {Required: true, Pattern: `[a-z0-9@._-]+\.service`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := set.Get("restart-service")
	return rb
}

func TestRenderQuotesEveryWord(t *testing.T) {
	cmd, err := restart(t).Render(map[string]string{"unit": "nginx.service"})
	if err != nil || cmd != "systemctl restart nginx.service" {
		t.Fatalf("%q %v", cmd, err)
	}
}

// The point of a runbook: no parameter value can add a word, operator or substitution.
func TestInjectionAttemptsAreRejectedOrInert(t *testing.T) {
	rb := restart(t)
	for _, evil := range []string{"x.service; rm -rf ~", "$(reboot).service", "a.service\nrm -rf /", "`id`.service", "x.service && id", "-rf.service "} {
		if _, err := rb.Render(map[string]string{"unit": evil}); !errors.Is(err, ErrParam) {
			t.Errorf("%q should have been rejected", evil)
		}
	}
	// An unconstrained string parameter is quoted, so it stays one inert word.
	set, _ := NewSet(&Runbook{Name: "say", Argv: []string{"echo", "{{msg}}"}, Verdict: "allow", Params: map[string]Param{"msg": {Required: true}}})
	say, _ := set.Get("say")
	cmd, err := say.Render(map[string]string{"msg": "hi; rm -rf ~ $(id) `id` 'q' \"d\""})
	if err != nil {
		t.Fatal(err)
	}
	p := cmdparse.Parse(cmd)
	if len(p.Segments) != 1 || p.Segments[0].Bin != "echo" || p.Opaque || p.Segments[0].Has("dynamic-arg") {
		t.Fatalf("parameter escaped its quoting: %q -> %+v", cmd, p.Segments)
	}
	if len(p.Segments[0].Argv) != 2 {
		t.Errorf("argv: %q", p.Segments[0].Argv)
	}
}

func TestFlagLookingValuesRejected(t *testing.T) {
	set, _ := NewSet(&Runbook{Name: "cat-file", Argv: []string{"cat", "{{f}}"}, Params: map[string]Param{"f": {Required: true}}})
	rb, _ := set.Get("cat-file")
	if _, err := rb.Render(map[string]string{"f": "--help"}); !errors.Is(err, ErrParam) {
		t.Error("-flag value accepted for an unconstrained string")
	}
}

func TestTypesRequiredDefaultsAndUnknown(t *testing.T) {
	set, err := NewSet(&Runbook{
		Name: "tail-log", Argv: []string{"tail", "-n", "{{n}}", "{{file}}"},
		Params: map[string]Param{
			"n":    {Type: "int", Default: "50", Min: 1, Max: 1000},
			"file": {Type: "enum", Enum: []string{"/var/log/syslog", "/var/log/auth.log"}, Required: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := set.Get("tail-log")
	cmd, err := rb.Render(map[string]string{"file": "/var/log/syslog"})
	if err != nil || cmd != "tail -n 50 /var/log/syslog" {
		t.Fatalf("%q %v", cmd, err)
	}
	for name, params := range map[string]map[string]string{
		"missing required": {},
		"bad int":          {"file": "/var/log/syslog", "n": "x"},
		"int range":        {"file": "/var/log/syslog", "n": "5000"},
		"bad enum":         {"file": "/etc/shadow"},
		"unknown param":    {"file": "/var/log/syslog", "extra": "1"},
	} {
		if _, err := rb.Render(params); !errors.Is(err, ErrParam) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDefinitionErrors(t *testing.T) {
	bad := map[string]*Runbook{
		"param as command": {Name: "a", Argv: []string{"{{c}}"}, Params: map[string]Param{"c": {}}},
		"undeclared":       {Name: "b", Argv: []string{"echo", "{{x}}"}},
		"unused":           {Name: "c", Argv: []string{"echo"}, Params: map[string]Param{"x": {}}},
		"bad name":         {Name: "Bad Name", Argv: []string{"echo"}},
		"empty argv":       {Name: "d"},
		"bad verdict":      {Name: "e", Argv: []string{"echo"}, Verdict: "deny"},
		"bad sandbox":      {Name: "f", Argv: []string{"echo"}, Sandbox: "fortress"},
		"bad regex":        {Name: "g", Argv: []string{"echo", "{{x}}"}, Params: map[string]Param{"x": {Pattern: "("}}},
		"half placeholder": {Name: "h", Argv: []string{"echo", "{{x"}},
	}
	for name, rb := range bad {
		if _, err := NewSet(rb); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	if s, err := Load(filepath.Join(dir, "none.yaml")); err != nil || len(s.List()) != 0 {
		t.Fatalf("missing file: %v", err)
	}
	f := filepath.Join(dir, "rb.yaml")
	_ = os.WriteFile(f, []byte(`runbooks:
  - name: disk-usage
    description: how full are the disks
    argv: [df, -h]
    verdict: allow
  - name: restart-service
    argv: [systemctl, restart, "{{unit}}"]
    params:
      unit: {required: true, pattern: '[a-z0-9@._-]+\.service'}
    sandbox: protect
    timeout: 30s
`), 0o600)
	s, err := Load(f)
	if err != nil || len(s.List()) != 2 {
		t.Fatalf("%v %v", s, err)
	}
	rb, _ := s.Get("restart-service")
	if rb.Verdict != "ask" || rb.Timeout.Seconds() != 30 || rb.Sandbox != "protect" {
		t.Errorf("%+v", rb)
	}
	_ = os.WriteFile(f, []byte("runbooks:\n  - {name: x, argv: [echo], verdit: allow}\n"), 0o600)
	if _, err := Load(f); err == nil || !strings.Contains(err.Error(), "verdit") {
		t.Errorf("unknown field not rejected: %v", err)
	}
}
