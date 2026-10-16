package safe

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturing struct {
	mu   sync.Mutex
	recs []Record
}

func (c *capturing) Report(r Record) { c.mu.Lock(); c.recs = append(c.recs, r); c.mu.Unlock() }
func (c *capturing) all() []Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Record(nil), c.recs...)
}

func install(t *testing.T) *capturing {
	t.Helper()
	c := &capturing{}
	SetReporter(c)
	t.Cleanup(func() { SetReporter(reporterFunc(func(Record) {})) })
	return c
}

func TestCallTurnsPanicIntoErrorWithRefOnly(t *testing.T) {
	c := install(t)
	err := Call("test boundary", func() error { panic("password=hunter2hunter2 in the command text") })
	ref, ok := Ref(err)
	if !ok || !strings.HasPrefix(ref, "pnc_") {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "command text") {
		t.Errorf("the error shown to callers carries panic detail: %q", err.Error())
	}
	recs := c.all()
	if len(recs) != 1 || recs[0].Ref != ref || recs[0].Where != "test boundary" {
		t.Fatalf("%+v", recs)
	}
	if strings.Contains(recs[0].Value, "hunter2") || !strings.Contains(recs[0].Value, "REDACTED") {
		t.Errorf("stored value must be redacted: %q", recs[0].Value)
	}
	if !strings.Contains(recs[0].Stack, "safe_test.go") && !strings.Contains(recs[0].Stack, "safe.") {
		t.Errorf("stack missing: %q", recs[0].Stack)
	}
}

func TestCallPassesThroughOrdinaryErrorsAndResults(t *testing.T) {
	install(t)
	want := errors.New("plain")
	if err := Call("x", func() error { return want }); !errors.Is(err, want) {
		t.Error(err)
	}
	if err := Call("x", func() error { return nil }); err != nil {
		t.Error(err)
	}
}

type evil struct{}

func (evil) Error() string  { panic("Error() itself panics") }
func (evil) String() string { panic("String() itself panics") }

func TestHostilePanicValuesAreContained(t *testing.T) {
	c := install(t)
	cyc := &cyclic{}
	cyc.next = cyc
	for name, v := range map[string]any{
		"error whose Error panics": error(evil{}),
		"stringer that panics":     evil2{},
		"cyclic wrapped error":     cyc,
		"nil":                      nil,
		"nil pointer deref":        func() any { var p *int; defer func() { _ = recover() }(); return p }(),
		"huge string":              strings.Repeat("x", 1<<20),
		"control and bidi":         "line\x1b[2J\r\u202e\x00end",
		"non-utf8":                 "\xff\xfe\xfd",
		"arbitrary struct":         struct{ A, B int }{1, 2},
	} {
		err := Call(name, func() error { panic(v) })
		if _, ok := Ref(err); !ok {
			t.Errorf("%s: not contained: %v", name, err)
		}
	}
	for _, r := range c.all() {
		if len([]rune(r.Value)) > maxValueRunes+1 || len([]rune(r.Stack)) > maxStackRunes+1 {
			t.Errorf("%s: record not bounded (%d, %d)", r.Where, len(r.Value), len(r.Stack))
		}
		for _, ch := range r.Value {
			if ch != '\n' && ch != '\t' && (ch < 0x20 || ch == 0x7f || (ch >= 0x200b && ch <= 0x200f) || (ch >= 0x202a && ch <= 0x202e)) {
				t.Errorf("%s: control/format character %U survived in %q", r.Where, ch, r.Value)
			}
		}
	}
}

type evil2 struct{}

func (evil2) String() string { panic("boom") }

type cyclic struct{ next error }

func (c *cyclic) Error() string { return "cyc" }
func (c *cyclic) Unwrap() error { return c.next }

func TestFailingReporterDoesNotCascade(t *testing.T) {
	SetReporter(reporterFunc(func(Record) { panic("reporter is broken") }))
	t.Cleanup(func() { SetReporter(reporterFunc(func(Record) {})) })
	if err := Call("x", func() error { panic("first") }); err == nil {
		t.Fatal("expected a PanicError")
	} else if _, ok := Ref(err); !ok {
		t.Fatalf("%v", err)
	}
}

func TestGoKeepsTheProcessAlive(t *testing.T) {
	c := install(t)
	got := make(chan *PanicError, 1)
	Go("worker", func() { panic("in a goroutine") }, func(pe *PanicError) { got <- pe })
	select {
	case pe := <-got:
		if pe.Rec.Where != "worker" {
			t.Errorf("%+v", pe.Rec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onPanic not called")
	}
	// even a panicking onPanic must not escape the goroutine
	done := make(chan struct{})
	Go("worker2", func() { panic("again") }, func(*PanicError) { defer close(done); panic("handler too") })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler not called")
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(c.all()); n != 2 {
		t.Errorf("want 2 records, got %d", n)
	}
}
