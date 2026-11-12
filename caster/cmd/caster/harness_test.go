package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// binary builds the caster binary once per test run.
func binary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "caster-bin-")
		if err != nil {
			buildErr = err
			return
		}
		builtBin = filepath.Join(dir, "caster")
		if out, err := exec.Command("go", "build", "-o", builtBin, ".").CombinedOutput(); err != nil { //nolint:gosec // builds this package
			buildErr = &buildFailure{err, string(out)}
		}
	})
	if buildErr != nil {
		t.Fatalf("build: %v", buildErr)
	}
	return builtBin
}

type buildFailure struct {
	err error
	out string
}

func (b *buildFailure) Error() string { return b.err.Error() + "\n" + b.out }

// site is one throwaway installation: its own XDG directories and a config that binds port 0.
type site struct {
	t   *testing.T
	bin string
	dir string
	env []string
}

func newSite(t *testing.T) *site {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "substrate.test.yaml")
	if err := os.WriteFile(cfg, []byte("httphost:\n  addr: \"127.0.0.1:0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &site{t: t, bin: binary(t), dir: dir, env: []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir,
		"XDG_DATA_HOME=" + filepath.Join(dir, "data"), "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"XDG_STATE_HOME=" + filepath.Join(dir, "state"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
		"SUBSTRATE_CONFIG=" + cfg,
	}}
}

// run runs the binary to completion and returns stdout, stderr and the exit code.
func (s *site) run(stdin string, extraEnv []string, args ...string) (stdout, stderr string, code int) {
	s.t.Helper()
	cmd := exec.Command(s.bin, args...) //nolint:gosec // the binary this test just built
	cmd.Env = append(append([]string{}, s.env...), extraEnv...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns the concrete type
		code = ee.ExitCode()
	} else if err != nil {
		s.t.Fatalf("caster %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

// dataFiles lists every file under the data directory, relative to it.
func (s *site) dataFiles() []string {
	var out []string
	root := filepath.Join(s.dir, "data")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// bootstrap does the offline identity setup and returns a key file with the given scopes.
func (s *site) bootstrap(scopes string) (key string) {
	s.t.Helper()
	if _, errs, code := s.run("a long throwaway passphrase\n", nil, "identity", "setup", "--username", "owner"); code != 0 {
		s.t.Fatalf("identity setup: %s", errs)
	}
	return s.newKey("agent", scopes)
}

func (s *site) newKey(name, scopes string) string {
	s.t.Helper()
	file := filepath.Join(s.dir, name+".key")
	if _, errs, code := s.run("", nil, "identity", "key", "create", "--name", name, "--scope", scopes, "--out", file); code != 0 {
		s.t.Fatalf("key create: %s", errs)
	}
	b, err := os.ReadFile(file) //nolint:gosec // a path in the test's temp directory
	if err != nil {
		s.t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// serve starts `caster serve` and returns its base URL; the process is stopped with SIGTERM at cleanup.
func (s *site) serve() (base string, stop func() error) {
	s.t.Helper()
	srv := exec.Command(s.bin, "serve") //nolint:gosec // the binary this test just built
	srv.Env = s.env
	stderr, err := srv.StderrPipe()
	if err != nil {
		s.t.Fatal(err)
	}
	if err = srv.Start(); err != nil {
		s.t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- srv.Wait() }()
	stopped := false
	stop = func() error {
		if stopped {
			return nil
		}
		stopped = true
		_ = srv.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-exited:
			return err
		case <-time.After(15 * time.Second):
			_ = srv.Process.Kill()
			return context.DeadlineExceeded
		}
	}
	s.t.Cleanup(func() { _ = stop() })
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
	select {
	case a := <-found:
		return "http://" + a, stop
	case <-time.After(20 * time.Second):
		s.t.Fatal("the server did not report its address")
	}
	return "", stop
}

// httpDo performs one request and returns the status and body.
func httpDo(t *testing.T, method, url, bearer, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
