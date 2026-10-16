package cmdrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needBwrap(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	// unprivileged user namespaces may be disabled (CI containers)
	if err := exec.Command("bwrap", "--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib64", "/lib64", "true").Run(); err != nil {
		t.Skipf("bwrap cannot run here: %v", err)
	}
}

func runIn(t *testing.T, dir, profile, command string, protect ...string) *Result {
	t.Helper()
	r, err := Run(context.Background(), Spec{Command: command, Dir: dir, Sandbox: profile, Protect: protect, Env: os.Environ(), PassEnv: nil})
	if err != nil {
		t.Fatal(err)
	}
	// A sandbox that failed to apply also yields a non-zero exit and no side
	// effects; tests of confinement must prove it was applied, or they pass for
	// the wrong reason.
	if profile != "bogus" && (r.SandboxErr != "" || r.StartErr != "" || r.Sandbox != profile) {
		t.Fatalf("sandbox %q was not applied: sandboxErr=%q startErr=%q applied=%q", profile, r.SandboxErr, r.StartErr, r.Sandbox)
	}
	return r
}

func TestWorkspaceProfileWritesOnlyInWorkspace(t *testing.T) {
	needBwrap(t)
	ws := t.TempDir()
	outside := filepath.Join(os.Getenv("HOME"), ".conduit-sandbox-probe")
	defer func() { _ = os.Remove(outside) }() //nolint:gosec // test cleanup of a path built from HOME

	r := runIn(t, ws, SandboxWorkspace, `echo in > inside.txt && echo out > `+outside)
	if r.Sandbox != SandboxWorkspace {
		t.Fatalf("not sandboxed: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(ws, "inside.txt")); err != nil {
		t.Errorf("write inside workspace failed: %v (stderr %s)", err, r.Stderr)
	}
	if _, err := os.Stat(outside); err == nil { //nolint:gosec // probe path built from HOME in a test
		t.Errorf("write outside the workspace escaped the sandbox")
	}
	if r.ExitCode == 0 || !strings.Contains(string(r.Stderr), "No such file or directory") {
		t.Errorf("write outside workspace should fail because the path is not there: exit %d, stderr %q", r.ExitCode, r.Stderr)
	}
}

func TestWorkspaceProfileHidesHomeAndNetwork(t *testing.T) {
	needBwrap(t)
	ws := t.TempDir()
	r := runIn(t, ws, SandboxWorkspace, `ls "$HOME/.ssh" >/dev/null 2>&1; echo ssh=$?; (exec 3<>/dev/tcp/1.1.1.1/53) 2>/dev/null; echo net=$?`)
	out := string(r.Stdout)
	if !strings.Contains(out, "ssh=") || !strings.Contains(out, "net=") {
		t.Fatalf("probes did not run: %q (stderr %q)", out, r.Stderr)
	}
	if strings.Contains(out, "ssh=0") {
		t.Errorf("home is visible: %s", out)
	}
	if strings.Contains(out, "net=0") {
		t.Errorf("network reachable: %s", out)
	}
}

func TestROProfileCannotWriteWorkspace(t *testing.T) {
	needBwrap(t)
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "existing.txt"), []byte("data\n"), 0o600)
	r := runIn(t, ws, SandboxRO, `cat existing.txt; echo x > f`)
	if string(r.Stdout) != "data\n" {
		t.Errorf("ro profile must still read the working directory: %q", r.Stdout)
	}
	if r.ExitCode == 0 || !strings.Contains(string(r.Stderr), "Read-only file system") {
		t.Errorf("ro profile should refuse the write with EROFS: exit %d stderr %q", r.ExitCode, r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(ws, "f")); err == nil {
		t.Errorf("file created under ro")
	}
}

func TestProtectProfileKeepsControlPlaneReadOnly(t *testing.T) {
	needBwrap(t)
	ctl := t.TempDir()
	db := filepath.Join(ctl, "main.db")
	_ = os.WriteFile(db, []byte("orig"), 0o600)
	r := runIn(t, t.TempDir(), SandboxProtect, `echo tampered > `+db+`; cat `+db, ctl)
	if !strings.Contains(string(r.Stderr), "Read-only file system") {
		t.Errorf("protected write should fail with EROFS, stderr %q", r.Stderr)
	}
	b, _ := os.ReadFile(db) //nolint:gosec // test file in a temp dir
	if string(b) != "orig" {
		t.Errorf("protected file modified: %q (stderr %s)", b, r.Stderr)
	}
}

func TestUnknownProfileFailsClosed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	r := runIn(t, t.TempDir(), "bogus", `touch `+marker)
	if r.SandboxErr == "" || Classify(r) != ClassSandbox {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("command ran although its sandbox could not be applied")
	}
}

func TestStricter(t *testing.T) {
	if Stricter("workspace", "ro") != "ro" || Stricter("ro", "none") != "ro" || Stricter("", "protect") != "protect" {
		t.Error("ordering wrong")
	}
}
