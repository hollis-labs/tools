package service

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/conduit/internal/proc"
	"github.com/hollis-labs/conduit/internal/safe"
	"github.com/hollis-labs/conduit/internal/store"
)

// ReapReport says what a reap did.
type ReapReport struct {
	Orphaned int // running runs whose owner died, marked failed
	Killed   int // orphaned process groups terminated
	Expired  int // approved or pending runs that outlived their window
	Skipped  int // stale heartbeat but the owner is still alive: left alone
}

// Reap cleans up after a crash. A run left "running" by an owner that died is
// marked failed (class orphaned), and its process group is terminated if, and
// only if, the group leader is verifiably the process that was started for it
// (pid and start time both match, so a reused pid is never signaled). A run is
// only touched when its heartbeat is stale AND its owner is gone; a live owner
// that is merely slow is left alone. Approved or pending runs older than the
// approval window expire.
func (s *Service) Reap(now time.Time) (ReapReport, error) { return s.ReapStale(now, s.staleAfter) }

// ReapStale is Reap with an explicit heartbeat staleness threshold.
func (s *Service) ReapStale(now time.Time, staleAfter time.Duration) (ReapReport, error) {
	var rep ReapReport
	running, err := s.store.Running()
	if err != nil {
		return rep, err
	}
	for _, r := range running {
		if r.Host != s.host { // pids are meaningful only on the machine that ran them
			continue
		}
		beat := r.HeartbeatAt
		if beat.IsZero() {
			beat = r.CreatedAt
		}
		if now.Sub(beat) < staleAfter {
			continue
		}
		if r.OwnerPID != 0 && proc.Alive(r.OwnerPID, r.OwnerStart) {
			rep.Skipped++
			continue
		}
		if r.PGID > 1 && proc.Alive(r.PGID, r.PGIDStart) {
			proc.KillGroup(r.PGID, 2*time.Second)
			rep.Killed++
		}
		moved, ferr := s.store.Fail(r.ID, store.StateRunning, "orphaned",
			"the conduit process running this command exited; the run was reaped", "", now)
		if ferr != nil {
			return rep, ferr
		}
		if !moved {
			continue // finished normally in the meantime
		}
		rep.Orphaned++
		_ = s.store.InsertAttempt(&store.Attempt{
			RunID: r.ID, N: len(r.Attempts) + 1, Started: beat, Ended: now, ExitCode: -1,
			Class: "orphaned", StartErr: "owner exited before the command finished",
		})
	}

	cutoff := now.Add(-s.approvalTTL)
	approved, err := s.store.ApprovedBefore(cutoff)
	if err != nil {
		return rep, err
	}
	for _, r := range approved {
		if moved, terr := s.store.Transition(r.ID, store.StateApproved, store.StateExpired); terr == nil && moved {
			_ = s.store.UpdateRun(r.ID, store.StateExpired, "", now)
			rep.Expired++
		}
	}
	pending, err := s.store.ListRuns(store.Filter{State: store.StatePending, Limit: 1000})
	if err != nil {
		return rep, err
	}
	for _, r := range pending {
		if r.CreatedAt.Before(cutoff) {
			if moved, terr := s.store.Transition(r.ID, store.StatePending, store.StateExpired); terr == nil && moved {
				_ = s.store.UpdateRun(r.ID, store.StateExpired, "", now)
				rep.Expired++
			}
		}
	}
	return rep, nil
}

func (r ReapReport) String() string {
	return fmt.Sprintf("%d orphaned run(s) failed, %d process group(s) killed, %d expired, %d left alone (owner alive)",
		r.Orphaned, r.Killed, r.Expired, r.Skipped)
}

// StartReaper reaps now and then every interval until ctx is done. It runs in a
// contained goroutine: a bug in the reaper cannot stop the daemon.
func (s *Service) StartReaper(ctx context.Context, every time.Duration, logf func(string, ...any)) {
	run := func() {
		rep, err := s.Reap(time.Now())
		switch {
		case err != nil && logf != nil:
			logf("conduit: reaper: %v", err)
		case (rep.Orphaned+rep.Killed+rep.Expired) > 0 && logf != nil:
			logf("conduit: reaper: %s", rep)
		}
	}
	safe.Go("reaper", func() {
		run()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// each pass is contained, so one panic does not end the loop
				_ = safe.Call("reaper pass", func() error { run(); return nil })
			}
		}
	}, nil)
}

// PanicRecord is a recovered panic as the operator sees it.
type PanicRecord struct {
	Ref   string
	At    time.Time
	Where string
	Value string
	Stack string
	RunID string
}

// Panics lists recovered panics, newest first.
func (s *Service) Panics(limit int) ([]PanicRecord, error) {
	rows, err := s.store.Panics(limit)
	if err != nil {
		return nil, err
	}
	out := make([]PanicRecord, len(rows))
	for i, r := range rows {
		out[i] = PanicRecord(r)
	}
	return out, nil
}

// PanicByRef returns one recovered panic.
func (s *Service) PanicByRef(ref string) (*PanicRecord, error) {
	r, err := s.store.PanicByRef(ref)
	if err != nil {
		return nil, err
	}
	p := PanicRecord(*r)
	return &p, nil
}

// RecordPanic persists a recovered panic (the composition root's reporter calls
// this). Value and Stack are already redacted and bounded by package safe.
func (s *Service) RecordPanic(r safe.Record) error {
	return s.store.RecordPanic(store.PanicRow{Ref: r.Ref, At: r.At, Where: r.Where, Value: r.Value, Stack: r.Stack, RunID: r.RunID})
}
