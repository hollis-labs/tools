package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/caster/internal/service"
	"github.com/hollis-labs/caster/internal/store"
)

type env struct {
	*httptest.Server
	svc    *service.Service
	signer ed25519.PrivateKey
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.New(st)

	// A stand-in for Tether's issuer: an Ed25519 key whose public half is the
	// verifier's bootstrap key, as it would be before the first JWKS fetch.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	keyFile := filepath.Join(t.TempDir(), "issuer.pub.pem")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	jv, err := auth.NewJWTVerifier(auth.JWTConfig{Issuer: "tether-test", Audience: "caster", BootstrapKeyFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	keys := auth.NewAPIKeyVerifier(auth.APIKey{Hash: auth.HashKey("operator-key"), ID: "caster/operator", Kind: auth.KindOperator, Scopes: []string{"caster.*"}})
	g, err := auth.New(auth.Config{Mode: auth.ModeEnforce, Verifier: auth.Chain(svc.Access.Verifier(), jv, keys), OpenPath: OpenPaths})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(Handler(svc, g))
	t.Cleanup(ts.Close)
	return &env{ts, svc, priv}
}

// jwt signs a token the way Tether's issuer does (EdDSA, kid, iss/aud/sub/scope/exp/jti).
func (e *env) jwt(aud, scope string, ttl time.Duration) string {
	b64 := base64.RawURLEncoding.EncodeToString
	hdr, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "k1"})
	now := time.Now()
	claims, _ := json.Marshal(map[string]any{"iss": "tether-test", "aud": aud, "sub": "msg://session/local/s1", "scope": scope,
		"iat": now.Unix(), "exp": now.Add(ttl).Unix(), "jti": "j1", "kind": "agent"})
	signing := b64(hdr) + "." + b64(claims)
	return signing + "." + b64(ed25519.Sign(e.signer, []byte(signing)))
}

