package fixturehost

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/hollis-labs/libs/agent/sdk"
	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/gate/gatetest"
	"github.com/hollis-labs/libs/mesh/core/hitl"
)

func (h *Host) Capabilities(_ context.Context, adapter gate.AdapterScope) (gate.Capabilities, error) {
	if adapter != sdk.NativeAdapterScope() {
		return gate.Capabilities{}, gate.ErrUnsupported
	}
	return gate.Capabilities{Adapter: adapter, OperationClasses: []string{"tool"}, DecisionKinds: []gate.DecisionKind{gate.Allow, gate.Deny}, ScopeKinds: []gate.ScopeKind{gate.Once}, ObligationKinds: []string{"approval"}}, nil
}

func (h *Host) Request(ctx context.Context, request gate.Request) (gate.Ticket, error) {
	caps, err := h.Capabilities(ctx, request.Adapter)
	if err != nil {
		return gate.Ticket{}, err
	}
	if err = caps.ValidateRequest(request, clock()); err != nil {
		return gate.Ticket{}, err
	}
	if request.Environment == nil {
		return gate.Ticket{}, gate.ErrBinding
	}
	h.ledger.mu.Lock()
	err = h.verify(ctx, request.Binding, *request.Environment, nil)
	h.ledger.mu.Unlock()
	if err != nil {
		return gate.Ticket{}, err
	}
	snapshot, err := request.Snapshot()
	if err != nil {
		return gate.Ticket{}, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return gate.Ticket{}, err
	}
	handle, err := h.service.Enqueue(ctx, hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: "gate", IdempotencyKey: snapshot.IdempotencyKey, Source: hitl.SourceAssertion{ApplicationID: snapshot.EnforcementPoint, AgentID: string(snapshot.Binding.OperativeID)}, ExpiresAt: &snapshot.ExpiresAt, Extra: map[string]json.RawMessage{"gate_request": raw}})
	if err != nil {
		return gate.Ticket{}, err
	}
	digest, err := snapshot.Digest()
	return gate.Ticket{AuthorityRef: "fixture-hitl", Handle: handle, RequestDigest: digest, Binding: snapshot.Binding}, err
}

// project reads the actual HITL request snapshot and immutable terminal outcome.
// It does not treat a caller-supplied Decision or Participant as authentication.
func (h *Host) project(ctx context.Context, ticket gate.Ticket) (gate.Resolution, gate.Request, error) {
	view, err := h.service.Get(ctx, hitl.GetCommand{ContractVersion: hitl.ContractVersion, ItemID: ticket.Handle.ItemID, Caller: hitl.CallerAssertion{ApplicationID: "tradecraft/tool-admission"}})
	if err != nil {
		return gate.Resolution{}, gate.Request{}, err
	}
	var snapshot struct {
		Request gate.Request `json:"gate_request"`
	}
	if err = json.Unmarshal(view.Item.RequestSnapshot, &snapshot); err != nil {
		return gate.Resolution{}, gate.Request{}, err
	}
	digest, err := snapshot.Request.Digest()
	if err != nil {
		return gate.Resolution{}, gate.Request{}, err
	}
	if ticket.AuthorityRef != "fixture-hitl" || ticket.RequestDigest != digest || ticket.Binding != snapshot.Request.Binding {
		return gate.Resolution{}, gate.Request{}, gate.ErrBinding
	}
	result := gate.Resolution{State: gate.Pending, Ticket: ticket, Outcome: view.Item.TerminalOutcome}
	switch outcome := view.Item.TerminalOutcome.(type) {
	case hitl.Resolved:
		for _, option := range snapshot.Request.Options {
			if option.ID == outcome.Resolution.Response.Decision {
				result.State = gate.Decided
				result.Decision = &gate.Decision{Ticket: ticket, Resolution: outcome.Resolution, OptionID: option.ID, Scope: option.Scope, OperationDigest: snapshot.Request.OperationDigest, AuthorityRef: "fixture-authority", ExpiresAt: option.Scope.ExpiresAt}
				return result, snapshot.Request, nil
			}
		}
		return gate.Resolution{}, gate.Request{}, gate.ErrUnsupported
	case hitl.Canceled:
		result.State = gate.Cancelled
	case hitl.Expired:
		result.State = gate.Expired
	}
	return result, snapshot.Request, nil
}

func (h *Host) Await(ctx context.Context, ticket gate.Ticket, wait gate.WaitOptions) (gate.Resolution, error) {
	if err := wait.Validate(); err != nil {
		return gate.Resolution{}, err
	}
	current, request, err := h.project(ctx, ticket)
	if err != nil {
		return gate.Resolution{}, err
	}
	if current.State == gate.Pending && h.mode != "pending" {
		kind := gate.Allow
		if h.mode == "deny" {
			kind = gate.Deny
		}
		option := ""
		for _, offered := range request.Options {
			if offered.Kind == kind && offered.Scope.Kind == gate.Once {
				option = offered.ID
				break
			}
		}
		if option == "" {
			return gate.Resolution{}, gate.ErrUnsupported
		}
		_, err = h.service.Respond(ctx, hitl.RespondCommand{ItemID: ticket.Handle.ItemID, Response: hitl.Response{Kind: "gate", Decision: option}, Participant: hitl.Participant{PrincipalRef: "fixture-responder", Assurance: "closed-synthetic-universe"}})
		if err != nil {
			return gate.Resolution{}, err
		}
	}
	current, _, err = h.project(ctx, ticket)
	return current, err
}

func (h *Host) Cancel(ctx context.Context, ticket gate.Ticket, cause hitl.CancelCause) (gate.Resolution, error) {
	if cause != hitl.CauseCallerWithdrawn {
		return gate.Resolution{}, gate.ErrUnsupported
	}
	if _, _, err := h.project(ctx, ticket); err != nil {
		return gate.Resolution{}, err
	}
	_, err := h.service.Withdraw(ctx, hitl.WithdrawCommand{ContractVersion: hitl.ContractVersion, ItemID: ticket.Handle.ItemID, Caller: hitl.CallerAssertion{ApplicationID: "tradecraft/tool-admission"}})
	if err != nil {
		return gate.Resolution{}, err
	}
	result, _, err := h.project(ctx, ticket)
	return result, err
}

type authorityPort struct {
	h      *Host
	locked bool
}

func (p authorityPort) Verify(ctx context.Context, request gate.Request, decision gate.Decision) error {
	if !p.locked {
		p.h.ledger.mu.Lock()
		defer p.h.ledger.mu.Unlock()
	}
	if request.Environment == nil {
		return gate.ErrBinding
	}
	if err := p.h.verify(ctx, request.Binding, *request.Environment, nil); err != nil {
		return err
	}
	actual, stored, err := p.h.project(ctx, decision.Ticket)
	if err != nil {
		return err
	}
	digest, err := request.Digest()
	if err != nil {
		return err
	}
	storedDigest, err := stored.Digest()
	if err != nil {
		return err
	}
	if digest != storedDigest || actual.Decision == nil || !reflect.DeepEqual(*actual.Decision, decision) || decision.Resolution.Participant.PrincipalRef != "fixture-responder" || decision.Resolution.Participant.Assurance != "closed-synthetic-universe" {
		return gate.ErrAuthority
	}
	if request.EnforcementPoint != "tradecraft/tool-admission" || request.Policy != gatetest.Reference("fixture-policy") || !reflect.DeepEqual(request.Obligations, []gate.ObligationRef{{Kind: "approval", Ref: gatetest.Reference("fixture-approval")}}) {
		return gate.ErrAuthority
	}
	return nil
}
