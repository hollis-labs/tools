package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tools/registry/client"
)

// The generated client in client/ talks to the real handler over a real service: what openapi.yaml
// declares is what the API answers. IDs travel as query values, GET arguments as ?args=<JSON>, writes
// as an envelope body with the version in If-Match, and every failure decodes into the nested
// ErrorEnvelope.
func TestGeneratedClientAgainstTheAPI(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "db"))
	handler := Handler(s.Call)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The test server's stand-in for the verified principal the framework puts in the context.
		ctx := writer
		switch r.Header.Get("X-Test-As") {
		case "reviewer":
			ctx = reviewer
		case "nobody":
			ctx = caller("nobody", "registry.read")
		}
		handler.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(ts.Close)

	as := func(who string) *client.ClientWithResponses {
		t.Helper()
		c, err := client.NewClientWithResponses(ts.URL, client.WithRequestEditorFn(
			func(_ context.Context, r *http.Request) error { r.Header.Set("X-Test-As", who); return nil }))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	user := as("writer")
	ctx := context.Background()
	draft := client.PublicationArgs{Publication: ptr(client.PublicationArgsPublicationDraft)}

	t.Run("kinds and schema", func(t *testing.T) {
		k, err := user.KindsListWithResponse(ctx)
		if err != nil || k.JSON200 == nil || len(k.JSON200.Schemas) == 0 {
			t.Fatalf("kinds: %v %s", err, k.Body)
		}
		sc, err := user.SchemaGetWithResponse(ctx, &client.SchemaGetParams{Schema: "project/1"})
		if err != nil || sc.JSON200 == nil {
			t.Fatalf("schema: %v %s", err, sc.Body)
		}
		bad, _ := user.SchemaGetWithResponse(ctx, &client.SchemaGetParams{Schema: "nope/9"})
		if bad.StatusCode() != http.StatusBadRequest || bad.JSONDefault == nil || bad.JSONDefault.Error.Code != "unknown_schema" {
			t.Errorf("unknown schema: %d %s", bad.StatusCode(), bad.Body)
		}
	})

	// create, read, list
	payload := map[string]any{"schema": "project/1", "id": "pilot-a", "name": "pilot-a", "class": 2, "lifecycle": "active",
		"visibility": "private", "owner": "user:test", "interfaces": []any{map[string]any{"type": "cli", "auth": "none"}}}
	created, err := user.RecordCreateWithResponse(ctx, client.RecordCreateArgs{
		Record: client.Input{Schema: "project/1", Key: "pilot-a", Payload: payload}})
	if err != nil || created.JSON200 == nil {
		t.Fatalf("create: %v %s", err, created.Body)
	}
	rec := created.JSON200
	if rec.VersionToken == "" || rec.RevisionId == "" {
		t.Fatalf("create returned no version: %+v", rec)
	}

	t.Run("get by id, then conditionally", func(t *testing.T) {
		got, err := user.RecordGetWithResponse(ctx, &client.RecordGetParams{Id: rec.Id, Args: &draft})
		if err != nil || got.JSON200 == nil || got.JSON200.Id != rec.Id || got.JSON200.Key != "pilot-a" {
			t.Fatalf("get: %v %s", err, got.Body)
		}
		etag := got.HTTPResponse.Header.Get("ETag")
		if etag == "" {
			t.Fatal("no ETag on a record without edges")
		}
		again, _ := user.RecordGetWithResponse(ctx, &client.RecordGetParams{Id: rec.Id, Args: &draft, IfNoneMatch: &etag})
		if again.StatusCode() != http.StatusNotModified {
			t.Errorf("If-None-Match: %d", again.StatusCode())
		}
	})
	t.Run("list sees the draft", func(t *testing.T) {
		l, err := user.RecordListWithResponse(ctx, &client.RecordListParams{Args: &client.QueryArgs{Query: &client.Query{
			Publication: ptr("draft"), Kind: ptr("project")}}})
		if err != nil || l.JSON200 == nil || len(l.JSON200.Records) != 1 || l.JSON200.Records[0].Id != rec.Id {
			t.Fatalf("list: %v %s", err, l.Body)
		}
	})
	t.Run("update needs the current version; a stale one is a 409", func(t *testing.T) {
		upd := client.RecordUpdateArgs{Id: rec.Id, Version: rec.VersionToken,
			Record: client.Input{Schema: "project/1", Key: "pilot-a", Payload: payload}}
		ok, err := user.RecordUpdateWithResponse(ctx, &client.RecordUpdateParams{}, upd)
		if err != nil || ok.JSON200 == nil || ok.JSON200.VersionToken == rec.VersionToken {
			t.Fatalf("update: %v %s", err, ok.Body)
		}
		stale, _ := user.RecordUpdateWithResponse(ctx, &client.RecordUpdateParams{}, upd)
		if stale.StatusCode() != http.StatusConflict || stale.JSONDefault == nil || stale.JSONDefault.Error.Code != "version_conflict" {
			t.Errorf("stale update: %d %s", stale.StatusCode(), stale.Body)
		}
		rec = ok.JSON200
	})
	t.Run("revisions", func(t *testing.T) {
		h, err := user.RevisionListWithResponse(ctx, &client.RevisionListParams{Id: rec.Id})
		if err != nil || h.JSON200 == nil || len(h.JSON200.Revisions) != 2 {
			t.Fatalf("history: %v %s", err, h.Body)
		}
		one, err := user.RevisionGetWithResponse(ctx, &client.RevisionGetParams{Id: rec.Id, Args: client.ArgsRevisionGet{Revision: rec.RevisionId}})
		if err != nil || one.JSON200 == nil || one.JSON200.RevisionId != rec.RevisionId {
			t.Fatalf("revision: %v %s", err, one.Body)
		}
	})
	t.Run("relations", func(t *testing.T) {
		other, _ := user.RecordCreateWithResponse(ctx, client.RecordCreateArgs{Record: client.Input{Schema: "project/1", Key: "pilot-b",
			Payload: map[string]any{"schema": "project/1", "id": "pilot-b", "name": "pilot-b", "class": 2, "lifecycle": "active",
				"visibility": "private", "owner": "user:test", "interfaces": []any{map[string]any{"type": "cli", "auth": "none"}}}}})
		if other.JSON200 == nil {
			t.Fatalf("second record: %s", other.Body)
		}
		made, err := user.RelationCreateWithResponse(ctx, &client.RelationCreateParams{}, client.RelationCreateArgs{
			Id: rec.Id, Version: rec.VersionToken, Relation: client.Relation{
				Schema: "relation/1", Type: "consumes", Source: "authored", Status: "proposed",
				From: client.Reference{Authority: "registry", Kind: "project", Id: rec.Id},
				To:   client.Reference{Authority: "registry", Kind: "project", Id: other.JSON200.Id}}})
		if err != nil || made.JSON200 == nil {
			t.Fatalf("relation create: %v %s", err, made.Body)
		}
		l, err := user.RelationListWithResponse(ctx, &client.RelationListParams{Id: rec.Id, Args: &client.RelationListArgs{Publication: ptr(client.RelationListArgsPublicationDraft)}})
		if err != nil || l.JSON200 == nil || len(l.JSON200.Edges) == 0 {
			t.Fatalf("relation list: %v %s", err, l.Body)
		}
	})
	t.Run("a missing record is a 404 ErrorEnvelope", func(t *testing.T) {
		m, _ := user.RecordGetWithResponse(ctx, &client.RecordGetParams{Id: "project:does-not-exist", Args: &draft})
		if m.StatusCode() != http.StatusNotFound || m.JSONDefault == nil || m.JSONDefault.Error.Code != "not_found" || m.JSONDefault.Error.RequestId == nil {
			t.Errorf("missing: %d %s", m.StatusCode(), m.Body)
		}
	})
	t.Run("a missing scope is a 403 ErrorEnvelope", func(t *testing.T) {
		f, _ := as("nobody").RecordCreateWithResponse(ctx, client.RecordCreateArgs{Record: client.Input{Schema: "project/1", Key: "x", Payload: payload}})
		if f.StatusCode() != http.StatusForbidden || f.JSONDefault == nil || f.JSONDefault.Error.Code != "forbidden" {
			t.Errorf("forbidden: %d %s", f.StatusCode(), f.Body)
		}
	})
}

func ptr[T any](v T) *T { return &v }
