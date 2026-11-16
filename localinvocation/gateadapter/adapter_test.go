package gateadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/gate/gatetest"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
	"github.com/hollis-labs/tools/localinvocation/gateadapter"
	"github.com/hollis-labs/tools/localinvocation/invocation"
	"github.com/hollis-labs/tools/localinvocation/runner"
)

type gateFake struct {
	request   gate.Request
	now       time.Time
	selected  string
	expired   bool
	cancelled bool
	calls     int
}

func (g *gateFake) Capabilities(_ context.Context, _ gate.AdapterScope) (gate.Capabilities, error) {
	g.calls++
	return gatetest.Capabilities(g.request), nil
}
func (g *gateFake) Request(_ context.Context, r gate.Request) (gate.Ticket, error) {
	g.calls++
	digest, err := r.Digest()
	return gate.Ticket{AuthorityRef: "fake-owner", Handle: hitl.Handle{ContractVersion: hitl.ContractVersion, ItemID: "item-1", State: hitl.StatePresented, Revision: 1}, RequestDigest: digest, Binding: r.Binding}, err
}
func (g *gateFake) Await(_ context.Context, ticket gate.Ticket, _ gate.WaitOptions) (gate.Resolution, error) {
	g.calls++
	if g.expired {
		return gate.Resolution{State: gate.Expired, Ticket: ticket, Outcome: hitl.Expired{ItemID: ticket.Handle.ItemID, InteractionRevision: 2, TerminatedAt: g.now}}, nil
	}
	if g.cancelled {
		return gate.Resolution{State: gate.Cancelled, Ticket: ticket, Outcome: hitl.Canceled{ItemID: ticket.Handle.ItemID, InteractionRevision: 2, Cause: hitl.CauseCallerWithdrawn, TerminatedAt: g.now}}, nil
	}
	if g.selected == "" {
		return gate.Resolution{State: gate.Pending, Ticket: ticket}, nil
	}
	for _, option := range g.request.Options {
		if option.ID == g.selected {
			record := hitl.ResolutionRecord{ResolutionID: "decision-1", Response: hitl.Response{Kind: "gate", Decision: option.ID}, Participant: hitl.Participant{PrincipalRef: "human-1", Assurance: "verified"}, ResolvedAt: g.now, InteractionRevision: 2}
			return gate.Resolution{State: gate.Decided, Ticket: ticket, Outcome: hitl.Resolved{ItemID: ticket.Handle.ItemID, InteractionRevision: 2, Resolution: record}, Decision: &gate.Decision{Ticket: ticket, Resolution: record, OptionID: option.ID, Scope: option.Scope, OperationDigest: g.request.OperationDigest, AuthorityRef: "fake-owner", ExpiresAt: option.Scope.ExpiresAt}}, nil
		}
	}
	return gate.Resolution{}, gate.ErrUnsupported
}
func (g *gateFake) Cancel(_ context.Context, ticket gate.Ticket, _ hitl.CancelCause) (gate.Resolution, error) {
	g.calls++
	g.cancelled = true
	return g.Await(context.Background(), ticket, gate.WaitOptions{})
}

type authorityFake struct{}

func (authorityFake) Verify(context.Context, gate.Request, gate.Decision) error { return nil }

type admissionFake struct {
	calls   int
	claimed bool
}

func (a *admissionFake) Admit(ctx context.Context, _ gate.Request, _ gate.Decision, start func(context.Context) (string, error)) (string, error) {
	a.calls++
	if a.claimed {
		return "", errors.New("claim already consumed")
	}
	a.claimed = true
	return start(ctx)
}

type starterFake struct {
	calls   int
	command invocation.Command
}

func (s *starterFake) Start(_ context.Context, command invocation.Command, _ runner.Options) (string, error) {
	s.calls++
	s.command = command
	return "run-1", nil
}

func setup(t *testing.T) (gateadapter.Adapter, *gateFake, *admissionFake) {
	t.Helper()
	now := time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC)
	g := &gateFake{now: now}
	a := &admissionFake{}
	adapter := gateadapter.Adapter{Enabled: true, Port: g, Authority: authorityFake{}, Admission: a, Now: func() time.Time { return now }}
	adapter.Factory = func(_ context.Context, command invocation.Command, digest runtimecontract.Digest) (gate.Request, error) {
		r := gatetest.Request(now)
		r.OperationID = command.IDs.AttemptID
		r.OperationDigest = digest
		r.IdempotencyKey = "cli:" + command.IDs.AttemptID
		id, _ := json.Marshal(command.IDs.AttemptID)
		r.NativeRequestID = gate.NativeRequestID(id)
		for i := range r.Options {
			r.Options[i].Scope.Target = command.IDs.AttemptID
		}
		g.request = r
		return r, nil
	}
	return adapter, g, a
}

