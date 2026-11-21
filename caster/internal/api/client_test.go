package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/hollis-labs/tools/caster/client"
)

// The generated client in client/ talks to the real handlers: what openapi.yaml declares is what the
// API answers. A ref that does not resolve is an outcome (404 with a Meta body), a refused call is the
// flat Error body, and both decode into the client's types.
func TestGeneratedClientAgainstTheAPI(t *testing.T) {
	ts := newServer(t)
	ctx := context.Background()

	as := func(scopes string) *client.ClientWithResponses {
		t.Helper()
		c, err := client.NewClientWithResponses(ts.URL, client.WithRequestEditorFn(
			func(_ context.Context, r *http.Request) error {
				if scopes != "" {
					r.Header.Set("X-Test-Scopes", scopes) // the test server's stand-in for a verified principal
				}
				return nil
			}))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	user := as("caster.read caster.write")

	name := "pilot/hello"
	put, err := user.PutWithResponse(ctx, client.PutJSONRequestBody{Name: &name, Content: "hi there"})
	if err != nil || put.JSON200 == nil {
		t.Fatalf("put: %v %s", err, put.Body)
	}
	ref := put.JSON200.Ref

	t.Run("get returns what put stored", func(t *testing.T) {
		got, err := user.GetWithResponse(ctx, &client.GetParams{Ref: ref})
		if err != nil || got.JSON200 == nil || got.JSON200.Content == nil || *got.JSON200.Content != "hi there" {
			t.Fatalf("get: %v %s", err, got.Body)
		}
		if got.JSON200.State != client.ResolutionStateResolved {
			t.Errorf("state %q", got.JSON200.State)
		}
	})
	t.Run("list, log and search see the item", func(t *testing.T) {
		if l, _ := user.ListWithResponse(ctx, &client.ListParams{}); l.JSON200 == nil || len(l.JSON200.Items) != 1 {
			t.Errorf("list: %s", l.Body)
		}
		if l, _ := user.LogWithResponse(ctx, &client.LogParams{Name: name}); l.JSON200 == nil || len(l.JSON200.Revisions) != 1 {
			t.Errorf("log: %s", l.Body)
		}
		if s, _ := user.SearchWithResponse(ctx, &client.SearchParams{Q: "hi"}); s.JSON200 == nil || len(s.JSON200.Hits) != 1 {
			t.Errorf("search: %s", s.Body)
		}
	})
	t.Run("whoami", func(t *testing.T) {
		if w, _ := user.WhoamiWithResponse(ctx); w.JSON200 == nil || w.JSON200.Id != "key/test" {
			t.Errorf("whoami: %s", w.Body)
		}
	})
	t.Run("a missing ref is a 404 Meta, not an error", func(t *testing.T) {
		h, _ := user.HeadWithResponse(ctx, &client.HeadParams{Ref: "caster://pilot/missing"})
		if h.StatusCode() != http.StatusNotFound || h.JSON404 == nil || h.JSON404.State != client.ResolutionStateNotFound {
			t.Errorf("head: %d %s", h.StatusCode(), h.Body)
		}
	})
	t.Run("a missing scope is a 403 Error", func(t *testing.T) {
		r, _ := user.ShadowReportWithResponse(ctx, &client.ShadowReportParams{})
		if r.StatusCode() != http.StatusForbidden || r.JSONDefault == nil || r.JSONDefault.Code != client.ErrorCodeDenied {
			t.Errorf("report: %d %s", r.StatusCode(), r.Body)
		}
	})
	t.Run("no identity is a 401 Error", func(t *testing.T) {
		w, _ := as("").WhoamiWithResponse(ctx)
		if w.StatusCode() != http.StatusUnauthorized || w.JSONDefault == nil || w.JSONDefault.Code != client.ErrorCodeUnauthenticated {
			t.Errorf("whoami: %d %s", w.StatusCode(), w.Body)
		}
	})
}
