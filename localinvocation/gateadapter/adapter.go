package gateadapter

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
	"github.com/hollis-labs/tools/localinvocation/invocation"
	"github.com/hollis-labs/tools/localinvocation/runner"
)

var (
	ErrPending       = errors.New("gateadapter: approval pending")
	ErrDisabled      = errors.New("gateadapter: prepared mode differs from current feature flag")
	ErrUnconfigured  = errors.New("gateadapter: enabled integration needs gate, authority, request factory and admission")
	ErrInvalidRecord = errors.New("gateadapter: prepared command and record differ")
)

// RequestFactory binds host-verified principal, policy, environment and native
// choices to the exact prepared command digest. It must not take those facts
// from display text. Prepare rejects a differently bound operation digest.
type RequestFactory func(context.Context, invocation.Command, runtimecontract.Digest) (gate.Request, error)

// AdmissionPort atomically claims a valid approval and fences current binding,
// policy, resources, digest and grant through effect start. It must call start
// synchronously exactly once after those checks; uncertain claims stay consumed
// until host reconciliation. This adapter's preflight does not replace it.
type AdmissionPort interface {
	Admit(context.Context, gate.Request, gate.Decision, func(context.Context) (string, error)) (string, error)
}

type Starter interface {
	Start(context.Context, invocation.Command, runner.Options) (string, error)
}

type Adapter struct {
	Enabled   bool
	Port      gate.GatePort
	Authority gate.AuthorityVerifier
	Factory   RequestFactory
	Admission AdmissionPort
	Now       func() time.Time
}

// Prepared is retained by the host while approval is pending. It includes the
// exact request and ticket in scoped storage; only refs reach routine records.
// GateRequired preserves the preparation mode across a flag change.
type Prepared struct {
	Command      invocation.Command `json:"command"`
	Record       invocation.Record  `json:"record"`
	Runner       runner.Options     `json:"runner"`
	GateRequired bool               `json:"gate_required"`
	Request      *gate.Request      `json:"request,omitempty"`
	Ticket       *gate.Ticket       `json:"ticket,omitempty"`
}

type StartResult struct {
	Command invocation.Command   `json:"command"`
	Record  invocation.Record    `json:"record"`
	Ticket  *gate.Ticket         `json:"ticket,omitempty"`
	State   gate.ResolutionState `json:"approval_state,omitempty"`
	RunID   string               `json:"run_id,omitempty"`
}

func (a Adapter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a Adapter) enabledReady() error {
	if a.Port == nil || a.Authority == nil || a.Factory == nil || a.Admission == nil {
		return ErrUnconfigured
	}
	return nil
}

func (a Adapter) Prepare(ctx context.Context, options invocation.Options, runOptions runner.Options) (Prepared, error) {
	// A caller cannot inject refs into either mode before the operation is gated.
	options.Gate = nil
	command, record, err := invocation.Prepare(options)
	if err != nil {
		return Prepared{}, err
	}
	p := Prepared{Command: command, Record: record, Runner: runOptions, GateRequired: a.Enabled}
	if !a.Enabled {
		return p, nil
	}
	if err := a.enabledReady(); err != nil {
		return Prepared{}, err
	}
	digest, err := Digest(command, runOptions)
	if err != nil {
		return Prepared{}, err
	}
	request, err := a.Factory(ctx, command, digest)
	if err != nil {
		return Prepared{}, err
	}
	if request.OperationDigest != digest || request.OperationID != command.IDs.AttemptID {
		return Prepared{}, gate.ErrBinding
	}
	request, err = request.Snapshot()
	if err != nil {
		return Prepared{}, err
	}
	caps, err := a.Port.Capabilities(ctx, request.Adapter)
	if err != nil {
		return Prepared{}, err
	}
	if err := caps.ValidateRequest(request, a.now()); err != nil {
		return Prepared{}, err
	}
	ticket, err := a.Port.Request(ctx, request)
	if err != nil {
		return Prepared{}, err
	}
	if err := ticket.Validate(); err != nil {
		return Prepared{}, err
	}
	requestDigest, err := request.Digest()
	if err != nil {
		return Prepared{}, err
	}
	if ticket.RequestDigest != requestDigest || ticket.Binding != request.Binding {
		return Prepared{}, gate.ErrBinding
	}
	p.Request, p.Ticket = &request, &ticket
	return p, nil
}

func validPrepared(p Prepared) error {
	if !reflect.DeepEqual(p.Command.Argv, p.Record.Argv) || p.Command.SourceForm != p.Record.SourceForm || p.Command.CWD != p.Record.CWD || p.Command.EnvironmentRef != p.Record.EnvironmentRef || p.Command.IDs != p.Record.IDs || p.Command.Gate != nil || p.Record.Gate != nil {
		return ErrInvalidRecord
	}
	_, err := Describe(p.Command, p.Runner)
	return err
}

