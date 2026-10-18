package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/conduit/internal/blob"
	"github.com/hollis-labs/conduit/internal/service"
	"github.com/hollis-labs/conduit/internal/store"
)

type getPanics struct{ blob.Store }

func (getPanics) Get(context.Context, string) ([]byte, error) {
	panic("blob read bug, token=sk-abcdefghijklmnopqrstuvwxyz")
}

func TestCLIPanicExitsWithEXSOFTWAREAndAPointerNotTheDetail(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blobReal, _ := blob.NewLocal(filepath.Join(t.TempDir(), "b"))
	good := service.New(st, service.WithBlobs(blobReal))
	run, _ := good.Run(context.Background(), service.RunRequest{Command: "echo hi"})

	var out, errb bytes.Buffer
	code := Main(context.Background(), service.New(st, service.WithBlobs(getPanics{blobReal})), []string{"recall", run.ID}, nil, &out, &errb, func() string { return "" })
	if code != ExitInternal || ExitInternal != 70 {
		t.Fatalf("exit %d", code)
	}
	msg := errb.String()
	if !strings.Contains(msg, "pnc_") || !strings.Contains(msg, "conduit panics show") || strings.Contains(msg, "sk-") || strings.Contains(msg, "blob read bug") {
		t.Errorf("stderr: %q", msg)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("terminal reader bug") }

// A panic in the key-reading goroutines must close the key stream (so the picker
// cancels and restores the terminal), not crash the process mid-draw.
func TestPickerKeyReaderPanicClosesTheStream(t *testing.T) {
	done := make(chan struct{})
	go func() {
		for range readKeys(panicReader{}) {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("key stream never closed after the reader panicked")
	}
}
