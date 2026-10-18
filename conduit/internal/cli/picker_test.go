package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/conduit/internal/service"
)

func items(cmds ...string) []service.HistoryItem {
	out := make([]service.HistoryItem, len(cmds))
	for i, c := range cmds {
		out[i] = service.HistoryItem{Command: c, Source: "conduit", Runs: 1, OK: 1}
	}
	return out
}

func typeStr(p *picker, s string) {
	for _, r := range s {
		p.key(key{kind: kRune, r: r})
	}
}

func TestPickerFiltersAndSelects(t *testing.T) {
	p := newPicker(items("go test ./...", "git status", "git push", "ls"), "", 5)
	if len(p.results) != 4 || p.selected() != "go test ./..." {
		t.Fatalf("%d %q", len(p.results), p.selected())
	}
	typeStr(p, "gpu")
	if p.selected() != "git push" || len(p.results) != 1 {
		t.Fatalf("%d %q", len(p.results), p.selected())
	}
	if p.key(key{kind: kEnter}) != actAccept {
		t.Error("enter on a match should accept")
	}
}

func TestPickerNoMatchEnterCancels(t *testing.T) {
	p := newPicker(items("ls"), "zzz", 5)
	if p.selected() != "" || p.key(key{kind: kEnter}) != actCancel {
		t.Error("enter with no match must not accept")
	}
}

func TestPickerEditing(t *testing.T) {
	p := newPicker(items("git status", "git push"), "", 5)
	typeStr(p, "git pu")
	p.key(key{kind: kBackspace})
	if string(p.query) != "git p" {
		t.Errorf("%q", string(p.query))
	}
	p.key(key{kind: kDelWord})
	if string(p.query) != "git " {
		t.Errorf("delword: %q", string(p.query))
	}
	p.key(key{kind: kClear})
	if len(p.query) != 0 || len(p.results) != 2 {
		t.Errorf("clear: %q %d", string(p.query), len(p.results))
	}
	if p.key(key{kind: kCancel}) != actCancel {
		t.Error("cancel")
	}
}

func TestPickerCursorStaysInWindow(t *testing.T) {
	var cmds []string
	for i := 0; i < 30; i++ {
		cmds = append(cmds, "cmd"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	p := newPicker(items(cmds...), "", 5)
	for i := 0; i < 12; i++ {
		p.key(key{kind: kDown})
	}
	if p.sel != 12 || p.sel < p.top || p.sel >= p.top+5 {
		t.Fatalf("sel=%d top=%d", p.sel, p.top)
	}
	for i := 0; i < 100; i++ {
		p.key(key{kind: kDown})
	}
	if p.sel != 29 {
		t.Errorf("clamp: %d", p.sel)
	}
	p.key(key{kind: kPgUp})
	if p.sel != 24 {
		t.Errorf("pgup: %d", p.sel)
	}
	for i := 0; i < 100; i++ {
		p.key(key{kind: kUp})
	}
	if p.sel != 0 || p.top != 0 {
		t.Errorf("top clamp: %d %d", p.sel, p.top)
	}
}

func TestViewPlainFitsWidthAndMarksSelection(t *testing.T) {
	p := newPicker(items("git commit -m 'a very long message that will not fit in the narrow terminal width'", "ls"), "", 5)
	lines := p.view(40, false)
	if !strings.HasPrefix(lines[0], "> ") || !strings.Contains(lines[0], "2/2") {
		t.Errorf("prompt: %q", lines[0])
	}
	for _, l := range lines[1:] {
		if n := len([]rune(l)); n > 40 {
			t.Errorf("line wider than terminal (%d): %q", n, l)
		}
		if strings.Contains(l, "\x1b") {
			t.Errorf("color=false emitted escape: %q", l)
		}
	}
	if !strings.HasPrefix(lines[1], "▌") || !strings.HasPrefix(lines[2], " ") {
		t.Errorf("selection marker: %q / %q", lines[1], lines[2])
	}
	if !strings.Contains(lines[1], "…") {
		t.Errorf("long command should be clipped with an ellipsis: %q", lines[1])
	}
}

func TestViewHighlightsMatchedRunes(t *testing.T) {
	p := newPicker(items("git status"), "gst", 5)
	row := p.view(60, true)[1]
	if !strings.Contains(row, ansiBold) {
		t.Errorf("no highlight in %q", row)
	}
}

func TestMetaShowsOutcomeAndSource(t *testing.T) {
	if m := metaFor(service.HistoryItem{Source: "shell"}); m != "shell" {
		t.Error(m)
	}
	m := metaFor(service.HistoryItem{Source: "conduit", Runs: 4, OK: 3, Effect: "write"})
	if !strings.Contains(m, "4x") || !strings.Contains(m, "75% ok") || !strings.Contains(m, "write") {
		t.Error(m)
	}
}

func TestReadKeysDecodesSequences(t *testing.T) {
	in := bytes.NewReader([]byte("a\x1b[A\x1b[B\r\x7f\x15\x1b[5~é"))
	var got []key
	for k := range readKeys(in) {
		got = append(got, k)
	}
	want := []key{{kind: kRune, r: 'a'}, {kind: kUp}, {kind: kDown}, {kind: kEnter}, {kind: kBackspace}, {kind: kClear}, {kind: kPgUp}, {kind: kRune, r: 'é'}}
	if len(got) < len(want) {
		t.Fatalf("got %v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("key %d: got %v want %v", i, got[i], w)
		}
	}
}

func TestReadShellHistoryBashAndZsh(t *testing.T) {
	dir := t.TempDir()
	bash := filepath.Join(dir, "bash")
	_ = os.WriteFile(bash, []byte("#1700000000\nls -la\n#1700000001\ngit status\n#1700000002\nls -la\n"), 0o600)
	got := readShellHistory(bash, 10)
	if strings.Join(got, "|") != "ls -la|git status" {
		t.Errorf("bash: %v", got)
	}
	zsh := filepath.Join(dir, "zsh")
	_ = os.WriteFile(zsh, []byte(": 1700000000:0;ls\n: 1700000001:0;echo a\\\nb\n: 1700000002:0;git push\n"), 0o600)
	got = readShellHistory(zsh, 10)
	if len(got) != 3 || got[0] != "git push" || got[1] != "echo a\nb" || got[2] != "ls" {
		t.Errorf("zsh: %q", got)
	}
	if readShellHistory(filepath.Join(dir, "missing"), 5) != nil {
		t.Error("missing file should give no history")
	}
}

func TestInitScriptsSubstituteKeyAndRejectUnsafe(t *testing.T) {
	var out, errb bytes.Buffer
	c := &cmd{out: &out, err: &errb}
	if code := c.initShell([]string{"bash"}); code != ExitOK || !strings.Contains(out.String(), `bind -x '"\C-r": __conduit_pick'`) {
		t.Fatalf("%d %q", code, out.String())
	}
	if strings.Contains(out.String(), "@@KEY@@") {
		t.Error("placeholder left in output")
	}
	out.Reset()
	if code := c.initShell([]string{"zsh", "-key", `^G`}); code != ExitOK || !strings.Contains(out.String(), "bindkey '^G'") {
		t.Fatalf("%d %q", code, out.String())
	}
	out.Reset()
	if code := c.initShell([]string{"bash", "-key", `x'; rm -rf ~; '`}); code == ExitOK || out.Len() != 0 {
		t.Error("unsafe key accepted")
	}
	if code := c.initShell([]string{"fish"}); code == ExitOK {
		t.Error("unsupported shell accepted")
	}
}
