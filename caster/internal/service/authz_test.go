package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/caster/internal/ref"
	"github.com/hollis-labs/caster/internal/resolver"
	"github.com/hollis-labs/caster/internal/store"
)

func TestFailClosedWithoutIdentity(t *testing.T) {
	s, _ := newSvc(t)
	anon := context.Background()
	if _, err := s.Notes.List(anon, ListRequest{}); KindOf(err) != KindUnauthenticated {
		t.Fatalf("no identity must be unauthenticated, got %v", err)
	}
	if _, err := s.Refs.Scan(anon, ScanRequest{Text: "x"}); KindOf(err) != KindUnauthenticated {
		t.Fatalf("scan without identity: %v", err)
	}
	if _, err := s.Shadow.Observe(anon, ObserveRequest{Text: "x"}); KindOf(err) != KindUnauthenticated {
		t.Fatalf("observe without identity: %v", err)
	}
}

func TestScopeMatrix(t *testing.T) {
	s, _ := newSvc(t)
	_, _ = s.Notes.Put(localCtx(), PutRequest{Name: "n/a", Content: "hello"})
	denied := func(name string, ctx context.Context, f func(context.Context) error) {
		t.Helper()
		if err := f(ctx); KindOf(err) != KindDenied {
			t.Errorf("%s: want denied, got %v", name, err)
		}
	}
	allowed := func(name string, ctx context.Context, f func(context.Context) error) {
		t.Helper()
		if err := f(ctx); err != nil {
			t.Errorf("%s: want allowed, got %v", name, err)
		}
	}
	put := func(c context.Context) error { _, e := s.Notes.Put(c, PutRequest{Name: "n/b", Content: "x"}); return e }
	get := func(c context.Context) error { _, e := s.Refs.Get(c, GetRequest{Ref: "n/a"}); return e }
	getRemote := func(c context.Context) error { _, e := s.Refs.Get(c, GetRequest{Ref: "CW-20260417-0011"}); return e }
	snap := func(c context.Context) error { _, e := s.Refs.Snapshot(c, SnapRequest{Ref: "n/a"}); return e }
	obs := func(c context.Context) error { _, e := s.Shadow.Observe(c, ObserveRequest{Text: "x"}); return e }
	rep := func(c context.Context) error { _, e := s.Shadow.Report(c, ReportRequest{}); return e }
	act := func(c context.Context) error {
		_, e := s.Actions.Run(c, ActRequest{Action: ActionSaveHandoff, Ref: "n/a", Context: okCtx, DryRun: true})
		return e
	}

	allowed("read can get local", ctxWith(ScopeRead), get)
	denied("read cannot put", ctxWith(ScopeRead), put)
	denied("write alone cannot read", ctxWith(ScopeWrite), get)
	allowed("write can put", ctxWith(ScopeWrite), put)
	denied("snapshot needs write", ctxWith(ScopeRead), snap)
	allowed("snapshot with read+write", ctxWith(ScopeRead, ScopeWrite), snap)
	denied("read cannot get a torque ref: it is read AS caster", ctxWith(ScopeRead), getRemote)
	denied("observe cannot read", ctxWith(ScopeObserve), get)
	allowed("observe can observe", ctxWith(ScopeObserve), obs)
	denied("read cannot observe", ctxWith(ScopeRead), obs)
	denied("observe cannot see reports", ctxWith(ScopeObserve), rep)
	allowed("admin sees reports", ctxWith(ScopeAdmin), rep)
	denied("act needs act", ctxWith(ScopeRead, ScopeWrite), act)
	allowed("act with act+read", ctxWith(ScopeAct, ScopeRead), act)
	allowed("wildcard grant", ctxWith("caster.*"), put)
}

func TestScanAndExpandDoNotLeakRemoteRefsWithoutResolve(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(st, fakeRemote{})
	_, _ = s.Notes.Put(localCtx(), PutRequest{Name: "n/a", Content: "local"})
	text := "see caster://n/a and torque://task/CW-20260417-0011"

	res, _ := s.Refs.Scan(ctxWith(ScopeRead), ScanRequest{Text: text})
	if len(res.Refs) != 1 || res.Refs[0].Ref != "caster://n/a" || res.Unresolved != 1 {
		t.Fatalf("without resolve the remote ref must look unresolved: %+v", res)
	}
	exp, _ := s.Refs.Expand(ctxWith(ScopeRead), ExpandRequest{Text: text})
	if strings.Contains(exp.Text, "SECRET-TASK-BODY") {
		t.Fatalf("remote content leaked to a caller without caster.resolve: %s", exp.Text)
	}
	exp, _ = s.Refs.Expand(ctxWith(ScopeRead, ScopeResolve), ExpandRequest{Text: text})
	if !strings.Contains(exp.Text, "SECRET-TASK-BODY") {
		t.Fatalf("with caster.resolve the remote ref must expand: %s", exp.Text)
	}
}

