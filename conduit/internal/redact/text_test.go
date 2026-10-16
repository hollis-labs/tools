package redact

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTextRedactsStripsAndBounds(t *testing.T) {
	in := "failed: GET https://user:pa55w0rd@example.com/path?token=abc123&x=1#frag " +
		"Authorization: Bearer abcdefghijklmnop1234 DB_PASSWORD=hunter2hunter2 \x1b[2J\u202e\x00end"
	out := Text(in, 500)
	for _, bad := range []string{"pa55w0rd", "token=abc123", "abcdefghijklmnop1234", "hunter2", "\x1b", "\u202e", "\x00", "#frag"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q survived in %q", bad, out)
		}
	}
	if !strings.Contains(out, "example.com/path") || !strings.Contains(out, "end") {
		t.Errorf("useful text was lost: %q", out)
	}
}

func TestTextNeverSplitsARuneAndBounds(t *testing.T) {
	out := Text(strings.Repeat("é€😀", 100), 7)
	if !utf8.ValidString(out) || utf8.RuneCountInString(out) > 8 {
		t.Errorf("%q (%d runes)", out, utf8.RuneCountInString(out))
	}
	if Text("\xff\xfe", 10) == "" || !utf8.ValidString(Text("\xff\xfe", 10)) {
		t.Error("invalid UTF-8 must be repaired, not dropped or passed on")
	}
}
