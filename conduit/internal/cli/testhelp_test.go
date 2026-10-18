package cli

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hollis-labs/tools/conduit/internal/blob"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func newTestService(t *testing.T) *service.Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bl, _ := blob.NewLocal(filepath.Join(t.TempDir(), "b"))
	return service.New(st, service.WithBlobs(bl))
}