func TestOffMakesNoGateCallsAndClearsForgedRefs(t *testing.T) {
	starter := &starterFake{}
	adapter := gateadapter.Adapter{}
	prepared, err := adapter.Prepare(context.Background(), invocation.Options{Argv: []string{"true"}, CWD: "/tmp", Environ: []string{}, Gate: &invocation.GateReferences{DecisionRef: "forged"}}, runner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Start(context.Background(), prepared, starter)
	if err != nil || starter.calls != 1 || result.Command.Gate != nil || result.Record.Gate != nil {
		t.Fatalf("off path: %+v %v", result, err)
	}
}

func TestPendingThenRestartAllowsExactPreparedCommand(t *testing.T) {
	adapter, port, admission := setup(t)
	starter := &starterFake{}
	prepared, err := adapter.Prepare(context.Background(), invocation.Options{Argv: []string{"sh", "-c", "printf ok"}, SourceForm: invocation.FormShellCommand, CWD: "/tmp", Environ: []string{}}, runner.Options{PTY: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Start(context.Background(), prepared, starter)
	if !errors.Is(err, gateadapter.ErrPending) || result.State != gate.Pending || starter.calls != 0 {
		t.Fatalf("pending executed: %+v %v", result, err)
	}
	// Persisted Prepared survives client replacement; the decision is reloaded
	// from the GatePort, never inferred from a prior UI state.
	raw, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	var recovered gateadapter.Prepared
	if err := json.Unmarshal(raw, &recovered); err != nil {
		t.Fatal(err)
	}
	port.selected = port.request.Options[0].ID
	reconciled, err := adapter.Reconcile(context.Background(), recovered)
	if err != nil || reconciled.State != gate.Decided {
		t.Fatalf("replacement failed to query GatePort: %+v %v", reconciled, err)
	}
	result, err = adapter.Start(context.Background(), recovered, starter)
	if err != nil || result.RunID != "run-1" || starter.calls != 1 || admission.calls != 1 {
		t.Fatalf("allow not admitted: %+v %v", result, err)
	}
	if result.Command.Gate == nil || result.Record.Gate == nil || result.Record.Gate.DecisionRef != "decision-1" || result.Record.Gate.ReceiptRef != "" || result.Record.Gate.PreparedOperationRef == "" {
		t.Fatalf("refs missing or invented: %+v", result.Record.Gate)
	}
	if _, err := adapter.Start(context.Background(), recovered, starter); err == nil || starter.calls != 1 {
		t.Fatalf("effect replayed: %v", err)
	}
}

func TestCancelAndStaleAttemptNeverStart(t *testing.T) {
	adapter, _, _ := setup(t)
	prepared, err := adapter.Prepare(context.Background(), invocation.Options{Argv: []string{"true"}, CWD: "/tmp", Environ: []string{}}, runner.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := adapter.Cancel(context.Background(), prepared)
	if err != nil || cancelled.State != gate.Cancelled {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	starter := &starterFake{}
	if _, err := adapter.Start(context.Background(), prepared, starter); err == nil || starter.calls != 0 {
		t.Fatalf("cancelled attempt started: %v", err)
	}
	stale := prepared
	stale.Command.IDs.AttemptID = "replacement-attempt"
	if _, err := adapter.Reconcile(context.Background(), stale); err == nil {
		t.Fatal("stale attempt reconciled")
	}
}

func TestDenyExpiryChangedDigestAndFlagChangeNeverStart(t *testing.T) {
	for _, state := range []string{"deny", "expired", "changed", "off"} {
		t.Run(state, func(t *testing.T) {
			adapter, port, _ := setup(t)
			starter := &starterFake{}
			prepared, err := adapter.Prepare(context.Background(), invocation.Options{Argv: []string{"true"}, CWD: "/tmp", Environ: []string{}}, runner.Options{})
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "deny":
				port.selected = port.request.Options[1].ID
			case "expired":
				port.expired = true
			case "changed":
				prepared.Command.Argv[0] = "false"
			case "off":
				adapter.Enabled = false
			}
			_, err = adapter.Start(context.Background(), prepared, starter)
			if err == nil || starter.calls != 0 {
				t.Fatalf("%s executed: %v", state, err)
			}
		})
	}
}

var _ gate.GatePort = (*gateFake)(nil)
var _ gate.AuthorityVerifier = authorityFake{}
var _ gateadapter.AdmissionPort = (*admissionFake)(nil)
var _ gateadapter.Starter = (*starterFake)(nil)
