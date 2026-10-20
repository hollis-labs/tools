package connectmatrix_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/auth"

	cm "github.com/hollis-labs/tools/connect-matrix"
)

const issName = "mock-test"

// target is a stand-in app: a go-auth guard in front of one route, trusting
// the mock issuer's JWKS and (optionally) its deny-list, exactly as Torque or
// Tesseract are configured.
func target(t *testing.T, issuerURL string, mode auth.Mode, withDenyList bool) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := auth.JWTConfig{Issuer: issName, Audience: "torque", JWKSURL: issuerURL + "/.well-known/jwks.json"}
	if withDenyList {
		dl := auth.NewDenyList()
		cfg.DenyList = dl
		go (&auth.DenyListPoller{URL: issuerURL + "/auth/denylist", List: dl, Interval: 25 * time.Millisecond}).Run(ctx)
	}
	jv, err := auth.NewJWTVerifier(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g, err := auth.New(auth.Config{Mode: mode, Verifier: jv, Scopes: auth.NewScopeTable().Route("GET", "/api/v1/tasks", "torque.read")})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })))
	t.Cleanup(ts.Close)
	return ts
}

func mockIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	m, err := cm.NewMockIssuer(issName)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(m.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func runner(t *testing.T, iss *httptest.Server, tgt *httptest.Server, enforcing bool, boundSeconds int) *cm.Runner {
	t.Helper()
	cfg := cm.Config{
		Issuer:             cm.IssuerConfig{Type: "mock", URL: iss.URL},
		RevokeBoundSeconds: boundSeconds, PollIntervalMillis: 20,
		Pairs: []cm.Pair{{Name: "torque", Target: tgt.URL, Audience: "torque", Scopes: []string{"torque.read"},
			Probe: cm.Probe{Path: "/api/v1/tasks"}, Enforcing: &enforcing}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	is, err := cm.NewIssuer(cfg.Issuer, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &cm.Runner{Issuer: is, Cfg: cfg}
}

func result(r cm.PairResult, check string) cm.CheckResult {
	for _, c := range r.Checks {
		if c.Name == check {
			return c
		}
	}
	return cm.CheckResult{Name: check, Result: "missing"}
}

func TestEnforcingTargetProvesAllChecks(t *testing.T) {
	iss := mockIssuer(t)
	tgt := target(t, iss.URL, auth.ModeEnforce, true)
	rs := runner(t, iss, tgt, true, 5).Run(context.Background(), nil)
	if !cm.AllPassed(rs) {
		t.Fatalf("expected a pass: %+v", rs)
	}
	for _, c := range []string{"mint", "accepted", "wrong_audience", "revoke", "revoked"} {
		if got := result(rs[0], c); got.Result != cm.Pass {
			t.Errorf("%s: %+v", c, got)
		}
	}
	if s := result(rs[0], "revoked").Seconds; s <= 0 || s > 5 {
		t.Errorf("revocation took %.2fs", s)
	}
}

func TestRevocationThatNeverTakesEffectFails(t *testing.T) {
	iss := mockIssuer(t)
	tgt := target(t, iss.URL, auth.ModeEnforce, false) // target ignores the deny-list
	rs := runner(t, iss, tgt, true, 1).Run(context.Background(), nil)
	if cm.AllPassed(rs) {
		t.Fatal("a target that never honors revocation passed")
	}
	if c := result(rs[0], "revoked"); c.Result != cm.Fail || !strings.Contains(c.Detail, "still answered 200") {
		t.Fatalf("revoked check: %+v", c)
	}
}

func TestNonEnforcingTargetIsReportedNotFailed(t *testing.T) {
	iss := mockIssuer(t)
	tgt := target(t, iss.URL, auth.ModeOff, true) // dial off: admits anyone
	rs := runner(t, iss, tgt, false, 5).Run(context.Background(), nil)
	if !cm.AllPassed(rs) {
		t.Fatalf("declared non-enforcing pair should pass its accepted check: %+v", rs)
	}
	if result(rs[0], "wrong_audience").Result != cm.Skip || result(rs[0], "revoked").Result != cm.Skip {
		t.Fatalf("checks not skipped: %+v", rs[0].Checks)
	}
	// Claiming enforcement on a target that is not enforcing must fail loudly.
	rs = runner(t, iss, tgt, true, 1).Run(context.Background(), nil)
	if c := result(rs[0], "wrong_audience"); c.Result != cm.Fail || !strings.Contains(c.Detail, "enforcing") {
		t.Fatalf("wrong_audience on an off target: %+v", c)
	}
}

func TestUnreachableTargetAndIssuerFailCleanly(t *testing.T) {
	iss := mockIssuer(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	rs := runner(t, iss, dead, true, 1).Run(context.Background(), nil)
	if c := result(rs[0], "accepted"); c.Result != cm.Fail || !strings.Contains(c.Detail, "probe error") {
		t.Fatalf("dead target: %+v", c)
	}
	downIssuer := httptest.NewServer(http.NotFoundHandler())
	downIssuer.Close()
	rs = runner(t, downIssuer, iss, true, 1).Run(context.Background(), nil)
	if c := result(rs[0], "mint"); c.Result != cm.Fail {
		t.Fatalf("dead issuer: %+v", c)
	}
}

func TestReportsNeverContainATokenOrCredential(t *testing.T) {
	iss := mockIssuer(t)
	tgt := target(t, iss.URL, auth.ModeEnforce, true)
	rs := runner(t, iss, tgt, true, 5).Run(context.Background(), nil)
	var table, js bytes.Buffer
	cm.WriteTable(&table, rs)
	if err := cm.WriteJSON(&js, rs); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{table.String(), js.String()} {
		if strings.Contains(out, "eyJ") || strings.Contains(out, "Bearer") {
			t.Fatalf("report leaks a token:\n%s", out)
		}
	}
	if !strings.Contains(table.String(), "1 of 1 pairs proved") {
		t.Fatalf("table: %s", table.String())
	}
}

func TestHTTPIssuerSendsCredentialFromFile(t *testing.T) {
	var gotAuth, gotKind string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/mint") {
			b := make([]byte, 512)
			n, _ := r.Body.Read(b)
			if strings.Contains(string(b[:n]), `"kind":"service"`) {
				gotKind = "service"
			}
			_, _ = w.Write([]byte(`{"token":"t","id":"i"}`))
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	cred := filepath.Join(t.TempDir(), "cred")
	if err := os.WriteFile(cred, []byte("op-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	is, err := cm.NewIssuer(cm.IssuerConfig{Type: "http", MintURL: srv.URL + "/mint", RevokeURL: srv.URL + "/revoke", CredentialFile: cred}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := is.Mint(context.Background(), cm.MintRequest{Name: "x", Audience: "a", Scopes: []string{"a.read"}})
	if err != nil || m.Token != "t" || m.ID != "i" || gotAuth != "Bearer op-secret" || gotKind != "service" {
		t.Fatalf("mint: %+v %v auth=%q kind=%q", m, err, gotAuth, gotKind)
	}
	if err := is.Revoke(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidation(t *testing.T) {
	good := `{"issuer":{"type":"mock","url":"http://127.0.0.1:9100"},"pairs":[{"name":"a","target":"http://x","audience":"a","scopes":["a.read"],"probe":{"path":"/p"}}]}`
	p := filepath.Join(t.TempDir(), "c.json")
	write := func(s string) { _ = os.WriteFile(p, []byte(s), 0o600) }
	write(good)
	c, err := cm.LoadConfig(p)
	if err != nil || c.RevokeBoundSeconds != 45 || c.Pairs[0].Probe.Method != "GET" || c.Pairs[0].Probe.OKStatus != 200 {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for name, bad := range map[string]string{
		"unknown field":  strings.Replace(good, `"pairs"`, `"bogus":1,"pairs"`, 1),
		"no pairs":       `{"issuer":{"type":"mock","url":"u"},"pairs":[]}`,
		"bad issuer":     strings.Replace(good, `"mock"`, `"nope"`, 1),
		"no scopes":      strings.Replace(good, `"scopes":["a.read"]`, `"scopes":[]`, 1),
		"same other aud": strings.Replace(good, `"probe"`, `"other_audience":"a","probe"`, 1),
		"duplicate":      strings.Replace(good, `"pairs":[`, `"pairs":[{"name":"a","target":"t","audience":"a","scopes":["a.read"],"probe":{"path":"/p"}},`, 1),
	} {
		write(bad)
		if _, err := cm.LoadConfig(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
