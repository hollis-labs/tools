package catalog

import (
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
