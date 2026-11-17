package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/hollis-labs/libs/agent/sdk/gateconnector"
	"github.com/hollis-labs/libs/agent/sdk/transcript"
	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/gate/gatetest"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
)

// The fake port only supplies a deterministic decision. It issues no grant and
// runs no effect. Gate connector still exercises the opt-in request path.
type fakeGate struct {
	request gate.Request
	calls   *int
	now     time.Time
}

func (f fakeGate) Capabilities(_ context.Context, a gate.AdapterScope) (gate.Capabilities, error) {
	*f.calls++
	if a != f.request.Adapter {
		return gate.Capabilities{}, gate.ErrUnsupported
	}
	return gatetest.Capabilities(f.request), nil
}
func (f fakeGate) Request(_ context.Context, r gate.Request) (gate.Ticket, error) {
	*f.calls++
	if !reflect.DeepEqual(r, f.request) {
		return gate.Ticket{}, gate.ErrBinding
	}
	digest, err := r.Digest()
	if err != nil {
		return gate.Ticket{}, err
	}
	return gate.Ticket{AuthorityRef: "pilot-fake", Handle: hitl.Handle{ContractVersion: hitl.ContractVersion, ItemID: "pilot-interaction", State: hitl.StatePresented, Revision: 1}, RequestDigest: digest, Binding: r.Binding}, nil
}
func (f fakeGate) Await(_ context.Context, t gate.Ticket, _ gate.WaitOptions) (gate.Resolution, error) {
	*f.calls++
	option := f.request.Options[0]
	decision := hitl.ResolutionRecord{ResolutionID: "pilot-decision-1", Response: hitl.Response{Kind: "gate", Decision: option.ID}, Participant: hitl.Participant{PrincipalRef: "fake-human", Assurance: "verified"}, ResolvedAt: f.now, InteractionRevision: 2}
	return gate.Resolution{State: gate.Decided, Ticket: t, Outcome: hitl.Resolved{ItemID: t.Handle.ItemID, InteractionRevision: 2, Resolution: decision}, Decision: &gate.Decision{Ticket: t, Resolution: decision, OptionID: option.ID, Scope: option.Scope, OperationDigest: f.request.OperationDigest, AuthorityRef: "pilot-fake", ExpiresAt: f.request.ExpiresAt}}, nil
}
func (f fakeGate) Cancel(_ context.Context, _ gate.Ticket, _ hitl.CancelCause) (gate.Resolution, error) {
	*f.calls++
	return gate.Resolution{}, gate.ErrUnsupported
}

type gateRow struct {
	request gate.Request
	ticket  *gate.Ticket
}
type fakeGateStore struct{ row gateRow }

func (s *fakeGateStore) Reserve(_ context.Context, r gate.Request) error {
	if s.row.request.ContractVersion != "" && !reflect.DeepEqual(s.row.request, r) {
		return errors.New("gate request changed")
	}
	snap, err := r.Snapshot()
	if err != nil {
		return err
	}
	s.row.request = snap
	return nil
}
func (s *fakeGateStore) Attach(_ context.Context, key string, t gate.Ticket) error {
	if key != s.row.request.IdempotencyKey {
		return errors.New("gate reservation missing")
	}
	if s.row.ticket != nil && *s.row.ticket != t {
		return errors.New("gate ticket changed")
	}
	s.row.ticket = &t
	return nil
}
func (s *fakeGateStore) Load(_ context.Context, key string) (gate.Request, *gate.Ticket, error) {
	if key != s.row.request.IdempotencyKey {
		return gate.Request{}, nil, errors.New("gate reservation missing")
	}
	snap, err := s.row.request.Snapshot()
	if err != nil {
		return gate.Request{}, nil, err
	}
	return snap, s.row.ticket, nil
}

func reowner(ref *runtimecontract.Reference) {
	if ref != nil {
		ref.OwnerID = ownerID
	}
}

func (p *pilot) gateTurn(ctx context.Context, turnID string) (string, any, error) {
	if !p.gate {
		connector := gateconnector.Connector{}
		_, err := connector.Open(ctx, gate.Request{})
		if !errors.Is(err, gateconnector.ErrDisabled) {
			return "", nil, fmt.Errorf("disabled connector: %w", err)
		}
		return "Gatecrasher is off; no request was made.", nil, nil
	}
	now := time.Now().UTC()
	request := gatetest.Request(now)
	request.Binding = p.binding
	request.IdempotencyKey = turnID
	request.TurnID = runtimecontract.TurnID(turnID)
	request.ToolCallID = runtimecontract.ToolCallID("gate-" + turnID)
	if request.Environment != nil {
		reowner(&request.Environment.Receipt)
		reowner(&request.Environment.Workspace)
		reowner(&request.Environment.Lease)
		reowner(&request.Environment.Grants)
	}
	reowner(&request.Policy)
	for i := range request.ResourceVersions {
		reowner(&request.ResourceVersions[i])
	}
	for i := range request.Provenance {
		reowner(&request.Provenance[i])
	}
	for i := range request.Obligations {
		reowner(&request.Obligations[i].Ref)
	}
	port := fakeGate{request: request, calls: &p.gateCalls, now: now}
	store := &fakeGateStore{}
	connector := gateconnector.Connector{Enabled: true, Port: port, Store: store, Now: func() time.Time { return now }}
	ticket, err := connector.Open(ctx, request)
	if err != nil {
		return "", nil, err
	}
	resolution, err := connector.Reconcile(ctx, request.IdempotencyKey, request.Binding)
	if err != nil {
		return "", nil, err
	}
	if resolution.Decision == nil {
		return "", nil, errors.New("fake gate did not decide")
	}
	decisionID := resolution.Decision.Resolution.ResolutionID
	payload, _ := json.Marshal(map[string]any{"decision_id": decisionID, "ticket_id": ticket.Handle.ItemID, "state": resolution.State, "operation_digest": request.OperationDigest})
	record, err := transcript.EncodeSideband(transcript.SidebandRecord{Type: "system", Subtype: "gate_approval", UUID: turnID + "-gate", SessionID: string(conversationID), Timestamp: now, Payload: payload})
	if err != nil {
		return "", nil, err
	}
	if _, err := p.transcript.Append(record); err != nil {
		return "", nil, err
	}
	return "Fake approval recorded; no effect executed.", map[string]any{"kind": "gate", "ticket_id": ticket.Handle.ItemID, "ticket_revision": ticket.Handle.Revision, "decision_id": decisionID, "operation_digest": request.OperationDigest, "state": "approved", "option_id": resolution.Decision.OptionID}, nil
}
