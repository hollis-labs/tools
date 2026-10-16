// Package service is the service layer of conduit: the one place business rules
// live, and the only thing the transports (internal/cli, a future internal/api
// and internal/mcp) call. See docs/transport-boundary.md.
package service

import (
	"context"
	"os"
	"os/user"
	"sync"
	"time"

	"github.com/hollis-labs/conduit/internal/blob"
	"github.com/hollis-labs/conduit/internal/cmdparse"
	"github.com/hollis-labs/conduit/internal/policy"
	"github.com/hollis-labs/conduit/internal/runbook"
	"github.com/hollis-labs/conduit/internal/store"
)

// Service is the root of the service layer.
type Service struct {
	// store is the underlying store. It is not an exported field so every caller
	// funnels through the service methods where business rules apply; Store() is
	// the explicit escape hatch for application-service code that composes
	// across several domain services and needs the raw handle. Transports must
	// not use it (.golangci.transport.yml).
	store     *store.Store
	blobs     blob.Store
	policy    *policy.Policy
	principal string
	host      string
	env       []string
	protect   []string // control-plane paths the protect sandbox keeps read-only
	runbooks  *runbook.Set

	approvalTTL time.Duration
	maxActive   int             // per-principal ceiling on pending + approved + running runs
	base        context.Context // lifetime of background executions

	mu       sync.Mutex
	running  map[string]context.CancelFunc
	canceled map[string]bool

	execs          sync.WaitGroup // executions in flight, drained by Shutdown
	heartbeatEvery time.Duration
	staleAfter     time.Duration
	// decide is the policy entry; a field so tests can make it misbehave.
	decide func(*cmdparse.Parsed) policy.Decision
}

// Option configures a Service.
type Option func(*Service)

// WithBlobs sets the output blob store.
func WithBlobs(b blob.Store) Option { return func(s *Service) { s.blobs = b } }

// WithPolicy sets the policy; the default is the built-in baseline.
func WithPolicy(p *policy.Policy) Option { return func(s *Service) { s.policy = p } }

// WithPrincipal names who is running commands.
func WithPrincipal(p string) Option { return func(s *Service) { s.principal = p } }

// WithProtect lists conduit's own state (database, config) so the protect
// sandbox profile can keep it read-only for the commands it runs.
func WithProtect(paths ...string) Option { return func(s *Service) { s.protect = paths } }

// WithRunbooks sets the named operations the service offers.
func WithRunbooks(r *runbook.Set) Option { return func(s *Service) { s.runbooks = r } }

// WithApprovalTTL sets how long a pending run can wait for approval (default 15m).
func WithApprovalTTL(d time.Duration) Option { return func(s *Service) { s.approvalTTL = d } }

// WithMaxActive caps how many runs one principal may have pending, approved or running (default 50).
func WithMaxActive(n int) Option { return func(s *Service) { s.maxActive = n } }

// WithBaseContext sets the context background executions run under; cancel it
// to stop them (a daemon's shutdown).
func WithBaseContext(ctx context.Context) Option { return func(s *Service) { s.base = ctx } }

// WithLiveness sets how often a running command's owner proves it is alive and
// how long without a heartbeat before the reaper treats the run as orphaned.
func WithLiveness(every, staleAfter time.Duration) Option {
	return func(s *Service) { s.heartbeatEvery, s.staleAfter = every, staleAfter }
}

// WithEnv sets the base environment commands start from (before scrubbing).
func WithEnv(env []string) Option { return func(s *Service) { s.env = env } }

// Store returns the underlying store, for application-service code that
// composes across domain services. It is the sanctioned crossing point, not a
// convenience for transports.
func (s *Service) Store() *store.Store { return s.store }

// New constructs a Service wired to the provided store.
func New(st *store.Store, opts ...Option) *Service {
	s := &Service{store: st, policy: policy.Baseline(), env: os.Environ(), approvalTTL: 15 * time.Minute, maxActive: 50,
		base: context.Background(), running: map[string]context.CancelFunc{}, canceled: map[string]bool{},
		heartbeatEvery: 5 * time.Second, staleAfter: 30 * time.Second}
	if u, err := user.Current(); err == nil {
		s.principal = "user:" + u.Username
	}
	s.host, _ = os.Hostname()
	for _, o := range opts {
		o(s)
	}
	s.decide = func(p *cmdparse.Parsed) policy.Decision { return s.policy.Decide(p) }
	return s
}

// Shutdown waits, up to d, for executions in flight to finish recording. Call it
// after canceling the base context (which stops the commands) and before closing
// the store, so a stopping daemon does not leave runs half-written.
func (s *Service) Shutdown(d time.Duration) {
	done := make(chan struct{})
	go func() { s.execs.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}
