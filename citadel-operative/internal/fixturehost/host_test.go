package fixturehost

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/hollis-labs/libs/agent/sdk"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/gate/gatetest"
)

func definition(t *testing.T) agentdef.Definition {
	t.Helper()
	data, err := os.ReadFile("../../testdata/operative.md")
	if err != nil {
		t.Fatal(err)
	}
	def, err := agentdef.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return *def
}
func operative(t *testing.T, h *Host) *sdk.Operative {
	t.Helper()
	o, err := sdk.New(definition(t), h.Ports())
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func host(t *testing.T, mode string, ledger *Ledger) *Host {
	t.Helper()
	h, err := New(mode, ledger)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestChangedApprovalDigestAndStaleBindingRefuseActualToolAdmission(t *testing.T) {
	for _, test := range []string{"digest", "binding", "policy", "resource", "reservation"} {
		t.Run(test, func(t *testing.T) {
			ledger := NewLedger()
			h := host(t, "allow", ledger)
			h.beforeAdmission = func(a sdk.Admission) {
				if a.Kind != "tool" {
					return
				}
				switch test {
				case "digest":
					a.Request.OperationDigest = gatetest.Digest("sdk-tool-v1", "changed")
				case "binding":
					ledger.mu.Lock()
					ledger.binding.Generation++
					ledger.binding.AttemptID = "successor"
					ledger.mu.Unlock()
				case "policy":
					a.Policy.Reference = gatetest.Reference("unbound-policy")
				case "resource":
					a.Operation.Versions[0].Revision = "changed"
				case "reservation":
					ledger.mu.Lock()
					stored := ledger.runs[key(a.Run)]
					stored.InputDigest = gatetest.Digest("sdk-run-input-v1", "changed")
					ledger.runs[key(a.Run)] = stored
					ledger.mu.Unlock()
				}
			}
			_, err := operative(t, h).Run(context.Background(), h.Input("r1", "Read the synthetic value."))
			if err == nil || h.Stats().ToolEffects != 0 || h.Stats().ModelCalls != 1 {
				t.Fatalf("unauthorized tool admission: err=%v stats=%+v", err, h.Stats())
			}
			if test == "binding" && !errors.Is(err, gate.ErrBinding) {
				t.Fatalf("stale binding error: %v", err)
			}
		})
	}
}

func TestUncertainRunIDRetainedAcrossNewHostsAndGenerations(t *testing.T) {
	ledger := NewLedger()
	first := host(t, "allow", ledger)
	first.toolFailure = errors.New("uncertain after synthetic effect")
	_, err := operative(t, first).Run(context.Background(), first.Input("same-input", "Read the fixture."))
	if err == nil || first.Stats().ToolEffects != 1 {
		t.Fatalf("uncertain setup: %v %+v", err, first.Stats())
	}
	ledger.mu.Lock()
	ledger.binding.Generation++
	ledger.binding.AttemptID = "new-attempt"
	ledger.mu.Unlock()
	replacement := host(t, "allow", ledger)
	if replacement.callID == first.callID {
		t.Fatal("repro requires a fresh provider call ID")
	}
	_, err = operative(t, replacement).Run(context.Background(), replacement.Input("same-input", "Changed prompt after replacement."))
	if !errors.Is(err, sdk.ErrRunUsed) || replacement.Stats().ModelCalls != 0 || replacement.Stats().ToolEffects != 0 {
		t.Fatalf("uncertain effect replayed: %v %+v", err, replacement.Stats())
	}
	if len(ledger.claims) != 1 {
		t.Fatal("uncertain effect claim was released")
	}
}

func TestConcurrentRunnersReserveOneLogicalInput(t *testing.T) {
	ledger := NewLedger()
	first, second := host(t, "allow", ledger), host(t, "allow", ledger)
	operatives := []*sdk.Operative{operative(t, first), operative(t, second)}
	inputs := []sdk.Input{first.Input("same-input", "read"), second.Input("same-input", "read")}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, o := range operatives {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := o.Run(context.Background(), inputs[i]); results <- err }()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, sdk.ErrRunUsed) {
			t.Fatal(err)
		}
	}
	if winners != 1 || first.Stats().ToolEffects+second.Stats().ToolEffects != 1 {
		t.Fatalf("duplicate logical input won: winners=%d first=%+v second=%+v", winners, first.Stats(), second.Stats())
	}
}

func TestApprovalProjectionMustMatchHITLOutcome(t *testing.T) {
	h := host(t, "allow", NewLedger())
	input := h.Input("r1", "read")
	request := gatetest.Request(clock())
	request.Binding = input.Binding
	request.Environment = &input.Environment
	request.Adapter = sdk.NativeAdapterScope()
	request.EnforcementPoint = "tradecraft/tool-admission"
	request.Policy = gatetest.Reference("fixture-policy")
	request.Obligations = []gate.ObligationRef{{Kind: "approval", Ref: gatetest.Reference("fixture-approval")}}
	ticket, err := h.Request(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := h.Await(context.Background(), ticket, gate.WaitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	verifier := authorityPort{h: h}
	if err = verifier.Verify(context.Background(), request, *resolution.Decision); err != nil {
		t.Fatal(err)
	}
	forged := *resolution.Decision
	forged.Resolution.Participant.PrincipalRef = "caller-label"
	if err = verifier.Verify(context.Background(), request, forged); !errors.Is(err, gate.ErrAuthority) {
		t.Fatalf("forged authority accepted: %v", err)
	}
	request.OperationDigest = gatetest.Digest("tool-v1", "other-operation")
	if err = verifier.Verify(context.Background(), request, *resolution.Decision); !errors.Is(err, gate.ErrAuthority) {
		t.Fatalf("changed operation accepted: %v", err)
	}
}

func TestReservationsAreSingleUseEvenForPendingAndIdenticalArguments(t *testing.T) {
	ledger := NewLedger()
	pending := host(t, "pending", ledger)
	_, err := operative(t, pending).Run(context.Background(), pending.Input("pending-input", "read"))
	if !errors.Is(err, sdk.ErrPending) {
		t.Fatal(err)
	}
	allow := host(t, "allow", ledger)
	_, err = operative(t, allow).Run(context.Background(), allow.Input("pending-input", "read"))
	if !errors.Is(err, sdk.ErrRunUsed) || allow.Stats().ModelCalls != 0 {
		t.Fatalf("pending input replayed: %v", err)
	}
	_, err = operative(t, allow).Run(context.Background(), allow.Input("new-logical-input", "read"))
	if err != nil || allow.Stats().ToolEffects != 1 {
		t.Fatalf("distinct input incorrectly deduplicated: %v", err)
	}
}
