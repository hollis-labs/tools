package blob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalRoundTripAndDedup(t *testing.T) {
	ctx := context.Background()
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r1, _ := l.Put(ctx, []byte("hello\n"), Meta{})
	r2, _ := l.Put(ctx, []byte("hello\n"), Meta{})
	if r1 != r2 || r1 != "cas://sha256/5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03" {
		t.Fatalf("%s %s", r1, r2)
	}
	b, err := l.Get(ctx, r1)
	if err != nil || string(b) != "hello\n" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := l.Get(ctx, "cas://sha256/"+Hash([]byte("absent"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("want not found, got %v", err)
	}
}

func TestLocalDetectsCorruption(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	l, _ := NewLocal(dir)
	ref, _ := l.Put(ctx, []byte("good"), Meta{})
	h := ref[len(refPrefix):]
	os.WriteFile(filepath.Join(dir, h[:2], h), []byte("bad"), 0o600)
	if _, err := l.Get(ctx, ref); err == nil {
		t.Fatal("corruption not detected")
	}
}

// Both backends must agree on refs, so a run recorded under one reads under the other.
func TestCasterAgreesWithLocalRefs(t *testing.T) {
	c, err := NewCaster("")
	if err != nil {
		t.Skip("caster not installed")
	}
	// A developer may already have a local store or a remote profile. This
	// integration test must only write to its own temporary, local store.
	root := t.TempDir()
	for _, key := range []string{"XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	t.Setenv("CASTER_LOCAL", "1")
	t.Setenv("CASTER_DB_PATH", filepath.Join(root, "caster.db"))
	t.Setenv("CASTER_WORKSPACE", "default")
	t.Setenv("HOLLIS_OTEL_ENABLED", "false")
	ctx := context.Background()
	l, _ := NewLocal(t.TempDir())
	data := []byte("conduit blob test: ref agreement\n")
	cr, err := c.Put(ctx, data, Meta{Source: "conduit://test"})
	if err != nil {
		t.Fatal(err)
	}
	lr, _ := l.Put(ctx, data, Meta{})
	if cr != lr {
		t.Fatalf("caster %s != local %s", cr, lr)
	}
	b, err := c.Get(ctx, cr)
	if err != nil || string(b) != string(data) {
		t.Fatalf("%q %v", b, err)
	}
}
