// Package fixturehost supplies only a closed synthetic universe. Its references,
// authority and in-memory claims cannot authenticate any real caller or endpoint.
package fixturehost

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/agent/sdk"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/gate/gatetest"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/hitl/memstore"
	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
	"github.com/hollis-labs/libs/security/policy"
)

type runKey struct {
	Owner        runtimecontract.OwnerID
	Operative    runtimecontract.OperativeID
	Conversation runtimecontract.ConversationID
	ID           string
}

func key(r sdk.RunReservation) runKey {
	return runKey{r.Binding.OwnerID, r.Binding.OperativeID, r.Binding.ConversationID, r.RunID}
}

// Ledger is one explicit process-local fixture universe. Its mutex serializes
// binding changes, single-use reservations and effect claims through callback
// completion. Real hosts require durable admission, authentication and leases.
type Ledger struct {
	mu          sync.Mutex
	binding     runtimecontract.Binding
	environment runtimecontract.EnvironmentRef
	runs        map[runKey]sdk.RunReservation
	claims      map[string]bool
	instances   uint64
}

func NewLedger() *Ledger {
	r := gatetest.Request(clock())
	r.Environment.Capabilities = []string{"tool.execute", sdk.CapabilityApproval}
	return &Ledger{binding: r.Binding, environment: *r.Environment, runs: map[runKey]sdk.RunReservation{}, claims: map[string]bool{}}
}

func clock() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) }
func Supported(mode string) bool {
	switch mode {
	case "allow", "deny", "pending", "cancel":
		return true
	}
	return false
}

type Host struct {
	ledger  *Ledger
	mode    string
	callID  string
	service *hitl.Service
	models  atomic.Int64
	effects atomic.Int64
	events  atomic.Int64
	// Test-only injection points are private to this fixture package. No CLI flag
	// can alter binding/digest verification or enable an external effect adapter.
	beforeAdmission func(sdk.Admission)
	toolFailure     error
}

func New(mode string, ledger *Ledger) (*Host, error) {
	if !Supported(mode) || ledger == nil {
		return nil, fmt.Errorf("fixture: explicit supported mode and ledger required")
	}
	ledger.mu.Lock()
	ledger.instances++
	id := ledger.instances
	ledger.mu.Unlock()
	return &Host{ledger: ledger, mode: mode, callID: fmt.Sprintf("fixture-call-%d", id), service: hitl.NewService(memstore.New(), hitl.Options{Clock: clock})}, nil
}

type Stats struct{ ModelCalls, ToolEffects, Events int64 }

func (h *Host) Stats() Stats { return Stats{h.models.Load(), h.effects.Load(), h.events.Load()} }
func (h *Host) Input(id, text string) sdk.Input {
	h.ledger.mu.Lock()
	defer h.ledger.mu.Unlock()
	return sdk.Input{ID: id, Text: text, Binding: h.ledger.binding, Environment: clone(h.ledger.environment)}
}

func (h *Host) Ports() sdk.Ports {
	router, _ := sdk.NewRouter("fixture", sdk.Route{Name: "fixture", Model: "synthetic-only", Provider: provider{h}})
	return sdk.Ports{Router: router, Environment: environmentPort{h}, Admission: h, Policy: h, Gate: sdk.GateAdapter{Port: h}, Authority: authorityPort{h: h}, Now: clock, Capabilities: []string{"tool.execute", sdk.CapabilityApproval}, Events: runtimeevents.SinkFunc(func(_ context.Context, e runtimeevents.Event) error {
		if err := e.Validate(); err != nil {
			return err
		}
		h.events.Add(1)
		return nil
	}), Tools: []sdk.Tool{{Definition: llmtypes.ToolDefinition{Name: "read_value", Description: "Read the in-memory fixture value", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}, Versions: []runtimecontract.Reference{gatetest.Reference("fixture-read-value-v1")}, Execute: func(ctx context.Context, args map[string]any) (sdk.ToolResult, error) {
		if err := ctx.Err(); err != nil {
			return sdk.ToolResult{}, err
		}
		if len(args) != 0 {
			return sdk.ToolResult{}, sdk.ErrAdmission
		}
		h.effects.Add(1)
		return sdk.ToolResult{Content: "42"}, h.toolFailure
	}}}}
}

type environmentPort struct{ h *Host }

func (p environmentPort) Verify(ctx context.Context, b runtimecontract.Binding, e runtimecontract.EnvironmentRef, required []string) error {
	p.h.ledger.mu.Lock()
	defer p.h.ledger.mu.Unlock()
	return p.h.verify(ctx, b, e, required)
}
func (h *Host) verify(ctx context.Context, b runtimecontract.Binding, e runtimecontract.EnvironmentRef, required []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b != h.ledger.binding || !reflect.DeepEqual(e, h.ledger.environment) {
		return gate.ErrBinding
	}
	return e.ValidateAt(clock(), required)
}

