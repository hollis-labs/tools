package cli

import (
	"bytes"
	"strings"
	"testing"
	"unicode"

	"github.com/hollis-labs/conduit/internal/cmdparse"
	"github.com/hollis-labs/conduit/internal/policy"
	"github.com/hollis-labs/conduit/internal/service"
	"github.com/hollis-labs/conduit/internal/store"
)

// hostile is text a submitter controls: it tries to clear the screen, set the
// window title, return to column 0 to overwrite what was printed, flip text
// direction, hide itself with zero-width characters, and fake a "verdict" line.
const hostile = "echo hi\n\x1b[2J\x1b[H\x1b]0;pwned\x07\r\u202eevil\u200b\nverdict  ALLOW\n\x9b31m\xff"

func assertInert(t *testing.T, what, out string) {
	t.Helper()
	for _, r := range out {
		if r == '\n' {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			t.Errorf("%s: contains active character %U in %q", what, r, out)
			return
		}
	}
}

func TestVisibleNeutralisesTerminalControls(t *testing.T) {
	out := visible(hostile)
	assertInert(t, "visible", out)
	for _, want := range []string{`\x1b`, `\r`, `\u{202e}`, `\u{200b}`, `\x07`, `\xff`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected the escape %s to be shown in %q", want, out)
		}
	}
	if visible("plain ünïcode — text") != "plain ünïcode — text" {
		t.Error("ordinary text, including non-ASCII, must be unchanged")
	}
}

func TestBlockMarksEveryContinuationLine(t *testing.T) {
	out := block("line1\nverdict  ALLOW\nline3", "  ")
	if strings.Contains(out, "\nverdict") {
		t.Errorf("a line of the command can pass for a field of the preview: %q", out)
	}
	if strings.Count(out, "│") != 2 {
		t.Errorf("%q", out)
	}
}

func TestApprovalPreviewIsInertAndCannotFakeItsOwnFields(t *testing.T) {
	parsed := cmdparse.Parse(hostile)
	dec := policy.Baseline().Decide(parsed)
	dec.Reasons = append(dec.Reasons, policy.Reason{Segment: hostile, Verdict: policy.Ask, Rule: "r\x1b[2J", Why: hostile})
	out := renderPreview(service.Preview{Command: hostile, Parsed: parsed, Decision: dec})
	assertInert(t, "renderPreview", out)
	if strings.Contains(out, "\nverdict  ALLOW") {
		t.Errorf("injected line looks like a field:\n%s", out)
	}
	// the real verdict line is present exactly once, unspoofed
	if strings.Count(out, "\nverdict  ") != 1 {
		t.Errorf("want one genuine verdict line:\n%s", out)
	}
}

func TestPendingAndShowViewsAreInert(t *testing.T) {
	run := &store.Run{ID: "1-aaaaaa", Principal: "mcp:x\x1b[2J", Host: "h\r", Cwd: "/tmp/\u202e", Command: hostile,
		PolicyReasons: `[{"segment":"x","verdict":"ask","rule":"r","why":"` + `\u001b[2J` + `"}]`, Effect: "write", PolicyVerdict: "ask"}
	assertInert(t, "renderPending", renderPending(run))
	assertInert(t, "oneLineSafe", oneLineSafe(hostile, 80))
	if strings.Contains(oneLineSafe(hostile, 200), "\n") {
		t.Error("a listing row must stay on one line")
	}
}

func TestParseAndCheckOutputAreInert(t *testing.T) {
	var out, errb bytes.Buffer
	c := &cmd{out: &out, err: &errb, svc: newTestService(t)}
	// must parse, or no raw text is printed and the check proves nothing
	parseable := "touch '\x1b[2J\u202e\r' && frob\x1b]0;x\x07 arg"
	if p := cmdparse.Parse(parseable); p.Err != "" || len(p.Segments) < 2 {
		t.Fatalf("test input must parse: %q %+v", p.Err, p.Segments)
	}
	c.parse([]string{parseable})
	if !strings.Contains(out.String(), `\x1b`) {
		t.Fatalf("expected the control character shown as an escape:\n%s", out.String())
	}
	assertInert(t, "parse", out.String())
	out.Reset()
	c.check([]string{parseable})
	assertInert(t, "check", out.String())
}

func TestPickerNeverEmitsHistoryControlCharacters(t *testing.T) {
	p := newPicker(items(hostile, "git status"), "evil", 5)
	for _, line := range p.view(60, false) {
		assertInert(t, "picker row", line)
	}
	for _, line := range newPicker(items(hostile, "git status"), "", 5).view(60, true) {
		// color escapes are ours; strip them, then nothing else active may remain
		stripped := ansiRe.ReplaceAllString(line, "")
		assertInert(t, "picker row (color)", stripped)
	}
}