func (e *env) do(t *testing.T, method, path, token, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *env) pair(t *testing.T, scopes ...string) string {
	t.Helper()
	g, err := e.svc.Access.CreateGrant(service.AsLocalOperator(t.Context()), service.PairRequest{Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	code, body := e.do(t, "POST", exchangePath, "", `{"code":"`+g.Code+`"}`)
	var ex service.ExchangeResult
	if code != 200 || json.Unmarshal([]byte(body), &ex) != nil || ex.Token == "" {
		t.Fatalf("exchange over HTTP: %d %s", code, body)
	}
	return ex.Token
}

func TestAuthenticationAndOpenRoutes(t *testing.T) {
	e := newEnv(t)
	if c, _ := e.do(t, "GET", "/health", "", ""); c != 200 {
		t.Fatalf("health must be open, got %d", c)
	}
	for _, tok := range []string{"", "wrong", "cst_nope"} {
		if c, _ := e.do(t, "GET", "/v1/list", tok, ""); c != 401 {
			t.Fatalf("token %q must be 401, got %d", tok, c)
		}
	}
	if c, _ := e.do(t, "POST", exchangePath, "", `{"code":"ccg_wrong"}`); c != 401 {
		t.Fatalf("a bad code must be 401, got %d", c)
	}
	if c, _ := e.do(t, "POST", "/v1/put", "", `{"name":"a/b","content":"x"}`); c != 401 {
		t.Fatalf("put without identity must be 401, got %d", c)
	}
}

func TestPairedDeviceScopesAreEnforcedByTheService(t *testing.T) {
	e := newEnv(t)
	reader := e.pair(t, "read")
	writer := e.pair(t, "read", "write")
	if c, _ := e.do(t, "POST", "/v1/put", reader, `{"name":"a/b","content":"x"}`); c != 403 {
		t.Fatalf("read-only device must not write, got %d", c)
	}
	if c, b := e.do(t, "POST", "/v1/put", writer, `{"name":"a/b","content":"hello"}`); c != 200 || !strings.Contains(b, "caster://a/b@1") {
		t.Fatalf("put: %d %s", c, b)
	}
	if c, b := e.do(t, "GET", "/v1/get?ref=a/b", reader, ""); c != 200 || !strings.Contains(b, `"content":"hello"`) {
		t.Fatalf("get: %d %s", c, b)
	}
	if c, _ := e.do(t, "GET", "/v1/get?ref=CW-20260417-0011", reader, ""); c != 403 {
		t.Fatalf("a remote-backed ref needs caster.resolve, got %d", c)
	}
	if c, _ := e.do(t, "GET", "/v1/devices", reader, ""); c != 403 {
		t.Fatalf("devices needs admin, got %d", c)
	}
	if c, b := e.do(t, "GET", "/v1/devices", "operator-key", ""); c != 200 || !strings.Contains(b, `"devices"`) {
		t.Fatalf("operator key lists devices: %d %s", c, b)
	}
	if c, b := e.do(t, "GET", "/v1/get?ref=a/nope", reader, ""); c != 404 || !strings.Contains(b, `"not_found"`) {
		t.Fatalf("a missing ref is a 404 outcome: %d %s", c, b)
	}
	if c, _ := e.do(t, "GET", "/v1/get?ref=../x", reader, ""); c != 400 {
		t.Fatalf("bad ref must be 400, got %d", c)
	}
}

func TestOperatorKeyCannotMintGrantsOverHTTP(t *testing.T) {
	e := newEnv(t)
	// There is no HTTP route for pairing grants, and the service refuses any
	// non-local caller, so a leaked operator key cannot enroll new devices.
	_, err := e.svc.Access.CreateGrant(service.WithPrincipal(t.Context(), auth.Principal{Verified: true, ID: "caster/operator", Kind: auth.KindOperator, Scopes: []string{"caster.*"}}),
		service.PairRequest{Scopes: []string{"read"}})
	if service.KindOf(err) != service.KindDenied {
		t.Fatalf("want denied, got %v", err)
	}
	if c, _ := e.do(t, "POST", "/v1/pair", "operator-key", `{"scopes":["read"]}`); c != 404 && c != 405 {
		t.Fatalf("no pairing route may exist over HTTP, got %d", c)
	}
}

func TestRevokedDeviceStopsAtOnce(t *testing.T) {
	e := newEnv(t)
	tok := e.pair(t, "read")
	if c, _ := e.do(t, "GET", "/v1/list", tok, ""); c != 200 {
		t.Fatalf("got %d", c)
	}
	ds, _ := e.svc.Access.Devices(service.AsLocalOperator(t.Context()), struct{}{})
	if _, err := e.svc.Access.Revoke(service.AsLocalOperator(t.Context()), service.RevokeRequest{ID: ds.Devices[0].ID}); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.do(t, "GET", "/v1/list", tok, ""); c != 401 {
		t.Fatalf("a revoked device must be 401 on its next request, got %d", c)
	}
}

func TestTetherIssuedTokens(t *testing.T) {
	e := newEnv(t)
	if c, _ := e.do(t, "GET", "/v1/list", e.jwt("caster", "caster.read", time.Hour), ""); c != 200 {
		t.Fatalf("a valid aud=caster token must work, got %d", c)
	}
	if c, _ := e.do(t, "POST", "/v1/put", e.jwt("caster", "caster.read", time.Hour), `{"name":"a/b","content":"x"}`); c != 403 {
		t.Fatalf("scope is enforced for JWTs too, got %d", c)
	}
	if c, _ := e.do(t, "GET", "/v1/list", e.jwt("tether", "caster.read", time.Hour), ""); c != 401 {
		t.Fatalf("a token minted for another audience must be refused, got %d", c)
	}
	if c, _ := e.do(t, "GET", "/v1/list", e.jwt("caster", "caster.read", -time.Hour), ""); c != 401 {
		t.Fatalf("an expired token must be refused, got %d", c)
	}
	if c, _ := e.do(t, "GET", "/v1/list", e.jwt("caster", "tether.read", time.Hour), ""); c != 403 {
		t.Fatalf("tether.* scopes grant nothing in caster, got %d", c)
	}
}

func TestExchangeIsRateLimited(t *testing.T) {
	e := newEnv(t)
	limited := false
	for i := 0; i < 30; i++ {
		if c, _ := e.do(t, "POST", exchangePath, "", `{"code":"ccg_guess"}`); c == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("guessing codes must hit the rate limit")
	}
}
