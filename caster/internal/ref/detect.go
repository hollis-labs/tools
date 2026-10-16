package ref

import (
	"regexp"
	"sort"
	"strings"
)

// Confidence classes. A high-confidence extractor matches a shape that is
// almost never anything else; a low-confidence one matches shapes that prose
// produces by accident, so it is only ever acted on after a resolver confirms.
const (
	ConfHigh = "high"
	ConfLow  = "low"
)

// Generation versions the extractor set. Cached detections are keyed by it, so
// changing an extractor means bumping it.
const Generation = 1

// Extractor finds ref-shaped spans of one kind. It never fetches anything.
type Extractor struct {
	Name       string
	Confidence string
	re         *regexp.Regexp
	build      func(match string) (string, bool) // match -> ref string
}

var extractors = []Extractor{
	{"uri", ConfHigh, regexp.MustCompile(`\b(?:cas|caster|torque|tesseract)://[A-Za-z0-9._/@-]+`),
		func(m string) (string, bool) { return strings.TrimRight(m, ".-"), true }},
	{"torque-id", ConfHigh, regexp.MustCompile(`\bCW-\d{8}-\d{4}\b`),
		func(m string) (string, bool) { return m, true }},
	// ULIDs are Tesseract item ids, but any 26 Crockford characters look the same.
	{"ulid", ConfLow, regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`),
		func(m string) (string, bool) { return "tesseract://item/" + m, true }},
	// A bare 64-hex string might be a CAS hash, or any other sha256.
	{"sha256", ConfLow, regexp.MustCompile(`\b[0-9a-f]{64}\b`),
		func(m string) (string, bool) { return "cas://sha256/" + m, true }},
}

// Extractors returns the registered extractors (name and confidence only).
func Extractors() []Extractor {
	out := make([]Extractor, len(extractors))
	for i, e := range extractors {
		out[i] = Extractor{Name: e.Name, Confidence: e.Confidence}
	}
	return out
}

// Candidate is a ref-shaped span found in text. Detection never fetches
// anything; a candidate is only shown once a resolver validates it.
type Candidate struct {
	Ref        Ref
	Start, End int
	Extractor  string
	Confidence string
}

// Detect finds refs in text using the high-confidence extractors: explicit
// URIs and bare Torque task ids. This is what explicit user actions (scan,
// expand) rely on.
func Detect(text string) []Candidate { return detect(text, true) }

// DetectAll also runs the low-confidence extractors. Shadow mode uses it to
// measure what they would add and how often they are wrong.
func DetectAll(text string) []Candidate { return detect(text, false) }

func detect(text string, highOnly bool) []Candidate {
	var out []Candidate
	overlaps := func(s, e int) bool {
		for _, c := range out {
			if s < c.End && e > c.Start {
				return true
			}
		}
		return false
	}
	for _, x := range extractors { // registration order = priority on overlap
		if highOnly && x.Confidence != ConfHigh {
			continue
		}
		for _, m := range x.re.FindAllStringIndex(text, -1) {
			raw, ok := x.build(text[m[0]:m[1]])
			if !ok {
				continue
			}
			end := m[0] + len(strings.TrimRight(text[m[0]:m[1]], ".-"))
			if x.Name != "uri" {
				end = m[1]
			}
			if overlaps(m[0], end) {
				continue
			}
			if r, err := Parse(raw); err == nil {
				out = append(out, Candidate{Ref: r, Start: m[0], End: end, Extractor: x.Name, Confidence: x.Confidence})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}