// fakeRemote resolves every torque ref.
type fakeRemote struct{}

func (fakeRemote) Schemes() []string { return []string{"torque"} }
func (fakeRemote) Resolve(_ context.Context, r ref.Ref) (resolver.Meta, error) {
	return resolver.Meta{Ref: r, State: resolver.StateResolved, Kind: "task", Mime: "text/markdown"}, nil
}
func (fakeRemote) Materialize(_ context.Context, r ref.Ref) (resolver.Content, error) {
	return resolver.Content{Meta: resolver.Meta{Ref: r, State: resolver.StateResolved}, Data: []byte("SECRET-TASK-BODY")}, nil
}

func TestPairingLifecycle(t *testing.T) {
	s, _ := newSvc(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	s.access.now = func() time.Time { return now }
	op := localCtx()

	if _, err := s.Access.CreateGrant(ctxWith(ScopeAdmin), PairRequest{Scopes: []string{"read"}}); KindOf(err) != KindDenied {
		t.Fatalf("an admin DEVICE must not mint grants (local terminal only): %v", err)
	}
	if _, err := s.Access.CreateGrant(op, PairRequest{Scopes: []string{"bogus"}}); KindOf(err) != KindInvalid {
		t.Fatalf("unknown scope: %v", err)
	}

	g, err := s.Access.CreateGrant(op, PairRequest{Scopes: []string{"read", "caster.observe"}, Label: "laptop"})
	if err != nil || !strings.HasPrefix(g.Code, "ccg_") {
		t.Fatalf("grant: %+v %v", g, err)
	}
	// Asking for more than the grant covers is refused and does NOT burn the grant.
	if _, err := s.Access.Exchange(op, ExchangeRequest{Code: g.Code, Scopes: []string{"write"}}); KindOf(err) != KindUnauthenticated {
		t.Fatalf("widening must be refused: %v", err)
	}
	ex, err := s.Access.Exchange(op, ExchangeRequest{Code: g.Code, Scopes: []string{"read"}})
	if err != nil || !strings.HasPrefix(ex.Token, "cst_") || len(ex.Scopes) != 1 || ex.Scopes[0] != ScopeRead {
		t.Fatalf("exchange (narrowed): %+v %v", ex, err)
	}
	if _, err := s.Access.Exchange(op, ExchangeRequest{Code: g.Code}); KindOf(err) != KindUnauthenticated {
		t.Fatalf("a code works once: %v", err)
	}
	for _, bad := range []string{"", "ccg_nope", "cst_" + ex.Token[4:], strings.Repeat("x", 500)} {
		if _, err := s.Access.Exchange(op, ExchangeRequest{Code: bad}); err == nil || err.Error() != errRefused().Error() {
			t.Fatalf("every failure must look the same, got %v for %q", err, bad)
		}
	}

	p, err := s.access.Verifier().Verify(op, ex.Token)
	if err != nil || p.ID != "device/"+ex.DeviceID || !p.HasScope(ScopeRead) || p.HasScope(ScopeWrite) {
		t.Fatalf("verify: %+v %v", p, err)
	}
	dev := WithPrincipal(op, p)
	if _, err := s.Access.Devices(dev, struct{}{}); KindOf(err) != KindDenied {
		t.Fatalf("a read device cannot list devices: %v", err)
	}
	if _, err := s.Access.Renew(dev, RenewRequest{Scopes: []string{"write"}}); KindOf(err) != KindDenied {
		t.Fatalf("renewal must not widen: %v", err)
	}
	now = now.Add(20 * 24 * time.Hour)
	renewed, err := s.Access.Renew(dev, RenewRequest{})
	if err != nil || !renewed.ExpiresAt.Equal(now.Add(DeviceLifetime)) {
		t.Fatalf("renew: %+v %v", renewed, err)
	}
	now = now.Add(DeviceLifetime + time.Hour)
	if _, err := s.access.Verifier().Verify(op, ex.Token); err == nil {
		t.Fatal("an expired device must not verify")
	}

	g2, _ := s.Access.CreateGrant(op, PairRequest{Scopes: []string{"read"}})
	ex2, _ := s.Access.Exchange(op, ExchangeRequest{Code: g2.Code})
	if _, err := s.access.Verifier().Verify(op, ex2.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Access.Revoke(op, RevokeRequest{ID: ex2.DeviceID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.access.Verifier().Verify(op, ex2.Token); err == nil {
		t.Fatal("a revoked device must stop verifying immediately")
	}

	g3, _ := s.Access.CreateGrant(op, PairRequest{Scopes: []string{"read"}, TTLSeconds: 60})
	now = now.Add(2 * time.Minute)
	if _, err := s.Access.Exchange(op, ExchangeRequest{Code: g3.Code}); KindOf(err) != KindUnauthenticated {
		t.Fatalf("an expired code must be refused: %v", err)
	}
}
