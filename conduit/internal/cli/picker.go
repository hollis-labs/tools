package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/conduit/internal/fuzzy"
	"github.com/hollis-labs/conduit/internal/service"
)

type keyKind int

const (
	kRune keyKind = iota
	kBackspace
	kClear   // ctrl-u
	kDelWord // ctrl-w
	kUp
	kDown
	kPgUp
	kPgDn
	kEnter
	kCancel
	kNone
)

type key struct {
	kind keyKind
	r    rune
}

type action int

const (
	actNone action = iota
	actAccept
	actCancel
)

// picker is the pure state of the interactive history picker: the query, the
// ranked results and the cursor. The terminal loop feeds it keys and draws
// view(); keeping it free of I/O makes it testable.
type picker struct {
	items   []service.HistoryItem
	texts   []string
	query   []rune
	results []fuzzy.Result
	sel     int // index into results
	top     int // first visible result
	height  int // visible list rows
}

func newPicker(items []service.HistoryItem, query string, height int) *picker {
	p := &picker{items: items, height: max(height, 1)}
	p.texts = make([]string, len(items))
	for i, it := range items {
		p.texts[i] = it.Command
	}
	p.query = []rune(query)
	p.refilter()
	return p
}

func (p *picker) refilter() {
	p.results = fuzzy.Rank(string(p.query), p.texts)
	p.sel, p.top = 0, 0
}

// selected returns the highlighted command, or "" when nothing matches.
func (p *picker) selected() string {
	if p.sel < 0 || p.sel >= len(p.results) {
		return ""
	}
	return p.texts[p.results[p.sel].Index]
}

func (p *picker) move(d int) {
	if len(p.results) == 0 {
		return
	}
	p.sel = min(max(p.sel+d, 0), len(p.results)-1)
	if p.sel < p.top {
		p.top = p.sel
	}
	if p.sel >= p.top+p.height {
		p.top = p.sel - p.height + 1
	}
}

func (p *picker) key(k key) action {
	switch k.kind {
	case kRune:
		p.query = append(p.query, k.r)
		p.refilter()
	case kBackspace:
		if len(p.query) > 0 {
			p.query = p.query[:len(p.query)-1]
			p.refilter()
		}
	case kClear:
		p.query = nil
		p.refilter()
	case kDelWord:
		q := p.query
		for len(q) > 0 && q[len(q)-1] == ' ' {
			q = q[:len(q)-1]
		}
		for len(q) > 0 && q[len(q)-1] != ' ' {
			q = q[:len(q)-1]
		}
		p.query = q
		p.refilter()
	case kUp:
		p.move(-1)
	case kDown:
		p.move(1)
	case kPgUp:
		p.move(-p.height)
	case kPgDn:
		p.move(p.height)
	case kEnter:
		if p.selected() == "" {
			return actCancel
		}
		return actAccept
	case kCancel:
		return actCancel
	case kNone:
	}
	return actNone
}

const (
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiRev   = "\x1b[7m"
	ansiGreen = "\x1b[32m"
	ansiReset = "\x1b[0m"
)

// view renders the prompt line and the visible list rows for a terminal of the
// given width. With color false the output has no escape codes.
func (p *picker) view(width int, color bool) []string {
	style := func(code, s string) string {
		if !color || s == "" {
			return s
		}
		return code + s + ansiReset
	}
	width = max(width, 20)
	lines := []string{fmt.Sprintf("%s %s  %s", style(ansiGreen, ">"), string(p.query),
		style(ansiDim, fmt.Sprintf("%d/%d", len(p.results), len(p.items))))}

	end := min(p.top+p.height, len(p.results))
	for i := p.top; i < end; i++ {
		r := p.results[i]
		it := p.items[r.Index]
		meta := metaFor(it)
		avail := width - 2 - utf8.RuneCountInString(meta) - 2
		hl := map[int]bool{}
		for _, ps := range r.Pos {
			hl[ps] = true
		}
		// Draw each original rune through visible(), so history text cannot move
		// the cursor or repaint the screen, while highlight positions still refer
		// to the runes the matcher saw.
		var b strings.Builder
		drawn, cut := 0, false
		for ci, ch := range []rune(strings.ReplaceAll(it.Command, "\n", "⏎")) {
			cell := visible(string(ch))
			if drawn+utf8.RuneCountInString(cell) > max(avail-1, 1) && ci < utf8.RuneCountInString(it.Command)-1 {
				cut = true
				break
			}
			if hl[ci] {
				cell = style(ansiBold+ansiGreen, cell)
			}
			b.WriteString(cell)
			drawn += utf8.RuneCountInString(visible(string(ch)))
		}
		if cut {
			b.WriteString("…")
		}
		pad := max(avail-drawn-btoi(cut), 0)
		line := b.String() + strings.Repeat(" ", pad) + "  " + style(ansiDim, meta)
		if i == p.sel {
			line = style(ansiRev, "▌") + line
		} else {
			line = " " + line
		}
		lines = append(lines, line)
	}
	if len(p.results) == 0 {
		lines = append(lines, style(ansiDim, "  no match"))
	}
	return lines
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// metaFor is the dim right-hand annotation: run count and success for conduit
// history, "shell" for lines taken from the shell's own history.
func metaFor(it service.HistoryItem) string {
	if it.Source != "conduit" {
		return "shell"
	}
	s := fmt.Sprintf("%dx", it.Runs)
	if it.OK < it.Runs {
		s += fmt.Sprintf(" %d%% ok", it.OK*100/it.Runs)
	}
	if it.Effect != "" && it.Effect != "read" && it.Effect != "none" {
		s += " " + it.Effect
	}
	return s
}
