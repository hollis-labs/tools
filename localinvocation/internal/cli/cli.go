package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/tools/localinvocation/entry"
	"github.com/hollis-labs/tools/localinvocation/invocation"
	"github.com/hollis-labs/tools/localinvocation/runner"
)

// Execute records preparation before calling the runner. The runner owns
// streams, cancellation, PTY and outcome classification.
func Execute(ctx context.Context, cmd invocation.Command, record invocation.Record, store string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if cmd.EnvironmentRef != "" {
		return 1, errors.New("standalone CLI has no environment resolver for this reference")
	}
	if err := (invocation.FileRecorder{Path: store}).Append(record); err != nil {
		return 1, err
	}
	// StoreEnv is a path, not a credential. Wrapped descendants use the same
	// journal even when this command selected it with --store.
	if err := os.Setenv(invocation.StoreEnv, store); err != nil {
		return 1, err
	}
	manager, err := runner.New(filepath.Join(filepath.Dir(store), "runs"), nil)
	if err != nil {
		return 1, err
	}
	id, err := manager.StartWithInput(ctx, cmd, runner.Options{}, stdin)
	if err != nil {
		return 1, err
	}
	defer manager.Cancel(id) // no-op after completion; prevents orphaning on output/read failure
	var outCursor, errCursor int64
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	cancelled := false
	for {
		result, err := manager.Result(id)
		if err != nil {
			return 1, err
		}
		if _, ok := result.OutputRef["stdout"]; ok {
			if outCursor, err = drain(manager, id, "stdout", outCursor, stdout); err != nil {
				return 1, err
			}
		}
		if _, ok := result.OutputRef["stderr"]; ok {
			if errCursor, err = drain(manager, id, "stderr", errCursor, stderr); err != nil {
				return 1, err
			}
		}
		if result.Done {
			if result.Outcome.Kind == runner.OutcomeExit && result.Outcome.ExitCode != nil {
				return *result.Outcome.ExitCode, nil
			}
			return 1, fmt.Errorf("command outcome %s: %s", result.Outcome.Kind, result.Outcome.Error)
		}
		if cancelled {
			<-tick.C
			continue
		}
		select {
		case <-ctx.Done():
			_ = manager.Cancel(id)
			cancelled = true
		case <-tick.C:
		}
	}
}

func drain(manager *runner.Manager, id, stream string, cursor int64, dest io.Writer) (int64, error) {
	for {
		page, err := manager.Observe(id, stream, cursor, runner.DefaultReadLimit)
		if err != nil {
			return cursor, err
		}
		if len(page.Data) != 0 {
			if _, err := dest.Write(page.Data); err != nil {
				return cursor, err
			}
		}
		cursor = page.NextCursor
		if !page.Truncated {
			return cursor, nil
		}
	}
}

func RecordHook(input []byte, settings entry.Settings) error {
	_, record, err := entry.Hook(input, settings.Options)
	if err != nil {
		return err
	}
	return (invocation.FileRecorder{Path: settings.Store}).Append(record)
}
