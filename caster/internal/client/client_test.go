package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tools/caster/internal/api"
	"github.com/hollis-labs/tools/caster/internal/client"
	"github.com/hollis-labs/tools/caster/internal/service"
	"github.com/hollis-labs/tools/caster/internal/storage/storagetest"
)

// node is the real API handler over a real storage. The guard and the identity module are not in this
// package, so a bearer "key_<scope>+<scope>" stands in for a verified API key holding those scopes
// (any other bearer is refused with the framework's flat 401), and POST /api/pair answers like the
// identity module does. The real binary's own two-process test covers the real thing.
type node struct {
	*httptest.Server
	home *service.Service
}

func newNode(t *testing.T) *node {
	t.Helper()
	home := service.New(storagetest.Open(t))
	mux := api.New(home).Mux()
	mux.HandleFunc("POST /api/pair", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Code string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Code != "pair_good" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"invalid_code","message":"the pairing code is not valid"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"key":"key_read+write","credential":{"id":"cred_1","name":"laptop","scopes":["caster.read","caster.write"]}}`))
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer key_"); ok {
			var scopes []string
			for _, s := range strings.Split(bearer, "+") {
				scopes = append(scopes, "caster."+s)
			}
			r = r.WithContext(libauth.WithPrincipal(r.Context(), libauth.Principal{Verified: true, ID: "key:test", Kind: libauth.KindDevice, Scopes: scopes}))
		} else if r.Header.Get("Authorization") != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"unauthorized","message":"the credential is not valid"}`))
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return &node{ts, home}
}

// as returns a client that presents an API key holding the named caster scopes.
func (n *node) as(t *testing.T, scopes ...string) *service.Service {
	t.Helper()
	c, err := client.New(n.URL, func(context.Context) (string, error) { return "key_" + strings.Join(scopes, "+"), nil }, false)
	if err != nil {
		t.Fatal(err)
	}
	return c.Service()
}

