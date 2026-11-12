package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tools/caster/internal/service"
	"github.com/hollis-labs/tools/caster/internal/storage/storagetest"
)

// newServer serves the API over a real storage. The guard and the identity module are not part of
// this package, so a test caller is stamped from the X-Test-Scopes header (absent = no identity),
// standing in for the verified principal the framework puts in the context.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	svc := service.New(storagetest.Open(t))
	mux := New(svc).Mux()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sc := r.Header.Get("X-Test-Scopes"); sc != "" {
			r = r.WithContext(libauth.WithPrincipal(r.Context(), libauth.Principal{Verified: true, ID: "key/test", Kind: libauth.KindDevice, Scopes: strings.Fields(sc)}))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, ts *httptest.Server, method, path, scopes, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if scopes != "" {
		req.Header.Set("X-Test-Scopes", scopes)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// errorBody decodes the framework's flat error body and fails if the answer is not one.
func errorBody(t *testing.T, body string) (code, message string) {
	t.Helper()
	var e map[string]any
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not JSON: %q", body)
	}
	if _, nested := e["error"]; nested {
		t.Fatalf("the error body must be flat {code, message}, got %q", body)
	}
	code, _ = e["code"].(string)
	message, _ = e["message"].(string)
	if code == "" || message == "" {
		t.Fatalf("an error needs a code and a message: %q", body)
	}
	return code, message
}

func TestNoIdentityIsUnauthenticated(t *testing.T) {
	ts := newServer(t)
	for _, c := range [][2]string{{"GET", "/api/list"}, {"POST", "/api/put"}, {"GET", "/api/whoami"}} {
		status, body := do(t, ts, c[0], c[1], "", `{"name":"a/b","content":"x"}`)
		if status != 401 {
			t.Fatalf("%s %s: want 401, got %d %s", c[0], c[1], status, body)
		}
		if code, _ := errorBody(t, body); code != service.KindUnauthenticated {
			t.Fatalf("code %q", code)
		}
	}
}

func TestScopesAreEnforcedByTheService(t *testing.T) {
	ts := newServer(t)
	if c, b := do(t, ts, "POST", "/api/put", "caster.read", `{"name":"a/b","content":"x"}`); c != 403 {
		t.Fatalf("read-only must not write: %d %s", c, b)
	} else if code, _ := errorBody(t, b); code != service.KindDenied {
		t.Fatalf("code %q", code)
	}
	if c, b := do(t, ts, "POST", "/api/put", "caster.read caster.write", `{"name":"a/b","content":"hello"}`); c != 200 || !strings.Contains(b, "caster://a/b@1") {
		t.Fatalf("put: %d %s", c, b)
	}
	if c, b := do(t, ts, "GET", "/api/get?ref=a/b", "caster.read", ""); c != 200 || !strings.Contains(b, `"content":"hello"`) {
		t.Fatalf("get: %d %s", c, b)
	}
	if c, b := do(t, ts, "GET", "/api/get?ref=CW-20260417-0011", "caster.read", ""); c != 403 {
		t.Fatalf("a remote-backed ref needs caster.resolve: %d %s", c, b)
	}
	if c, _ := do(t, ts, "GET", "/api/report", "caster.read caster.write", ""); c != 403 {
		t.Fatalf("report needs admin, got %d", c)
	}
	if c, b := do(t, ts, "GET", "/api/report", "caster.admin", ""); c != 200 {
		t.Fatalf("admin reads the report: %d %s", c, b)
	}
	if c, b := do(t, ts, "GET", "/api/whoami", "caster.read", ""); c != 200 || !strings.Contains(b, `"key/test"`) {
		t.Fatalf("whoami: %d %s", c, b)
	}
}

func TestMissingAndBadRefs(t *testing.T) {
	ts := newServer(t)
	if c, b := do(t, ts, "GET", "/api/get?ref=a/nope", "caster.read", ""); c != 404 || !strings.Contains(b, `"not_found"`) {
		t.Fatalf("a missing ref is a 404 outcome: %d %s", c, b)
	}
	c, b := do(t, ts, "GET", "/api/get?ref=../x", "caster.read", "")
	if c != 400 {
		t.Fatalf("a bad ref must be 400, got %d %s", c, b)
	}
	errorBody(t, b)
}

// A malformed full-text query is the caller's mistake: a clean 400 with the flat body, not a 500.
func TestMalformedSearchQueryIsABadRequest(t *testing.T) {
	ts := newServer(t)
	if c, b := do(t, ts, "POST", "/api/put", "caster.write", `{"name":"a/b","content":"a note about walruses"}`); c != 200 {
		t.Fatalf("put: %d %s", c, b)
	}
	if c, b := do(t, ts, "GET", "/api/search?q=walruses", "caster.read", ""); c != 200 || !strings.Contains(b, "[walruses]") {
		t.Fatalf("search: %d %s", c, b)
	}
	c, b := do(t, ts, "GET", "/api/search?q="+`%22unbalanced`, "caster.read", "")
	if c != 400 {
		t.Fatalf("a malformed query must be 400, got %d %s", c, b)
	}
	if code, _ := errorBody(t, b); code != service.KindInvalid {
		t.Fatalf("code %q", code)
	}
}

func TestBadBodiesAreBadRequests(t *testing.T) {
	ts := newServer(t)
	c, b := do(t, ts, "POST", "/api/put", "caster.write", `{not json`)
	if c != 400 {
		t.Fatalf("got %d %s", c, b)
	}
	errorBody(t, b)
	big := `{"name":"a/b","content":"` + strings.Repeat("x", maxBody+10) + `"}`
	if c, b = do(t, ts, "POST", "/api/put", "caster.write", big); c != 413 {
		t.Fatalf("an oversized body must be 413, got %d %.80s", c, b)
	}
}

func TestRawGetIsPlainText(t *testing.T) {
	ts := newServer(t)
	do(t, ts, "POST", "/api/put", "caster.write", `{"name":"a/h","content":"<script>x</script>","mime":"text/html"}`)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", ts.URL+"/api/get?ref=a/h&raw=1", nil)
	req.Header.Set("X-Test-Scopes", "caster.read")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("stored text must never be served as its stored mime: %q", ct)
	}
}
