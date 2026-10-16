package ref

import (
	"regexp"
	"sort"
)

// Candidate is a ref-shaped span found in text. Detection never fetches
// anything; a candidate is only shown once a resolver validates it.
type Candidate struct {
	Ref        Ref
	Start, End int
}

var (
	uriRe   = regexp.MustCompile(`\b(?:cas|caster|torque|tesseract)://[A-Za-z0-9._/@-]+`)
	taskRe2 = regexp.MustCompile(`\bCW-\d{8}-\d{4}\b`)
)

// Detect finds refs in text: explicit URIs and bare Torque task ids.
func Detect(text string) []Candidate {
	var out []Candidate
	taken := func(s, e int) bool {
		for _, c := range out {
			if s < c.End && e > c.Start {
				return true
			}
		}
		return false
	}
	add := func(s, e int, raw string) {
		if taken(s, e) {
			return
		}
		if r, err := Parse(raw); err == nil {
			out = append(out, Candidate{Ref: r, Start: s, End: e})
		}
	}
	for _, m := range uriRe.FindAllStringIndex(text, -1) {
		raw := text[m[0]:m[1]]
		for len(raw) > 0 && (raw[len(raw)-1] == '.' || raw[len(raw)-1] == '-') {
			raw = raw[:len(raw)-1]
		}
		add(m[0], m[0]+len(raw), raw)
	}
	for _, m := range taskRe2.FindAllStringIndex(text, -1) {
		add(m[0], m[1], text[m[0]:m[1]])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}
