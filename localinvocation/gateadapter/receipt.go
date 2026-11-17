package gateadapter

import (
	"github.com/hollis-labs/libs/mesh/core/gate"
)

// ReceiptRecord is an append-only invocation sideband keyed to the prepared
// attempt. It records later Gatecrasher receipt refs without rewriting the
// immutable preparation record or treating delivery as execution.
type ReceiptRecord struct {
	AttemptID            string              `json:"attempt_id"`
	PreparedOperationRef string              `json:"prepared_operation_ref"`
	DecisionRef          string              `json:"decision_ref"`
	DeliveryReceiptRef   string              `json:"delivery_receipt_ref,omitempty"`
	ExecutionReceiptRef  string              `json:"execution_receipt_ref,omitempty"`
	ExecutionState       gate.ExecutionState `json:"execution_state,omitempty"`
}

// ProjectReceipts checks receipt correlation. It is evidence only; it neither
// verifies approval authority nor reclaims an already used grant.
func ProjectReceipts(p Prepared, decision gate.Decision, delivery *gate.DeliveryReceipt, execution *gate.ExecutionReceipt) (ReceiptRecord, error) {
	if err := validPrepared(p); err != nil {
		return ReceiptRecord{}, err
	}
	if !p.GateRequired || p.Request == nil || p.Ticket == nil {
		return ReceiptRecord{}, ErrInvalidRecord
	}
	digest, err := Digest(p.Command, p.Runner)
	if err != nil {
		return ReceiptRecord{}, err
	}
	requestDigest, err := p.Request.Digest()
	if err != nil {
		return ReceiptRecord{}, err
	}
	if p.Request.OperationDigest != digest || p.Request.OperationID != p.Command.IDs.AttemptID || p.Ticket.RequestDigest != requestDigest || p.Ticket.Binding != p.Request.Binding || decision.Ticket != *p.Ticket || decision.OperationDigest != digest || decision.Resolution.ResolutionID == "" {
		return ReceiptRecord{}, gate.ErrBinding
	}
	found := false
	for _, option := range p.Request.Options {
		if option.ID == decision.OptionID && option.Scope == decision.Scope {
			found = true
			break
		}
	}
	if !found {
		return ReceiptRecord{}, gate.ErrBinding
	}
	out := ReceiptRecord{AttemptID: p.Command.IDs.AttemptID, PreparedOperationRef: digest.Scheme + ":" + digest.DescriptorVersion + ":" + digest.Value, DecisionRef: decision.Resolution.ResolutionID}
	if delivery != nil {
		if err := delivery.Validate(); err != nil {
			return ReceiptRecord{}, err
		}
		if delivery.DecisionRef != out.DecisionRef || delivery.Binding != p.Request.Binding || delivery.AdapterID != p.Request.Adapter.AdapterID || !delivery.NativeRequestID.Equal(p.Request.NativeRequestID) {
			return ReceiptRecord{}, gate.ErrBinding
		}
		out.DeliveryReceiptRef = delivery.EvidenceRef
	}
	if execution != nil {
		if err := execution.Validate(); err != nil {
			return ReceiptRecord{}, err
		}
		if execution.DecisionRef != out.DecisionRef || execution.Binding != p.Request.Binding || execution.OperationID != p.Request.OperationID {
			return ReceiptRecord{}, gate.ErrBinding
		}
		out.ExecutionReceiptRef, out.ExecutionState = execution.EvidenceRef, execution.State
	}
	return out, nil
}
