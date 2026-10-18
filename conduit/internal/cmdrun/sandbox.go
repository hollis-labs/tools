package cmdrun

import (
	"fmt"
	"os"
	"os/exec"
	"slices"

	"github.com/hollis-labs/go-sandbox/sandbox"
)

// Sandbox profile names. Rank orders them by how much they confine: a policy
// obligation can only be met or exceeded, never weakened by a request.
const (
	SandboxNone         = "none"
	SandboxProtect      = "protect"       // whole host, conduit's own state read-only
	SandboxWorkspaceNet = "workspace-net" // system read-only, cwd writable, network
	SandboxWorkspace    = "workspace"     // system read-only, cwd writable, no network
	SandboxRO           = "ro"            // system and cwd read-only, no network
)

var sandboxRank = map[string]int{"": 0, SandboxNone: 0, SandboxProtect: 1, SandboxWorkspaceNet: 2, SandboxWorkspace: 3, SandboxRO: 4}

// ValidSandbox reports whether name is a known profile.
func ValidSandbox(name string) bool { _, ok := sandboxRank[name]; return ok }

// Stricter returns the more confining of two profile names.
func Stricter(a, b string) string {
	if sandboxRank[b] > sandboxRank[a] {
		return b
	}
	return a
}

// profile builds the go-sandbox profile for name. protect lists conduit's own
// control-plane paths; only those that exist can be protected.
func profile(name string, protect []string) (sandbox.Profile, error) {
	switch name {
	case SandboxRO:
		// go-sandbox always binds its workspace writable and refuses to protect the
		// workspace root. So applySandbox hands it a private scratch directory as
		// the workspace and grants the real working directory as a read-only path.
		return sandbox.Profile{ID: name, Subprocess: true}, nil
	case SandboxWorkspace:
		return sandbox.Profile{ID: name, FS: sandbox.FSSpec{Read: []string{"workspace"}, Write: []string{"workspace"}}, Subprocess: true}, nil
	case SandboxWorkspaceNet:
		return sandbox.Profile{ID: name, FS: sandbox.FSSpec{Read: []string{"workspace"}, Write: []string{"workspace"}}, Net: true, Subprocess: true}, nil
	case SandboxProtect:
		var existing []string
		for _, p := range protect {
			if _, err := os.Stat(p); err == nil {
				existing = append(existing, p)
			}
		}
		slices.Sort(existing)
		return sandbox.Profile{ID: name, HostFilesystem: true, Net: true, Subprocess: true, FS: sandbox.FSSpec{Protect: existing}}, nil
	}
	return sandbox.Profile{}, fmt.Errorf("unknown sandbox profile %q", name)
}

// applySandbox wraps cmd in the named profile. An unknown profile, a missing
// backend or a refusal is an error: a required sandbox never degrades to
// running unconfined.
func applySandbox(cmd *exec.Cmd, name, workspace string, protect []string) (cleanup func(), err error) {
	p, err := profile(name, protect)
	if err != nil {
		return nil, err
	}
	if name != SandboxRO {
		return sandbox.Apply(cmd, p, workspace)
	}
	scratch, err := os.MkdirTemp("", "conduit-ro-")
	if err != nil {
		return nil, err
	}
	p.FS.Read = []string{workspace}
	inner, err := sandbox.Apply(cmd, p, scratch)
	if err != nil {
		_ = os.RemoveAll(scratch)
		return nil, err
	}
	return func() {
		if inner != nil {
			inner()
		}
		_ = os.RemoveAll(scratch)
	}, nil
}
