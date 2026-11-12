package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

type fakeServer struct {
	*httptest.Server
	calls    atomic.Int32
	password string
	acted    []string
}

func newFake(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		var c struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&c)
		f.password = c.Password
		if c.Password != "right horse battery" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"invalid username or password"}`))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "conduit_session", Value: "ses_x", Path: "/"})
		_, _ = w.Write([]byte(`{}`))
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, err := r.Cookie("conduit_session"); err != nil || r.Header.Get("X-Requested-With") == "" {
				w.WriteHeader(401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/approvals", authed(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"pending":[{"id":"run_abcdef12","command":"rm -rf /tmp/x","effect":"write"},{"id":"run_zzzzzz99","command":"ls"}]}`))
	}))
	mux.HandleFunc("POST /api/runs/{id}/{verb}", authed(func(w http.ResponseWriter, r *http.Request) {
		f.acted = append(f.acted, r.PathValue("verb")+" "+r.PathValue("id"))
		_, _ = w.Write([]byte(`{"state":"approved"}`))
	}))
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func runRemote(t *testing.T, in *os.File, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	c := &cmd{ctx: context.Background(), in: in, out: &out, err: &errb}
	code := c.remote(args[0], args[1:])
	return code, out.String(), errb.String()
}

func withTerminal(t *testing.T, pw string) *os.File {
	t.Helper()
	oldTTY, oldRead := isTTY, readPassword
	isTTY = func(*os.File) bool { return true }
	readPassword = func(*os.File) ([]byte, error) { return []byte(pw), nil }
	t.Cleanup(func() { isTTY, readPassword = oldTTY, oldRead })
	return os.Stdin
}

// A stdin that is not a terminal is refused before any request is made: the password has no other way in.
func TestApproveRefusesWhenStdinIsNotATerminal(t *testing.T) {
	f := newFake(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	_, _ = w.WriteString("right horse battery\n")
	code, _, stderr := runRemote(t, r, "approve", "--server", f.URL, "--user", "owner", "abcdef12")
	if code == ExitOK || !strings.Contains(stderr, "terminal") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
	if f.calls.Load() != 0 {
		t.Error("a request was made")
	}
	for _, a := range []string{"--password", "-password"} {
		if code, _, _ := runRemote(t, r, "approve", "--server", f.URL, "--user", "o", a, "x", "abcdef12"); code == ExitOK {
			t.Errorf("a %s flag was accepted", a)
		}
	}
}

func TestApproveAndRejectSignInAsAPerson(t *testing.T) {
	f := newFake(t)
	in := withTerminal(t, "right horse battery")
	code, stdout, stderr := runRemote(t, in, "approve", "--server", f.URL, "--user", "owner", "abcdef12")
	if code != ExitOK || !strings.Contains(stdout, "run_abcdef12: approved") {
		t.Fatalf("code %d, out %q, err %q", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "right horse") {
		t.Error("the password was echoed")
	}
	if code, _, _ := runRemote(t, in, "reject", "--server", f.URL, "--user", "owner", "run_zzzzzz99"); code != ExitOK {
		t.Fatalf("reject exit %d", code)
	}
	if got := strings.Join(f.acted, ","); got != "approve run_abcdef12,reject run_zzzzzz99" {
		t.Errorf("acted: %s", got)
	}
}

func TestApproveWrongPasswordAndUnknownRun(t *testing.T) {
	f := newFake(t)
	in := withTerminal(t, "nope")
	if code, _, stderr := runRemote(t, in, "approve", "--server", f.URL, "--user", "owner", "abcdef12"); code == ExitOK || !strings.Contains(stderr, "sign in failed") {
		t.Fatalf("code %d, %q", code, stderr)
	}
	in = withTerminal(t, "right horse battery")
	if code, _, _ := runRemote(t, in, "approve", "--server", f.URL, "--user", "owner", "missing"); code != ExitNotFound {
		t.Errorf("unknown run: exit %d", code)
	}
	if len(f.acted) != 0 {
		t.Errorf("acted: %v", f.acted)
	}
}

func TestServerURLRefusesPlainHTTPBeyondLoopback(t *testing.T) {
	for _, u := range []string{"http://example.com", "http://192.0.2.7:7421", "ftp://127.0.0.1", "127.0.0.1:7421"} {
		if _, err := serverURL(u); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	for _, u := range []string{"http://127.0.0.1:7421/", "http://localhost:1", "https://conduit.example.org"} {
		if _, err := serverURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}
