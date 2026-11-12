package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/tools/caster/internal/ref"
	"github.com/hollis-labs/tools/caster/internal/resolver"
	"github.com/hollis-labs/tools/caster/internal/storage/storagetest"
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
	st := storagetest.Open(t)
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

func TestWhoamiReportsTheCaller(t *testing.T) {
	s, _ := newSvc(t)
	got, err := s.Identity.Whoami(ctxWith(ScopeRead, ScopeWrite), struct{}{})
	if err != nil || got.ID != "device/test" || len(got.Scopes) != 2 || got.ExpiresAt != nil {
		t.Fatalf("whoami %+v err %v", got, err)
	}
	if _, err = s.Identity.Whoami(context.Background(), struct{}{}); KindOf(err) != KindUnauthenticated {
		t.Fatalf("no identity must be unauthenticated, got %v", err)
	}
}

func TestLocalScopesNeverHoldAdminOrAct(t *testing.T) {
	for _, s := range LocalScopes {
		if s == ScopeAdmin || s == ScopeAct || s == "*" || s == "caster.*" {
			t.Fatalf("the local principal must not hold %s", s)
		}
	}
	local := ctxWith(LocalScopes...)
	if can(local, ScopeAdmin) || can(local, ScopeAct) {
		t.Fatal("a caller with exactly LocalScopes passed an admin or act check")
	}
	for _, s := range []string{ScopeRead, ScopeResolve, ScopeWrite, ScopeObserve} {
		if !can(local, s) {
			t.Fatalf("the local principal lost %s", s)
		}
	}
}
