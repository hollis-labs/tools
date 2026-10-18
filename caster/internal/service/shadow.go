package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/hollis-labs/caster/internal/ref"
	"github.com/hollis-labs/caster/internal/resolver"
	"github.com/hollis-labs/caster/internal/store"
)

// Shadow limits.
const (
	MaxObserveBytes   = 256 << 10
	MaxResolvePerText = 50
)

// Shadow verdicts: what the policy dry-run decided for a detection.
const (
	VerdictCard       = "card"        // resolved, high confidence: would be shown
	VerdictQuietIndex = "quiet_index" // resolved, low confidence: indexed, never shown
	VerdictIgnore     = "ignore"      // did not resolve: plain text
	VerdictError      = "error"       // the resolver could not answer
	VerdictSkipped    = "skipped"     // over the per-text resolve budget
)

// ModeShadow is the only mode: detections are recorded and measured, and
// nothing is returned that a UI could render.
const ModeShadow = "shadow"

// Shadow is the domain service that runs the detection pipeline on text a UI or
// ingester hands it, validates each candidate with its resolver, applies the
// card/quiet policy as a dry run, and records the outcome. It exists to measure
// false positives before any card is enabled. Message text is never stored.
type Shadow struct {
	store *store.Store
	refs  *Refs
}

// ObserveRequest is text to observe. Source is an opaque label for where it came
// from (a session, a message id); it is kept only for the first sighting.
type ObserveRequest struct {
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

// ObserveResult is deliberately counts only: shadow mode shows nothing.
type ObserveResult struct {
	Mode       string `json:"mode"`
	Cached     bool   `json:"cached"` // this text was already observed at this generation
	Candidates int    `json:"candidates"`
	Resolved   int    `json:"resolved"`
	WouldShow  int    `json:"would_show"`
}

// Observe detects, validates and records. The same text at the same extractor
// generation is recorded once; later sightings only count as repeats.
func (s *Shadow) Observe(ctx context.Context, req ObserveRequest) (ObserveResult, error) {
	if err := require(ctx, ScopeObserve); err != nil {
		return ObserveResult{}, err
	}
	if len(req.Text) > MaxObserveBytes {
		return ObserveResult{}, invalid(fmt.Errorf("text over %d bytes", MaxObserveBytes))
	}
	sum := sha256.Sum256([]byte(req.Text))
	hash := hex.EncodeToString(sum[:])
	out := ObserveResult{Mode: ModeShadow}
	if o, err := s.store.FindObservation(hash, ref.Generation); err == nil {
		out.Cached, out.Candidates, out.Resolved, out.WouldShow = true, o.Candidates, o.Resolved, o.Shown
		return out, nil
	}
	cands := ref.DetectAll(req.Text)
	dets := make([]store.Detection, 0, len(cands))
	memo := map[string]resolver.Meta{}
	memoErr := map[string]error{}
	resolves := 0
	for _, c := range cands {
		d := store.Detection{Extractor: c.Extractor, Confidence: c.Confidence, Ref: c.Ref.String(), Scheme: c.Ref.Scheme}
		key := d.Ref
		m, seen := memo[key]
		err := memoErr[key]
		if !seen {
			if resolves >= MaxResolvePerText {
				d.State, d.Verdict = "skipped", VerdictSkipped
				dets = append(dets, d)
				continue
			}
			resolves++
			m, err = s.refs.reg.Resolve(ctx, c.Ref)
			memo[key], memoErr[key] = m, err
		}
		switch {
		case err != nil:
			d.State, d.Verdict, d.Error = "error", VerdictError, err.Error()
		case m.State == StateResolved && c.Confidence == ref.ConfHigh:
			d.State, d.Verdict, d.WouldShow = m.State, VerdictCard, true
		case m.State == StateResolved:
			d.State, d.Verdict = m.State, VerdictQuietIndex
		default:
			d.State, d.Verdict = m.State, VerdictIgnore
		}
		dets = append(dets, d)
	}
	o, err := s.store.RecordObservation(hash, ref.Generation, req.Source, dets)
	if err != nil {
		return ObserveResult{}, classify(err)
	}
	out.Candidates, out.Resolved, out.WouldShow = o.Candidates, o.Resolved, o.Shown
	return out, nil
}

// ReportRequest bounds the report to observations seen since a time.
type ReportRequest struct {
	Since time.Time `json:"since,omitempty"`
}

// ExtractorReport is one extractor's measured behavior. ResolveRate is the
// fraction of candidates a resolver confirmed. Precision is computed only from
// human labels (true / (true+false)) and is nil until something is labeled.
type ExtractorReport struct {
	Extractor    string   `json:"extractor"`
	Confidence   string   `json:"confidence"`
	Candidates   int      `json:"candidates"`
	Resolved     int      `json:"resolved"`
	Unresolved   int      `json:"unresolved"`
	Errors       int      `json:"errors"`
	WouldShow    int      `json:"would_show"`
	DistinctRefs int      `json:"distinct_refs"`
	ResolveRate  float64  `json:"resolve_rate"`
	LabeledTrue  int      `json:"labeled_true"`
	LabeledFalse int      `json:"labeled_false"`
	Precision    *float64 `json:"precision,omitempty"`
}

// RefCount is a ref and how many times it was detected.
type RefCount struct {
	Ref   string `json:"ref"`
	Count int    `json:"count"`
}

// ReportResult is the measurement so far.
type ReportResult struct {
	Mode          string            `json:"mode"`
	Generation    int               `json:"generation"`
	Observations  int               `json:"observations"`
	Repeats       int               `json:"repeats"` // sightings answered from the cache
	Extractors    []ExtractorReport `json:"extractors"`
	TopUnresolved []RefCount        `json:"top_unresolved"`
}

// Report aggregates what shadow mode has seen.
func (s *Shadow) Report(ctx context.Context, req ReportRequest) (ReportResult, error) {
	if err := require(ctx, ScopeAdmin); err != nil {
		return ReportResult{}, err
	}
	tot, stats, top, err := s.store.ShadowReport(req.Since, 20)
	if err != nil {
		return ReportResult{}, classify(err)
	}
	out := ReportResult{Mode: ModeShadow, Generation: ref.Generation, Observations: tot.Observations, Repeats: tot.Repeats,
		Extractors: []ExtractorReport{}, TopUnresolved: []RefCount{}}
	for _, e := range stats {
		r := ExtractorReport{Extractor: e.Extractor, Confidence: e.Confidence, Candidates: e.Candidates, Resolved: e.Resolved,
			Unresolved: e.Unresolved, Errors: e.Errors, WouldShow: e.WouldShow, DistinctRefs: e.DistinctRefs,
			LabeledTrue: e.LabeledTrue, LabeledFalse: e.LabeledFalse}
		if e.Candidates > 0 {
			r.ResolveRate = float64(e.Resolved) / float64(e.Candidates)
		}
		if n := e.LabeledTrue + e.LabeledFalse; n > 0 {
			p := float64(e.LabeledTrue) / float64(n)
			r.Precision = &p
		}
		out.Extractors = append(out.Extractors, r)
	}
	for _, c := range top {
		out.TopUnresolved = append(out.TopUnresolved, RefCount{c.Ref, c.Count})
	}
	return out, nil
}

// LabelRequest records a human judgement on a detected ref: Verdict "true" (the
// detection was right) or "false" (a false positive).
type LabelRequest struct {
	Ref     string `json:"ref"`
	Verdict string `json:"verdict"`
}

// LabelResult says how many recorded detections were labeled.
type LabelResult struct {
	Updated int64 `json:"updated"`
}

// Label sets the verdict on every recorded detection of a ref.
func (s *Shadow) Label(ctx context.Context, req LabelRequest) (LabelResult, error) {
	if err := require(ctx, ScopeAdmin); err != nil {
		return LabelResult{}, err
	}
	if req.Verdict != "true" && req.Verdict != "false" {
		return LabelResult{}, invalid(fmt.Errorf(`verdict must be "true" or "false"`))
	}
	r, err := parseRef(req.Ref)
	if err != nil {
		return LabelResult{}, err
	}
	n, err := s.store.LabelRef(r.String(), req.Verdict)
	if err != nil {
		return LabelResult{}, classify(err)
	}
	if n == 0 {
		return LabelResult{}, &Error{KindNotFound, fmt.Errorf("no recorded detections of %s", r)}
	}
	return LabelResult{Updated: n}, nil
}
