package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/security/redact"
)

// exitInternal is EX_SOFTWARE: conduit itself failed.
const exitInternal = panics.ExitSoftware

// runGuarded is the outermost boundary: a panic anywhere in start-up or wiring
// (before the CLI's own guard exists) still ends in a ref and a clean exit code,
// with deferred cleanup (store close, terminal restore) already run.
func runGuarded(ctx context.Context) (code int, err error) {
	// Until the store is open there is nowhere to persist a panic, but its text is
	// still untrusted: redact it before it reaches standard error.
	panics.SetDefault(panics.Config{Redact: redact.Default().Text})
	defer func() {
		if v := recover(); v != nil {
			pe := panics.Capture("conduit start-up", v)
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

func (p panicReporter) Report(r panics.Record) {
	p.logf("conduit: PANIC recovered %s in %s: %s  (conduit panics show %s)", r.Ref, r.Where, r.Value, r.Ref)
	if err := p.svc.RecordPanic(r); err != nil {
		p.logf("conduit: could not record panic %s: %v", r.Ref, err)
	}
}
