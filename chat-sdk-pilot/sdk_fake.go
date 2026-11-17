package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/sdk"
	"github.com/hollis-labs/libs/agent/sdk/agentdir"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/libs/mesh/core/runtimecontract"
)

// directoryStore gives agentdir.Host the same conversation identity as
// chathost. This is a deterministic local fixture, not a durable SDK store.
type directoryStore struct {
	mu   sync.Mutex
	busy bool
}

type directoryLease struct {
	store *directoryStore
	ctx   context.Context
}

func (s *directoryStore) Acquire(ctx context.Context, snap agentdir.Snapshot) (agentdir.Lease, error) {
	if snap.Profile.Kind != agentdir.Stable || snap.Profile.SeatID != string(seatID) {
		return nil, errors.New("pilot: wrong agent directory")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return nil, agentdir.ErrBusy
	}
	s.busy = true
	return &directoryLease{store: s, ctx: ctx}, nil
}

func (s *directoryStore) EndTask(context.Context, string) error { return agentdir.ErrTaskEnded }
func (l *directoryLease) ConversationID() string                { return string(conversationID) }
func (l *directoryLease) Context() context.Context              { return l.ctx }
func (l *directoryLease) Release() {
	l.store.mu.Lock()
	l.store.busy = false
	l.store.mu.Unlock()
}

type fakeSDKFactory struct {
	binding runtimecontract.Binding
	env     runtimecontract.EnvironmentRef
	ledger  *fakeSDKLedger
}

type fakeSDKLedger struct {
	mu   sync.Mutex
	runs map[string]sdk.RunReservation
}

func (f fakeSDKFactory) New(_ context.Context, snap agentdir.Snapshot, id string) (agentdir.Runner, error) {
	if id != string(f.binding.ConversationID) {
		return nil, sdk.ErrAdmission
	}
	return fakeSDKRunner{factory: f, definition: snap.Definition}, nil
}

type fakeSDKRunner struct {
	factory    fakeSDKFactory
	definition agentdef.Definition
}

func (r fakeSDKRunner) Run(ctx context.Context, text string) (sdk.Result, error) {
	return r.run(ctx, text, nil)
}

func (r fakeSDKRunner) RunStream(ctx context.Context, text string, delta func(string) error) (sdk.Result, error) {
	if delta == nil {
		return sdk.Result{}, agentdir.ErrStreamingUnavailable
	}
	return r.run(ctx, text, delta)
}

func (r fakeSDKRunner) run(ctx context.Context, text string, delta func(string) error) (sdk.Result, error) {
	var model llmcontracts.Provider = fakeModel{text: text}
	if delta != nil {
		model = agentdir.TapProvider{Provider: model, Delta: delta}
	}
	router, err := sdk.NewRouter("pilot", sdk.Route{Name: "pilot", Model: "deterministic", Provider: model})
	if err != nil {
		return sdk.Result{}, err
	}
	ports := sdk.Ports{
		Router:      router,
		Environment: fakeSDKEnvironment{r.factory},
		Admission:   r.factory.ledger,
		Policy:      fakeSDKPolicy{},
		Events: runtimeevents.SinkFunc(func(_ context.Context, event runtimeevents.Event) error {
			return event.Validate()
		}),
	}
	op, err := sdk.New(r.definition, ports)
	if err != nil {
		return sdk.Result{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return sdk.Result{}, err
	}
	return op.Run(ctx, sdk.Input{ID: hex.EncodeToString(nonce[:]), Binding: r.factory.binding, Environment: r.factory.env, Text: text})
}

type fakeModel struct{ text string }

func (fakeModel) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{ContextWindowSize: 32000}
}
func (fakeModel) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "", sdk.ErrUnsupported
}
func (m fakeModel) StreamChat(_ context.Context, _ llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	stream := make(chan llmtypes.StreamEvent, 3)
	stream <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "Echo: " + m.text}
	stream <- llmtypes.StreamEvent{Type: llmtypes.EventUsage, Usage: &llmtypes.Usage{InputTokens: 1, OutputTokens: 1}}
	stream <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
	close(stream)
	return stream, nil
}

type fakeSDKEnvironment struct{ factory fakeSDKFactory }

func (v fakeSDKEnvironment) Verify(_ context.Context, binding runtimecontract.Binding, env runtimecontract.EnvironmentRef, required []string) error {
	if binding != v.factory.binding || !sameJSON(env, v.factory.env) {
		return sdk.ErrAdmission
	}
	return env.ValidateAt(time.Now(), required)
}

type fakeSDKPolicy struct{}

func (fakeSDKPolicy) CheckDefinition(_ context.Context, def agentdef.Definition, _ runtimecontract.Binding, _ runtimecontract.EnvironmentRef) error {
	if def.HarnessProfile.Permissions.Profile != "pilot" {
		return sdk.ErrAdmission
	}
	return nil
}
func (fakeSDKPolicy) Evaluate(context.Context, sdk.Operation) (sdk.PolicyResult, error) {
	return sdk.PolicyResult{}, sdk.ErrUnsupported
}

func (l *fakeSDKLedger) ReserveRun(_ context.Context, reservation sdk.RunReservation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.runs[reservation.RunID]; exists {
		return sdk.ErrRunUsed
	}
	l.runs[reservation.RunID] = reservation
	return nil
}

func (l *fakeSDKLedger) Admit(ctx context.Context, admission sdk.Admission, start func(context.Context) error) error {
	l.mu.Lock()
	reserved, exists := l.runs[admission.Run.RunID]
	l.mu.Unlock()
	if !exists || !sameJSON(reserved, admission.Run) || admission.Kind != "model" || admission.Binding != reserved.Binding || !sameJSON(admission.Environment, reserved.Environment) {
		return sdk.ErrAdmission
	}
	return start(ctx)
}

func sameJSON(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && string(left) == string(right)
}

func fixtureReference(id string) runtimecontract.Reference {
	digest := sha256.Sum256([]byte(id))
	return runtimecontract.Reference{
		ID: id, Revision: "1", IssuerRef: "pilot-fixture", OwnerID: ownerID,
		Digest: runtimecontract.Digest{Scheme: "sha256", Value: hex.EncodeToString(digest[:])},
	}
}

func fakeSDKEnvironmentRef() runtimecontract.EnvironmentRef {
	return runtimecontract.EnvironmentRef{
		Receipt: fixtureReference("receipt"), Workspace: fixtureReference("workspace"),
		Lease: fixtureReference("lease"), Grants: fixtureReference("grants"),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
}

func newDirectoryHost(agentDirectory string, binding runtimecontract.Binding) (agentdir.Host, error) {
	if binding.ConversationID != conversationID || binding.OwnerID != ownerID || binding.OperativeID != agentID {
		return agentdir.Host{}, fmt.Errorf("pilot: unexpected SDK binding")
	}
	return agentdir.Host{
		Root:    filepath.Dir(agentDirectory),
		Store:   &directoryStore{},
		Factory: fakeSDKFactory{binding: binding, env: fakeSDKEnvironmentRef(), ledger: &fakeSDKLedger{runs: map[string]sdk.RunReservation{}}},
	}, nil
}
