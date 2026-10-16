package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/caster/internal/service"
	"github.com/hollis-labs/caster/internal/store"
)

func server(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	keys := auth.NewAPIKeyVerifier(
		auth.APIKey{Hash: auth.HashKey("rw"), ID: "rw", Scopes: []string{service.ScopeRead, service.ScopeWrite}},
		auth.APIKey{Hash: auth.HashKey("ro"), ID: "ro", Scopes: []string{service.ScopeRead}},
	)
	g, err := auth.New(auth.Config{Mode: auth.ModeEnforce, Verifier: keys, Scopes: Scopes()})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(Handler(service.New(st), g))
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

func TestAuthAndRoundTrip(t *testing.T) {
	ts := server(t)
	if c, _ := do(t, "GET", ts.URL+"/health", "", ""); c != 200 {
		t.Fatalf("health must be open, got %d", c)
	}
	if c, _ := do(t, "GET", ts.URL+"/v1/list", "", ""); c != 401 {
		t.Fatalf("no token must be 401, got %d", c)
	}
	if c, _ := do(t, "GET", ts.URL+"/v1/list", "wrong", ""); c != 401 {
		t.Fatalf("bad token must be 401, got %d", c)
	}
	if c, _ := do(t, "POST", ts.URL+"/v1/put", "ro", `{"name":"a/b","content":"x"}`); c != 403 {
		t.Fatalf("read-only key must not write, got %d", c)
	}
	if c, b := do(t, "POST", ts.URL+"/v1/put", "rw", `{"name":"a/b","content":"hello"}`); c != 200 || !strings.Contains(b, "caster://a/b@1") {
		t.Fatalf("put: %d %s", c, b)
	}
	if c, b := do(t, "GET", ts.URL+"/v1/get?ref=a/b", "ro", ""); c != 200 || !strings.Contains(b, `"content":"hello"`) {
		t.Fatalf("get: %d %s", c, b)
	}
	if c, b := do(t, "GET", ts.URL+"/v1/get?ref=a/nope", "ro", ""); c != 404 || !strings.Contains(b, `"not_found"`) {
		t.Fatalf("missing ref must be a 404 outcome: %d %s", c, b)
	}
	if c, _ := do(t, "GET", ts.URL+"/v1/get?ref=../x", "ro", ""); c != 400 {
		t.Fatalf("bad ref must be 400, got %d", c)
	}
	if c, _ := do(t, "POST", ts.URL+"/v1/put", "rw", `{not json`); c != 400 {
		t.Fatalf("bad body must be 400, got %d", c)
	}
	if c, _ := do(t, "GET", ts.URL+"/v1/unmapped", "rw", ""); c != 403 && c != 404 {
		t.Fatalf("unmapped route must fail closed, got %d", c)
	}
}
