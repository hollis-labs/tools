// Package fuzzy is a small fuzzy matcher in the fzf style: a pattern matches a
// text when its characters appear in order, and the best alignment is scored
// with bonuses for word boundaries and consecutive runs. It is a standalone
// reimplementation (no fzf code), enough for a command-history picker.
package fuzzy

import (
	"sort"
	"strings"
	"unicode"
)

const (
	scoreMatch        = 16
	scoreGapStart     = -3
	scoreGapExtension = -1
	bonusBoundary     = scoreMatch / 2
	bonusCamel        = bonusBoundary - 1
	bonusConsecutive  = -(scoreGapStart + scoreGapExtension)
	bonusFirstChar    = 2 // multiplier applied to the bonus of the first pattern char
)

type class int

const (
	clsNone class = iota
	clsSpace
	clsDelim
	clsLower
	clsUpper
	clsDigit
)

func classOf(r rune) class {
	switch {
	case unicode.IsSpace(r):
		return clsSpace
	case strings.ContainsRune("/-_.:,;|=@", r):
		return clsDelim
	case unicode.IsLower(r):
		return clsLower
	case unicode.IsUpper(r):
		return clsUpper
	case unicode.IsDigit(r):
		return clsDigit
	}
	return clsDelim
}

// bonusAt is the boundary bonus for a match at a position given the class of the
// previous character.
func bonusAt(prev, cur class) int {
	switch {
	case cur == clsNone:
		return 0
	case prev == clsSpace:
		return bonusBoundary + 2
	case prev == clsDelim && cur != clsDelim:
		return bonusBoundary + 1
	case prev == clsLower && cur == clsUpper:
		return bonusCamel
	case prev == clsLower && cur == clsDigit, prev != clsDigit && cur == clsDigit:
		return bonusCamel
	}
	return 0
}

// Match scores pattern against text. ok is false when pattern is not a
// subsequence of text. pos holds the rune indexes of the best alignment.
// Matching is case-insensitive unless the pattern contains an upper-case rune.
func Match(pattern, text string) (score int, pos []int, ok bool) {
	p := []rune(pattern)
	if len(p) == 0 {
		return 0, nil, true
	}
	t := []rune(text)
	if len(p) > len(t) {
		return 0, nil, false
	}
	smart := true
	for _, r := range p {
		if unicode.IsUpper(r) {
			smart = false
		}
	}
	eq := func(a, b rune) bool {
		if a == b {
			return true
		}
		return smart && unicode.ToLower(b) == a
	}

	// Quick reject: subsequence test.
	k := 0
	for _, r := range t {
		if k < len(p) && eq(p[k], r) {
			k++
		}
	}
	if k < len(p) {
		return 0, nil, false
	}

	n, m := len(t), len(p)
	bonus := make([]int, n)
	prev := clsSpace
	for j, r := range t {
		c := classOf(r)
		bonus[j] = bonusAt(prev, c)
		prev = c
	}

	const neg = -1 << 30
	// h[i][j]: best score matching p[:i+1] within t[:j+1] (last match anywhere <= j)
	// d[i][j]: best score matching p[:i+1] with p[i] matched exactly at t[j]
	// run[i][j]: length of the consecutive run ending at (i,j) in that best d
	h := make([][]int, m)
	d := make([][]int, m)
	run := make([][]int, m)
	for i := range h {
		h[i] = make([]int, n)
		d[i] = make([]int, n)
		run[i] = make([]int, n)
		for j := range h[i] {
			h[i][j], d[i][j] = neg, neg
		}
	}
	for i := 0; i < m; i++ {
		for j := i; j < n; j++ {
			best := neg
			if eq(p[i], t[j]) {
				b := bonus[j]
				if i == 0 {
					best = scoreMatch + b*bonusFirstChar
					run[i][j] = 1
				} else if j > 0 {
					// start a new run after a gap
					if g := h[i-1][j-1]; g > neg {
						best = g + scoreMatch + b
						run[i][j] = 1
						if j-1 >= 0 && d[i-1][j-1] == g {
							// h came from a match exactly at j-1: this extends a run
							r := run[i-1][j-1] + 1
							rb := bonus[j-r+1]
							if rb < bonusBoundary {
								rb = bonusBoundary
							}
							if b > rb {
								rb = b
							}
							best = g + scoreMatch + max(rb, bonusConsecutive)
							run[i][j] = r
						}
					}
				}
			}
			d[i][j] = best
			// h: best of matching at j, or carrying h[i][j-1] with a gap penalty
			hv := best
			if j > i && h[i][j-1] > neg {
				gap := scoreGapExtension
				if d[i][j-1] == h[i][j-1] {
					gap = scoreGapStart
				}
				if v := h[i][j-1] + gap; v > hv {
					hv = v
				}
			}
			h[i][j] = hv
		}
	}

	// Best end column for the last pattern char; prefer later ties? earlier is
	// shorter-gap, keep first best.
	bj, bs := -1, neg
	for j := m - 1; j < n; j++ {
		if d[m-1][j] > bs {
			bj, bs = j, d[m-1][j]
		}
	}
	if bj < 0 {
		return 0, nil, false
	}
	// Traceback: for each i from last to first, find the match column.
	pos = make([]int, m)
	j := bj
	for i := m - 1; i >= 0; i-- {
		pos[i] = j
		if i == 0 {
			break
		}
		// previous char matched at the largest j' < j whose d[i-1][j'] is part of h[i-1][j-1]
		target := h[i-1][j-1]
		found := -1
		for jj := j - 1; jj >= i-1; jj-- {
			if d[i-1][jj] > neg && d[i-1][jj] >= target-3*(j-1-jj) {
				// accept the nearest column that reproduces the optimum within the gap cost
				if scoreAfterGap(d[i-1][jj], j-1-jj) == target {
					found = jj
					break
				}
			}
		}
		if found < 0 {
			// fall back to the best d in range (alignment still valid, score unaffected)
			bestv := neg
			for jj := j - 1; jj >= i-1; jj-- {
				if d[i-1][jj] > bestv {
					bestv, found = d[i-1][jj], jj
				}
			}
		}
		j = found
	}
	return bs, pos, true
}

func scoreAfterGap(v, gap int) int {
	if gap <= 0 {
		return v
	}
	return v + scoreGapStart + (gap-1)*scoreGapExtension
}

// Result is one ranked item.
type Result struct {
	Index int // index into the input slice
	Score int
	Pos   []int // rune positions to highlight, over the combined terms
}

// Rank filters items by query and orders them best first. A query is
// space-separated terms that must all match (AND). Ties keep input order, so
// pass items most-recent first. An empty query returns every item in order.
func Rank(query string, items []string) []Result {
	terms := strings.Fields(query)
	out := make([]Result, 0, len(items))
	for i, it := range items {
		if len(terms) == 0 {
			out = append(out, Result{Index: i})
			continue
		}
		total := 0
		var all []int
		ok := true
		for _, term := range terms {
			s, pos, m := Match(term, it)
			if !m {
				ok = false
				break
			}
			total += s
			all = append(all, pos...)
		}
		if ok {
			sort.Ints(all)
			out = append(out, Result{Index: i, Score: total, Pos: dedupe(all)})
		}
	}
	if len(terms) > 0 {
		sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	}
	return out
}

func dedupe(s []int) []int {
	if len(s) < 2 {
		return s
	}
	o := s[:1]
	for _, v := range s[1:] {
		if v != o[len(o)-1] {
			o = append(o, v)
		}
	}
	return o
}