func (a Adapter) retained(p Prepared) error {
	if !p.GateRequired || !a.Enabled {
		return ErrDisabled
	}
	if err := a.enabledReady(); err != nil {
		return err
	}
	if err := validPrepared(p); err != nil {
		return err
	}
	if p.Request == nil || p.Ticket == nil {
		return ErrInvalidRecord
	}
	digest, err := Digest(p.Command, p.Runner)
	if err != nil {
		return err
	}
	if p.Request.OperationDigest != digest || p.Request.OperationID != p.Command.IDs.AttemptID {
		return gate.ErrBinding
	}
	requestDigest, err := p.Request.Digest()
	if err != nil {
		return err
	}
	if err := p.Ticket.Validate(); err != nil {
		return err
	}
	if p.Ticket.RequestDigest != requestDigest || p.Ticket.Binding != p.Request.Binding {
		return gate.ErrBinding
	}
	return nil
}

// Reconcile reloads the authoritative interaction after a client replacement.
// Its result is observational; Start still validates and claims admission.
func (a Adapter) Reconcile(ctx context.Context, p Prepared) (gate.Resolution, error) {
	if err := a.retained(p); err != nil {
		return gate.Resolution{}, err
	}
	resolution, err := a.Port.Await(ctx, *p.Ticket, gate.WaitOptions{MaxWait: 0})
	if err != nil {
		return gate.Resolution{}, err
	}
	if err := resolution.Validate(); err != nil {
		return gate.Resolution{}, err
	}
	if resolution.Ticket != *p.Ticket {
		return gate.Resolution{}, gate.ErrBinding
	}
	return resolution, nil
}

// Cancel explicitly withdraws this exact interaction. Cancelling a wait
// context does not withdraw it.
func (a Adapter) Cancel(ctx context.Context, p Prepared) (gate.Resolution, error) {
	if err := a.retained(p); err != nil {
		return gate.Resolution{}, err
	}
	resolution, err := a.Port.Cancel(ctx, *p.Ticket, hitl.CauseCallerWithdrawn)
	if err != nil {
		return gate.Resolution{}, err
	}
	if err := resolution.Validate(); err != nil {
		return gate.Resolution{}, err
	}
	if resolution.Ticket != *p.Ticket {
		return gate.Resolution{}, gate.ErrBinding
	}
	return resolution, nil
}

// Start checks the authoritative GatePort state, then delegates the final
// fenced claim and physical process start to the host. A pending, denied,
// cancelled, expired or unknown interaction never calls Starter.
// Repeating Start after a crash requires the same persisted Prepared record;
// the admission ledger must refuse uncertain or already consumed attempts.
func (a Adapter) Start(ctx context.Context, p Prepared, starter Starter) (StartResult, error) {
	if p.GateRequired != a.Enabled {
		return StartResult{}, ErrDisabled
	}
	if starter == nil {
		return StartResult{}, errors.New("gateadapter: starter required")
	}
	if err := validPrepared(p); err != nil {
		return StartResult{}, err
	}
	out := StartResult{Command: p.Command, Record: p.Record, Ticket: p.Ticket}
	if !a.Enabled {
		if p.Request != nil || p.Ticket != nil {
			return StartResult{}, ErrInvalidRecord
		}
		id, err := starter.Start(ctx, p.Command, p.Runner)
		out.RunID = id
		return out, err
	}
	if err := a.retained(p); err != nil {
		return StartResult{}, err
	}
	digest := p.Request.OperationDigest
	resolution, err := a.Reconcile(ctx, p)
	if err != nil {
		return out, err
	}
	out.State = resolution.State
	switch resolution.State {
	case gate.Pending:
		return out, ErrPending
	case gate.Expired:
		return out, gate.ErrExpired
	case gate.Cancelled:
		return out, gate.ErrDenied
	case gate.Decided:
	default:
		return out, gate.ErrInvalid
	}
	decision := *resolution.Decision
	if err := gate.ValidateAdmission(ctx, *p.Request, decision, gate.Admission{Binding: p.Request.Binding, Adapter: p.Request.Adapter, EnforcementPoint: p.Request.EnforcementPoint, OperationDigest: digest, Now: a.now()}, a.Authority); err != nil {
		return out, err
	}
	refs := invocation.GateReferences{PreparedOperationRef: digest.Scheme + ":" + digest.DescriptorVersion + ":" + digest.Value, DecisionRef: decision.Resolution.ResolutionID}
	cmdRefs, recRefs := refs, refs
	out.Command.Gate, out.Record.Gate = &cmdRefs, &recRefs
	id, err := a.Admission.Admit(ctx, *p.Request, decision, func(admitCtx context.Context) (string, error) {
		currentDigest, digestErr := Digest(out.Command, p.Runner)
		if digestErr != nil || currentDigest != p.Request.OperationDigest {
			return "", gate.ErrBinding
		}
		if admissionErr := gate.ValidateAdmission(admitCtx, *p.Request, decision, gate.Admission{Binding: p.Request.Binding, Adapter: p.Request.Adapter, EnforcementPoint: p.Request.EnforcementPoint, OperationDigest: currentDigest, Now: a.now()}, a.Authority); admissionErr != nil {
			return "", admissionErr
		}
		return starter.Start(admitCtx, out.Command, p.Runner)
	})
	out.RunID = id
	return out, err
}