func (h *Host) CheckDefinition(_ context.Context, d agentdef.Definition, _ runtimecontract.Binding, _ runtimecontract.EnvironmentRef) error {
	if d.HarnessProfile.Permissions.Profile != "fixture" || d.Continuity.Mode != agentdef.Ephemeral || !reflect.DeepEqual(d.Requirements.Tools, []string{"read_value"}) {
		return fmt.Errorf("%w: fixture requires ephemeral mode, fixture profile and read_value only", gate.ErrAuthority)
	}
	return nil
}
func (h *Host) Evaluate(context.Context, sdk.Operation) (sdk.PolicyResult, error) {
	return sdk.PolicyResult{Decision: policy.Decision{Effect: policy.Ask, Obligations: []policy.Obligation{{Kind: policy.Approval, PolicyID: "fixture-policy", Reference: "fixture-approval"}}}, Reference: gatetest.Reference("fixture-policy"), Obligations: []gate.ObligationRef{{Kind: "approval", Ref: gatetest.Reference("fixture-approval")}}}, nil
}

func (h *Host) ReserveRun(ctx context.Context, r sdk.RunReservation) error {
	h.ledger.mu.Lock()
	defer h.ledger.mu.Unlock()
	if err := h.verify(ctx, r.Binding, r.Environment, nil); err != nil {
		return err
	}
	if r.RunID == "" || r.DefinitionDigest == "" || r.Route != "fixture" || r.Model != "synthetic-only" || r.InputDigest.DescriptorVersion != "sdk-run-input-v1" {
		return sdk.ErrAdmission
	}
	if err := r.InputDigest.Validate(); err != nil {
		return err
	}
	if _, exists := h.ledger.runs[key(r)]; exists {
		return sdk.ErrRunUsed
	}
	h.ledger.runs[key(r)] = clone(r)
	return nil
}

func (h *Host) Admit(ctx context.Context, a sdk.Admission, start func(context.Context) error) error {
	if h.beforeAdmission != nil {
		h.beforeAdmission(a)
	}
	h.ledger.mu.Lock()
	defer h.ledger.mu.Unlock()
	if err := h.verify(ctx, a.Binding, a.Environment, nil); err != nil {
		return err
	}
	r, exists := h.ledger.runs[key(a.Run)]
	if !exists || !reflect.DeepEqual(r, a.Run) || r.Binding != a.Binding || !reflect.DeepEqual(r.Environment, a.Environment) || r.DefinitionDigest != a.DefinitionDigest {
		return sdk.ErrAdmission
	}
	switch a.Kind {
	case "model":
		if a.Route != r.Route {
			return sdk.ErrAdmission
		}
		return start(ctx)
	case "tool":
		if a.Operation == nil || a.Request == nil || a.Decision == nil || a.Policy == nil {
			return sdk.ErrAdmission
		}
		op := a.Operation
		if op.RunID != r.RunID || op.InputDigest != r.InputDigest || op.Binding != a.Binding || !reflect.DeepEqual(op.Environment, a.Environment) || op.DefinitionDigest != r.DefinitionDigest || op.PermissionProfile != "fixture" || op.Tool != "read_value" || string(op.Arguments) != "{}" || !reflect.DeepEqual(op.Versions, []runtimecontract.Reference{gatetest.Reference("fixture-read-value-v1")}) {
			return sdk.ErrAdmission
		}
		current, _ := h.Evaluate(ctx, *op)
		if !reflect.DeepEqual(current, *a.Policy) {
			return gate.ErrAuthority
		}
		digest, err := op.Digest()
		if err != nil {
			return err
		}
		if err = gate.ValidateAdmission(ctx, *a.Request, *a.Decision, gate.Admission{Binding: h.ledger.binding, Adapter: sdk.NativeAdapterScope(), EnforcementPoint: "tradecraft/tool-admission", OperationDigest: digest, Now: clock()}, authorityPort{h: h, locked: true}); err != nil {
			return err
		}
		if h.ledger.claims[op.ID] {
			return sdk.ErrRunUsed
		}
		h.ledger.claims[op.ID] = true
		return start(ctx)
	default:
		return sdk.ErrUnsupported
	}
}

// Snapshots detach mutable accepted values from callers. All values passed here
// are serializable SDK records; failure is a programmer error in this fake host.
func clone[T any](v T) T {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var copy T
	if err = json.Unmarshal(raw, &copy); err != nil {
		panic(err)
	}
	return copy
}
