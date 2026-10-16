package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/conduit/internal/safe"
	"github.com/hollis-labs/conduit/internal/service"
)

// exitInternal is EX_SOFTWARE: conduit itself failed.
const exitInternal = 70

// runGuarded is the outermost boundary: a panic anywhere in start-up or wiring
// (before the CLI's own guard exists) still ends in a ref and a clean exit code,
// with deferred cleanup (store close, terminal restore) already run.
func runGuarded(ctx context.Context) (code int, err error) {
	defer func() {
		if v := recover(); v != nil {
			pe := safe.Capture("conduit start-up", v)
			fmt.Fprintf(os.Stderr, "conduit: internal error (ref %s). This is a bug in conduit.\n", pe.Rec.Ref)
			code, err = exitInternal, nil
		}
	}()
	return run(ctx)
}

// panicReporter logs each recovered panic and stores it for `conduit panics`.
type panicReporter struct {
	svc  *service.Service
	logf func(string, ...any)
}

func (p panicReporter) Report(r safe.Record) {
	p.logf("conduit: PANIC recovered %s in %s: %s  (conduit panics show %s)", r.Ref, r.Where, r.Value, r.Ref)
	if err := p.svc.RecordPanic(r); err != nil {
		p.logf("conduit: could not record panic %s: %v", r.Ref, err)
	}
}
