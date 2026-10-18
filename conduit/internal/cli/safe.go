package cli

import (
	"fmt"
	"math"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

// visible makes untrusted text safe to print on a terminal. Control characters
// (including ESC, so no cursor movement, screen clears or title changes), the
// C1 range, and invisible or direction-changing format characters (zero-width
// joiners, bidi overrides) are shown as escapes instead of acting. A tab shows
// as \t, a carriage return as \r, and invalid UTF-8 as \xNN. A newline is kept:
// use block for text where a line break could fake the layout around it.
//
// The command text an operator approves, and the reasons shown beside it, come
// from the submitter. If they could repaint the screen, the operator would be
// approving something other than what they read.
func visible(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\n':
			b.WriteByte('\n')
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
			if r < 0x100 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				fmt.Fprintf(&b, `\u{%x}`, r)
			}
		default:
			b.WriteRune(r)
		}
		i += n
	}
	return b.String()
}

// block renders multi-line untrusted text so every line after the first is
// marked, indented under its label. A command containing "\nverdict  ALLOW"
// then shows as visibly part of the command, not as a field of the preview.
func block(s, indent string) string {
	return strings.ReplaceAll(visible(s), "\n", "\n"+indent+"│ ")
}

// oneLineSafe is visible text on a single line, clipped to n runes.
func oneLineSafe(s string, n int) string {
	return oneLine(strings.ReplaceAll(visible(s), "\n", `\n`), n)
}

// isTerminal reports whether this command writes to a terminal. Stored output
// is untrusted: to a terminal it is shown with control characters made visible,
// to a pipe or file it is written as stored.
func (c *cmd) isTerminal() bool {
	f, ok := c.out.(*os.File)
	if !ok || f == nil {
		return false
	}
	fd := f.Fd()
	return fd <= math.MaxInt && term.IsTerminal(int(fd))
}