func TestRoundTripThroughTheRemoteContract(t *testing.T) {
	n := newNode(t)
	svc := n.as(t, "read", "write")
	ctx := context.Background()

	put, err := svc.Notes.Put(ctx, service.PutRequest{Name: "r/a", Content: "hello", R: strings.NewReader("hello over http")})
	if err != nil || put.Ref != "caster://r/a@1" {
		t.Fatalf("put: %+v %v", put, err)
	}
	got, err := svc.Refs.Get(ctx, service.GetRequest{Ref: "r/a", MaxBytes: -1})
	if err != nil || string(got.Data) != "hello over http" || got.Revision != "1" {
		t.Fatalf("get: %+v %v", got, err)
	}
	list, err := svc.Notes.List(ctx, service.ListRequest{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, logErr := svc.Notes.Log(ctx, service.LogRequest{Name: "r/nope"}); service.KindOf(logErr) != service.KindNotFound {
		t.Fatalf("a missing name is a not_found error: %v", logErr)
	}
	who, err := svc.Identity.Whoami(ctx, struct{}{})
	if err != nil || who.ID != "key:test" || strings.Join(who.Scopes, ",") != "caster.read,caster.write" {
		t.Fatalf("whoami: %+v %v", who, err)
	}
}

func TestOutcomesAreNotErrorsButFailuresAre(t *testing.T) {
	n := newNode(t)
	svc := n.as(t, "read")
	ctx := context.Background()

	got, err := svc.Refs.Get(ctx, service.GetRequest{Ref: "r/missing"})
	if err != nil || got.State != service.StateNotFound {
		t.Fatalf("a missing ref is an outcome carrying state, as locally: %+v %v", got, err)
	}
	if _, err := svc.Notes.Put(ctx, service.PutRequest{Name: "r/x", Content: "x"}); service.KindOf(err) != service.KindDenied {
		t.Fatalf("a read-only key must be denied, got %v", err)
	}
	if _, err := svc.Refs.Get(ctx, service.GetRequest{Ref: "CW-20260417-0011"}); service.KindOf(err) != service.KindDenied {
		t.Fatalf("a remote-backed ref needs caster.resolve: %v", err)
	}
	if _, err := svc.Refs.Get(ctx, service.GetRequest{Ref: "../x"}); service.KindOf(err) != service.KindInvalid {
		t.Fatalf("a bad ref is invalid: %v", err)
	}
	if _, err := svc.Shadow.Report(ctx, service.ReportRequest{}); service.KindOf(err) != service.KindDenied {
		t.Fatalf("the shadow report needs caster.admin: %v", err)
	}
}

func TestMalformedSearchIsAnInvalidError(t *testing.T) {
	n := newNode(t)
	svc := n.as(t, "read")
	if _, err := svc.Notes.Search(context.Background(), service.SearchRequest{Query: `"unterminated`}); service.KindOf(err) != service.KindInvalid {
		t.Fatalf("a malformed FTS query is a client error, not a server one: %v", err)
	}
}

func TestBinaryBytesSurviveViaRawFetch(t *testing.T) {
	n := newNode(t)
	bin := []byte{0xff, 0xfe, 0x00, 0x80, 'a'}
	put, err := n.home.Notes.Put(localCtx(), service.PutRequest{Name: "r/bin", R: bytes.NewReader(bin)})
	if err != nil {
		t.Fatal(err)
	}
	svc := n.as(t, "read")
	got, err := svc.Refs.Get(context.Background(), service.GetRequest{Ref: put.Ref, MaxBytes: -1})
	if err != nil || !got.Binary || !bytes.Equal(got.Data, bin) {
		t.Fatalf("binary: %+v %v", got, err)
	}
}

func localCtx() context.Context {
	return service.WithPrincipal(context.Background(), libauth.Principal{Verified: true, ID: "local:test", Scopes: []string{"*"}})
}

func TestBadKeySaysWhatToDo(t *testing.T) {
	n := newNode(t)
	c, _ := client.New(n.URL, func(context.Context) (string, error) { return "wrong", nil }, false)
	_, err := c.Service().Notes.List(context.Background(), service.ListRequest{})
	if service.KindOf(err) != service.KindUnauthenticated || !strings.Contains(err.Error(), "caster login") {
		t.Fatalf("want an actionable 401, got %v", err)
	}
	noKey, _ := client.New(n.URL, func(context.Context) (string, error) { return "", os.ErrNotExist }, false)
	if _, err := noKey.Service().Notes.List(context.Background(), service.ListRequest{}); service.KindOf(err) != service.KindUnauthenticated {
		t.Fatalf("an unreadable credential is unauthenticated: %v", err)
	}
}

func TestPairRedeemsAnIdentityCode(t *testing.T) {
	n := newNode(t)
	anon, err := client.New(n.URL, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	p, err := anon.Pair(context.Background(), "pair_good")
	if err != nil || p.Key != "key_read+write" || p.Credential.ID != "cred_1" {
		t.Fatalf("pair: %+v %v", p, err)
	}
	if _, err := anon.Pair(context.Background(), "pair_bad"); service.KindOf(err) != service.KindUnauthenticated {
		t.Fatalf("a bad code is unauthenticated, with no hint which part was wrong: %v", err)
	}
}

func TestRedirectsAreNeverFollowed(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect target received a request (Authorization=%q)", r.Header.Get("Authorization"))
	}))
	defer evil.Close()
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL, http.StatusFound)
	}))
	defer hop.Close()
	c, _ := client.New(hop.URL, func(context.Context) (string, error) { return "key_x", nil }, false)
	if _, err := c.Service().Notes.List(context.Background(), service.ListRequest{}); service.KindOf(err) != service.KindUnavailable {
		t.Fatalf("a redirect must fail, not be followed: %v", err)
	}
}

