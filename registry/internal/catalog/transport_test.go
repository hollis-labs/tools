package catalog

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/kinds"
)

func TestLiveResolvedEdgesNeverReturnStaleNotModified(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	a := create(t, s, input("source"))
	b := create(t, s, input("target"))
	rel := &kinds.Relation{Schema: kinds.RelationSchema, Type: kinds.Consumes, From: kinds.Reference{Authority: "registry", Kind: "project", ID: a.ID}, To: kinds.Reference{Authority: "registry", Kind: "project", ID: b.ID}, Source: "authored", Status: "proposed"}
	source := callRecord(t, s, writer, "relation_create", Args{ID: a.ID, Version: a.VersionToken, Relation: rel})
	handler := Handler(s.Call)
	read := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/v1/record?id="+url.QueryEscape(a.ID)+"&args="+url.QueryEscape(`{"publication":"draft"}`), nil).WithContext(writer)
		req.Header.Set("If-None-Match", etag)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	first := read("")
	if first.Code != 200 || first.Header().Get("ETag") != "" {
		t.Fatalf("live graph must not use source-only validator: %d %s", first.Code, first.Header().Get("ETag"))
	}
	callRecord(t, s, writer, "record_archive", Args{ID: b.ID, Version: b.VersionToken})
	next := read(`"` + source.VersionToken + `"`)
	if next.Code != 200 || !strings.Contains(next.Body.String(), `"target_state":"archived"`) {
		t.Fatalf("stale resolution: %d %s", next.Code, next.Body)
	}
	// An immutable snapshot remains conditionally cacheable at the same boundary.
	req := httptest.NewRequest("GET", "/v1/snapshot", nil).WithContext(writer)
	snapshot := httptest.NewRecorder()
	handler.ServeHTTP(snapshot, req)
	if snapshot.Code != 200 || snapshot.Header().Get("ETag") == "" {
		t.Fatal("snapshot validator missing")
	}
	req = httptest.NewRequest("GET", "/v1/snapshot", nil).WithContext(writer)
	req.Header.Set("If-None-Match", snapshot.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	handler.ServeHTTP(cached, req)
	if cached.Code != 304 {
		t.Fatalf("stable snapshot changed validator: %d %s", cached.Code, cached.Body)
	}
}

func TestRecordETagWritePreconditionsAndVerification(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	r := create(t, s, input("etag"))
	handler := Handler(s.Call)
	get := func(previous string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/v1/record?id="+url.QueryEscape(r.ID)+"&args="+url.QueryEscape(`{"publication":"draft"}`), nil).WithContext(writer)
		req.Header.Set("If-None-Match", previous)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	first := get("")
	if first.Code != 200 || first.Header().Get("ETag") == "" {
		t.Fatal("missing draft ETag")
	}
	cached := get(first.Header().Get("ETag"))
	if cached.Code != 304 {
		t.Fatal("unchanged representation did not cache")
	}
	callRecord(t, s, writer, "record_verify", Args{ID: r.ID, Version: r.VersionToken})
	verified := get(first.Header().Get("ETag"))
	if verified.Code != 200 || verified.Header().Get("ETag") == first.Header().Get("ETag") {
		t.Fatal("verification did not invalidate representation")
	}
	body, _ := json.Marshal(Args{ID: r.ID, Version: r.VersionToken, Record: input("etag")})
	put := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/v1/record", bytes.NewReader(body)).WithContext(writer)
		req.Header.Set("If-Match", etag)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	stale := put(first.Header().Get("ETag"))
	if stale.Code != 409 {
		t.Fatalf("stale observation ETag accepted: %d %s", stale.Code, stale.Body)
	}
	updated := put(verified.Header().Get("ETag"))
	if updated.Code != 200 {
		t.Fatalf("GET/If-Match roundtrip failed: %d %s", updated.Code, updated.Body)
	}
	var next Record
	if err := json.Unmarshal(updated.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	ap := approvePublication(t, s, preparePublication(t, s, Candidate{ID: next.ID, Revision: next.RevisionID}))
	publish(t, s, ap, "published")
	req := httptest.NewRequest("GET", "/v1/record?id="+url.QueryEscape(r.ID), nil).WithContext(writer)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body, _ = json.Marshal(Args{ID: next.ID, Record: input("etag")})
	publishedUpdate := put(rec.Header().Get("ETag"))
	if publishedUpdate.Code != 200 {
		t.Fatalf("published ETag cannot condition current draft: %d %s", publishedUpdate.Code, publishedUpdate.Body)
	}
}
