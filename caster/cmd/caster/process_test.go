package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The real binary on a throwaway data directory: the offline bootstrap, sign-in, a write, a read, a
// refused request and SIGTERM, the way the operator will run it.
func TestRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "caster")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil { //nolint:gosec // builds this package
		t.Fatalf("build: %v\n%s", err, out)
	}
	cfg := filepath.Join(dir, "substrate.test.yaml")
	if err := os.WriteFile(cfg, []byte("httphost:\n  addr: \"127.0.0.1:0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"XDG_DATA_HOME="+filepath.Join(dir, "data"), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"),
		"XDG_STATE_HOME="+filepath.Join(dir, "state"), "XDG_CACHE_HOME="+filepath.Join(dir, "cache"),
		"SUBSTRATE_CONFIG="+cfg)
	runCLI := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...) //nolint:gosec // the binary this test just built
		cmd.Env, cmd.Stdin = env, strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("caster %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	const password = "a long throwaway passphrase"
	runCLI(password+"\n", "identity", "setup", "--username", "owner")
	keyFile := filepath.Join(dir, "agent.key")
	runCLI("", "identity", "key", "create", "--name", "agent", "--scope", "caster.read,caster.write", "--out", keyFile)
	keyBytes, err := os.ReadFile(keyFile) //nolint:gosec // a path in the test's temp directory
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(keyBytes))

	srv := exec.Command(bin, "serve") //nolint:gosec // the binary this test just built
	srv.Env = env
	stderr, err := srv.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- srv.Wait() }()
	t.Cleanup(func() { _ = srv.Process.Kill() })

	addrRe := regexp.MustCompile(`addr=(\S+)`)
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if m := addrRe.FindStringSubmatch(sc.Text()); m != nil && strings.Contains(sc.Text(), "listening") {
				found <- m[1]
				break
			}
		}
		_, _ = io.Copy(io.Discard, stderr)
	}()
	var base string
	select {
	case a := <-found:
		base = "http://" + a
	case <-time.After(20 * time.Second):
		t.Fatal("the server did not report its address")
	}

	call := func(method, path string, hdr map[string]string, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), method, base+path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if hostOverride, ok := hdr["Host"]; ok {
			req.Host = hostOverride
		}
		r, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			t.Fatal(doErr)
		}
		defer func() { _ = r.Body.Close() }()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	bearer := map[string]string{"Authorization": "Bearer " + key, "Content-Type": "application/json"}

	if c, b := call("GET", "/api/setup", nil, ""); c != 200 || !strings.Contains(b, `"setup_required":false`) {
		t.Fatalf("setup must be closed after the offline bootstrap: %d %s", c, b)
	}
	if c, _ := call("GET", "/api/list", nil, ""); c != 401 {
		t.Fatalf("no credential must be 401, got %d", c)
	}
	if c, b := call("POST", "/api/put", bearer, `{"name":"e2e/note","content":"hello walrus"}`); c != 200 || !strings.Contains(b, "caster://e2e/note@1") {
		t.Fatalf("put with a key: %d %s", c, b)
	}
	if c, b := call("GET", "/api/get?ref=e2e/note", bearer, ""); c != 200 || !strings.Contains(b, `"state":"resolved"`) {
		t.Fatalf("get with a key: %d %s", c, b)
	}
	if c, b := call("GET", "/api/report", bearer, ""); c != 403 {
		t.Fatalf("a key without caster.admin must be refused: %d %s", c, b)
	}
	if c, b := call("GET", "/api/search?q=%22bad", bearer, ""); c != 400 || !strings.Contains(b, `"code":"invalid"`) {
		t.Fatalf("a malformed query is a 400: %d %s", c, b)
	}
	if c, b := call("GET", "/api/list", map[string]string{"Authorization": bearer["Authorization"], "Host": "evil.test"}, ""); c != 403 || !strings.Contains(b, "Invalid host") {
		t.Fatalf("an unknown Host must be refused: %d %s", c, b)
	}

	// Sign-in with the owner's password: the cookie authenticates reads, a cookie write needs the header.
	login, _ := json.Marshal(map[string]string{"username": "owner", "password": password})
	jar := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", base+"/api/login", bytes.NewReader(login))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "caster")
	req.Header.Set("Origin", base)
	resp, err := jar.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(resp.Cookies()) == 0 {
		t.Fatalf("sign-in: %d, cookies %d", resp.StatusCode, len(resp.Cookies()))
	}
	cookie := resp.Cookies()[0]
	req, _ = http.NewRequestWithContext(context.Background(), "POST", base+"/api/put", strings.NewReader(`{"name":"e2e/cookie","content":"x"}`))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, err = jar.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("a cookie write without X-Requested-With must be refused, got %d", resp.StatusCode)
	}

	if err = srv.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-exited:
		if err != nil {
			t.Fatalf("SIGTERM must end the server cleanly, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the server ignored SIGTERM")
	}
}
