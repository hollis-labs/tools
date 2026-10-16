package service

import (
	"context"
	"strings"
	"testing"
)

func TestShadowPolicyDryRun(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	put, _ := s.Notes.Put(ctx, PutRequest{Name: "n/real", Content: "hello"})
	hash := strings.TrimPrefix(put.CAS, "cas://sha256/")
	missingHash := strings.Repeat("a", 64)
	text := "see caster://n/real and caster://n/gone, hash " + hash + " and " + missingHash + ", ulid 01M41PJBEQYR4TNQTVJ23DQBTE"

	res, err := s.Shadow.Observe(ctx, ObserveRequest{Text: text, Source: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != ModeShadow || res.Cached || res.Candidates != 5 || res.Resolved != 2 || res.WouldShow != 1 {
		t.Fatalf("want 5 candidates, 2 resolved, 1 would-show (only the high-confidence resolved one): %+v", res)
	}
	again, _ := s.Shadow.Observe(ctx, ObserveRequest{Text: text})
	if !again.Cached || again.Candidates != 5 {
		t.Fatalf("same text must be served from cache: %+v", again)
	}

	rep, err := s.Shadow.Report(ctx, ReportRequest{})
	if err != nil || rep.Observations != 1 || rep.Repeats != 1 {
		t.Fatalf("report: %+v %v", rep, err)
	}
	by := map[string]ExtractorReport{}
	for _, e := range rep.Extractors {
		by[e.Extractor] = e
	}
	if u := by["uri"]; u.Candidates != 2 || u.Resolved != 1 || u.WouldShow != 1 || u.Unresolved != 1 {
		t.Fatalf("uri stats: %+v", u)
	}
	if h := by["sha256"]; h.Candidates != 2 || h.Resolved != 1 || h.WouldShow != 0 {
		t.Fatalf("a resolved low-confidence match must be indexed quietly, never shown: %+v", h)
	}
	if by["ulid"].Errors != 1 { // no Tesseract resolver in this test: the resolver could not answer
		t.Fatalf("ulid stats: %+v", by["ulid"])
	}
}

func TestShadowLabelsGivePrecision(t *testing.T) {
	s, _ := newSvc(t)
	ctx := context.Background()
	_, _ = s.Notes.Put(ctx, PutRequest{Name: "n/a", Content: "x"})
	_, _ = s.Shadow.Observe(ctx, ObserveRequest{Text: "caster://n/a"})
	if _, err := s.Shadow.Label(ctx, LabelRequest{Ref: "caster://n/a", Verdict: "maybe"}); KindOf(err) != KindInvalid {
		t.Fatalf("bad verdict must be invalid: %v", err)
	}
	if _, err := s.Shadow.Label(ctx, LabelRequest{Ref: "caster://n/never-seen", Verdict: "true"}); KindOf(err) != KindNotFound {
		t.Fatalf("labelling an unseen ref must be not_found: %v", err)
	}
	if r, err := s.Shadow.Label(ctx, LabelRequest{Ref: "n/a", Verdict: "true"}); err != nil || r.Updated != 1 {
		t.Fatalf("label: %+v %v", r, err)
	}
	rep, _ := s.Shadow.Report(ctx, ReportRequest{})
	if p := rep.Extractors[0].Precision; p == nil || *p != 1 {
		t.Fatalf("precision: %+v", rep.Extractors[0])
	}
}

func TestShadowRejectsHugeText(t *testing.T) {
	s, _ := newSvc(t)
	if _, err := s.Shadow.Observe(context.Background(), ObserveRequest{Text: strings.Repeat("x", MaxObserveBytes+1)}); KindOf(err) != KindInvalid {
		t.Fatalf("want invalid, got %v", err)
	}
}