func TestUnreachableIsUnavailable(t *testing.T) {
	n := newNode(t)
	url := n.URL
	n.Close()
	c, _ := client.New(url, func(context.Context) (string, error) { return "key_x", nil }, false)
	if _, err := c.Service().Notes.List(context.Background(), service.ListRequest{}); service.KindOf(err) != service.KindUnavailable {
		t.Fatalf("want unavailable, got %v", err)
	}
}

func TestURLPolicy(t *testing.T) {
	ok := []string{"https://example.com", "http://127.0.0.1:8991", "http://localhost:8991", "http://100.64.0.1:8991", "http://example.tail1234.ts.net:8991"}
	for _, u := range ok {
		if _, err := client.ValidateURL(u, false); err != nil {
			t.Errorf("%s should be allowed: %v", u, err)
		}
	}
	for _, u := range []string{"http://example.com", "http://192.0.2.10:8991", "ftp://x", "not a url", ""} {
		if _, err := client.ValidateURL(u, false); err == nil {
			t.Errorf("%q should be refused", u)
		}
	}
	if _, err := client.ValidateURL("http://example.com", true); err != nil {
		t.Errorf("explicit opt-in must allow plain http: %v", err)
	}
}

func TestProfileHoldsNoSecretAndEnvWins(t *testing.T) {
	dir := t.TempDir()
	if p, err := client.LoadProfile(dir); p != nil || err != nil {
		t.Fatalf("no profile yet: %v %v", p, err)
	}
	if err := client.SaveProfile(dir, client.Profile{URL: "https://home.example", TokenRef: "keychain://caster/home", KeyID: "cred_1"}); err != nil { //nolint:gosec // a secrets reference, not a credential
		t.Fatal(err)
	}
	fi, _ := os.Stat(client.ProfilePath(dir))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("profile mode %v", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(client.ProfilePath(dir))
	if strings.Contains(string(raw), "key_") {
		t.Fatal("the profile must hold a reference, never a token")
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	if p, _ := client.Active(dir, env(nil)); p == nil || p.URL != "https://home.example" {
		t.Fatalf("saved profile should be active: %+v", p)
	}
	if p, _ := client.Active(dir, env(map[string]string{"CASTER_REMOTE": "https://other.example", "CASTER_TOKEN_REF": "file://caster/x"})); p.URL != "https://other.example" { //nolint:gosec // a secrets reference, not a credential
		t.Fatalf("env must win: %+v", p)
	}
	if _, err := client.Active(dir, env(map[string]string{"CASTER_REMOTE": "https://other.example"})); err == nil {
		t.Fatal("CASTER_REMOTE without CASTER_TOKEN_REF must be an error, not a silent fallback")
	}
	if p, _ := client.Active(dir, env(map[string]string{"CASTER_LOCAL": "1"})); p != nil {
		t.Fatal("CASTER_LOCAL=1 must force the local store")
	}
}

func TestLauncherResolvedTokenIsUsedButNeverShown(t *testing.T) {
	dir := t.TempDir()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, e := range map[string]map[string]string{
		"token env":           {"CASTER_REMOTE": "https://home.example", "CASTER_TOKEN": "key_literal"},
		"resolved ref in env": {"CASTER_REMOTE": "https://home.example", "CASTER_TOKEN_REF": "key_literal"},
	} {
		p, err := client.Active(dir, env(e))
		if err != nil || p == nil {
			t.Fatalf("%s: %v %v", name, p, err)
		}
		tok, err := p.Source()(context.Background())
		if err != nil || tok != "key_literal" {
			t.Fatalf("%s: token %q, %v", name, tok, err)
		}
		if strings.Contains(p.TokenLabel(), "key_") || p.TokenRef != "" {
			t.Fatalf("%s: the literal leaked into the display or the reference: %q", name, p.TokenLabel())
		}
		if b, _ := json.Marshal(p); strings.Contains(string(b), "key_") {
			t.Fatalf("%s: the literal would be written to a saved profile: %s", name, b)
		}
	}
}
