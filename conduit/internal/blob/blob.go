// Package blob is where captured command output lives. Output is immutable and
// content-addressed; a CommandRun holds refs, never bytes.
//
// Refs use caster's scheme (cas://sha256/<hex>) so a ref written by either
// implementation resolves with `caster get` when caster is the backend. The
// caster backend shells out to the caster CLI: that tool is owned elsewhere and
// its binary, not its Go packages, is the contract.
package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned for a ref that does not resolve.
var ErrNotFound = errors.New("blob not found")

// Meta travels with a blob when the backend can keep it.
type Meta struct {
	Mime   string // default text/plain
	Source string // provenance, e.g. conduit://run/<id>/stdout
	Tags   string
}

// Store keeps blobs.
type Store interface {
	Put(ctx context.Context, data []byte, m Meta) (ref string, err error)
	Get(ctx context.Context, ref string) ([]byte, error)
	Name() string
}

const refPrefix = "cas://sha256/"

// Hash returns the hex sha256 of data.
func Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Open picks a backend. kind is "caster", "local" or "" (caster when it is on
// PATH, else local under dir).
func Open(kind, dir string) (Store, error) {
	switch kind {
	case "local":
		return NewLocal(dir)
	case "caster":
		return NewCaster("")
	case "":
		if c, err := NewCaster(""); err == nil {
			return c, nil
		}
		return NewLocal(dir)
	}
	return nil, fmt.Errorf("unknown blob backend %q (want caster or local)", kind)
}

// Caster stores blobs through the caster CLI.
type Caster struct{ bin string }

// NewCaster finds the caster binary (bin "" = PATH lookup).
func NewCaster(bin string) (*Caster, error) {
	if bin == "" {
		p, err := exec.LookPath("caster")
		if err != nil {
			return nil, fmt.Errorf("caster not on PATH: %w", err)
		}
		bin = p
	}
	return &Caster{bin: bin}, nil
}

func (*Caster) Name() string { return "caster" }

func (c *Caster) Put(ctx context.Context, data []byte, m Meta) (string, error) {
	args := []string{"put", "-m", orDefault(m.Mime, "text/plain")}
	if m.Source != "" {
		args = append(args, "-s", m.Source)
	}
	if m.Tags != "" {
		args = append(args, "-t", m.Tags)
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, c.bin, args...) //nolint:gosec // c.bin is the caster binary found on PATH; args are fixed flags plus caller metadata
	cmd.Stdin = bytes.NewReader(data)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("caster put: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	ref := strings.TrimSpace(out.String())
	if !strings.HasPrefix(ref, refPrefix) {
		return "", fmt.Errorf("caster put: unexpected ref %q", ref)
	}
	return ref, nil
}

func (c *Caster) Get(ctx context.Context, ref string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin, "get", ref) //nolint:gosec // c.bin is the caster binary; ref is one argv word
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 2 {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("caster get %s: %w: %s", ref, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// Local is a plain on-disk sha256 store, the fallback when caster is absent.
type Local struct{ dir string }

// NewLocal opens (creating) a store rooted at dir.
func NewLocal(dir string) (*Local, error) {
	if dir == "" {
		return nil, errors.New("local blob store needs a directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Local{dir: dir}, nil
}

func (*Local) Name() string { return "local" }

func (l *Local) path(hash string) string { return filepath.Join(l.dir, hash[:2], hash) }

func (l *Local) Put(_ context.Context, data []byte, _ Meta) (string, error) {
	h := Hash(data)
	p := l.path(h)
	if _, err := os.Stat(p); err == nil {
		return refPrefix + h, nil // immutable: same bytes, same blob
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", err
	}
	return refPrefix + h, nil
}

func (l *Local) Get(_ context.Context, ref string) ([]byte, error) {
	h, ok := strings.CutPrefix(ref, refPrefix)
	if !ok || len(h) != 64 {
		return nil, fmt.Errorf("not a cas ref: %q", ref)
	}
	b, err := os.ReadFile(l.path(h))
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if Hash(b) != h {
		return nil, fmt.Errorf("blob %s failed verification", h)
	}
	return b, nil
}

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}
