// Package api is conduit's HTTP transport: the addressable "run this on this host" surface. The identity module has
// authenticated the caller and the auth guard has checked the route's scope before a handler runs (RegisterScopes);
// every run is attributed to the verified principal and passes the same policy as a local one. The approval routes
// need a scope that agent keys are not given AND a signed-in person (a session or a user token): an API key is never a
// person, and a principal cannot approve its own run.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/core/panics/httpguard"
	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tools/conduit/internal/runbook"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

const maxBody = 1 << 20

// Config configures the handler.
type Config struct {
	// AllowDirs are the roots a caller's working directory must sit under. A request with no dir runs in the first one.
	// At least one is required.
	AllowDirs []string
	// RatePerMinute caps requests per principal (default 240).
	RatePerMinute int
}

// Server is the HTTP transport.
type Server struct {
	svc *service.Service
	cfg Config

	mu     sync.Mutex
	window map[string]*bucket // per principal
}

type bucket struct {
	start time.Time
	n     int
}

// New returns the API server. It starts nothing: Run starts the loop that executes approved runs.
func New(svc *service.Service, cfg Config) (*Server, error) {
	if len(cfg.AllowDirs) == 0 {
		return nil, errors.New("api: at least one allowed directory is required")
	}
	roots, err := service.ResolveRoots(cfg.AllowDirs)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	cfg.AllowDirs = roots
	if cfg.RatePerMinute <= 0 {
		cfg.RatePerMinute = 240
	}
	return &Server{svc: svc, cfg: cfg, window: map[string]*bucket{}}, nil
}

// Run executes the runs a person approved, which the daemon owns, until ctx ends.
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// contained per tick: a panic in one pass must not end the loop
			_ = panics.Call("api start-approved", func() error { _, err := s.svc.StartApprovedRemote(); return err })
		}
	}
}

type handler func(w http.ResponseWriter, r *http.Request, p libauth.Principal)

type route struct {
	Method, Pattern, Scope string
	h                      handler
}

func (s *Server) routes() []route {
	return []route{
		{"POST", "/api/check", service.ScopeRead, s.check},
		{"POST", "/api/exec", service.ScopeRun, s.exec},
		{"GET", "/api/runs", service.ScopeRead, s.list},
		{"GET", "/api/runs/{id}", service.ScopeRead, s.get},
		{"GET", "/api/runs/{id}/output", service.ScopeRead, s.output},
		{"POST", "/api/runs/{id}/cancel", service.ScopeRun, s.cancel},
		{"GET", "/api/approvals", service.ScopeApprove, s.pending},
		{"POST", "/api/runs/{id}/approve", service.ScopeApprove, s.approve},
		{"POST", "/api/runs/{id}/reject", service.ScopeApprove, s.reject},
		{"GET", "/api/runbooks", service.ScopeRead, s.runbooks},
	}
}

// Handler is the route set, for httphost.Options.Routes.
func (s *Server) Handler() *http.ServeMux {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.Method+" "+rt.Pattern, httpguard.Handler(s.wrap(rt.h)))
	}
	return mux
}

// RegisterScopes declares each route's scope on the table the auth guard enforces. A route not listed is refused in
// enforce mode.
func (s *Server) RegisterScopes(t *libauth.ScopeTable) {
	for _, rt := range s.routes() {
		t.Route(rt.Method, rt.Pattern, rt.Scope)
	}
}

// wrap reads the principal the guard verified, rate limits per principal and marks the context.
func (s *Server) wrap(h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := libauth.FromContext(r.Context())
		if !ok || !p.Verified {
			// The guard has already refused an unauthenticated route in enforce mode; this is the second lock.
			writeErr(w, 401, "unauthenticated", "a verified caller is required")
			return
		}
		if s.throttled(p.ID) {
			writeErr(w, 429, "rate_limited", "request rate exceeded")
			return
		}
		ctx := service.As(r.Context(), p.ID)
		if p.Kind == "user" {
			ctx = service.AsPerson(r.Context(), p.ID)
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		h(w, r.WithContext(ctx), p)
	})
}

func (s *Server) throttled(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.window[key]
	if b == nil || time.Since(b.start) >= time.Minute {
		if len(s.window) > 4096 { // drop expired windows so the map cannot grow without bound
			for k, v := range s.window {
				if time.Since(v.start) >= time.Minute {
					delete(s.window, k)
				}
			}
		}
		b = &bucket{start: time.Now()}
		s.window[key] = b
	}
	b.n++
	return b.n > s.cfg.RatePerMinute
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		return fmt.Errorf("%w: %w", service.ErrBadRequest, err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": kind, "message": msg}})
}

func fail(w http.ResponseWriter, err error) {
	if ref, ok := panics.Ref(err); ok {
		writeJSON(w, 500, map[string]any{"error": map[string]any{"code": "internal", "message": "internal error (ref " + ref + ")", "ref": ref}})
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, 404, "not_found", err.Error())
	case errors.Is(err, service.ErrBadRequest), errors.Is(err, runbook.ErrParam), errors.Is(err, service.ErrNoRunbook):
		writeErr(w, 400, "invalid_request", err.Error())
	case errors.Is(err, service.ErrNotPerson):
		writeErr(w, 403, "not_a_person", err.Error())
	case errors.Is(err, service.ErrSelfApproval):
		writeErr(w, 403, "self_approval", err.Error())
	case errors.Is(err, service.ErrForbidden):
		writeErr(w, 403, "forbidden", err.Error())
	case errors.Is(err, service.ErrTooMany):
		writeErr(w, 429, "too_many_runs", err.Error())
	case errors.Is(err, service.ErrNotPending), errors.Is(err, service.ErrNotRunnable), errors.Is(err, service.ErrExpired):
		writeErr(w, 409, "conflict", err.Error())
	case errors.Is(err, service.ErrDenied):
		writeErr(w, 403, "denied", err.Error())
	default:
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, 413, "too_large", "request body too large")
			return
		}
		writeErr(w, 500, "internal", "internal error")
	}
}

// dir validates a caller-supplied working directory against the allowed roots.
func (s *Server) dir(d string) (string, error) { return service.ResolveDir(s.cfg.AllowDirs, d) }

func (s *Server) check(w http.ResponseWriter, r *http.Request, _ libauth.Principal) {
	var a struct {
		Command string `json:"command"`
	}
	if err := decode(r, &a); err != nil {
		fail(w, err)
		return
	}
	p, d := s.svc.Check(a.Command)
	writeJSON(w, 200, map[string]any{"parsed": p, "decision": d})
}

func (s *Server) exec(w http.ResponseWriter, r *http.Request, _ libauth.Principal) {
	var a service.ExecRequest
	if err := decode(r, &a); err != nil {
		fail(w, err)
		return
	}
	// A runbook that fixes its own directory keeps it (the operator reviewed it);
	// everything else runs in a validated directory under an allowed root.
	if rb, ok := s.svc.Runbook(a.Runbook); a.Dir != "" || !ok || rb.Dir == "" {
		d, err := s.dir(a.Dir)
		if err != nil {
			fail(w, err)
			return
		}
		a.Dir = d
	}
	v, err := s.svc.ExecAndWait(r.Context(), a)
	if err != nil {
		fail(w, err)
		return
	}
	code := 200
	switch v.State {
	case store.StatePending:
		code = 202
	case store.StateDenied:
		code = 403
	}
	writeJSON(w, code, v)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, p libauth.Principal) {
	q := r.URL.Query()
	limit := 20
	fmt.Sscanf(q.Get("limit"), "%d", &limit) //nolint:errcheck // a bad limit keeps the default
	runs, err := s.svc.ListFor(p.ID, p.HasScope(service.ScopeReadAll), q.Get("session"), q.Get("state"), min(max(limit, 1), 200))
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]*service.RunView, len(runs))
	for i, run := range runs {
		out[i] = s.svc.View(r.Context(), run, 0)
	}
	writeJSON(w, 200, map[string]any{"runs": out})
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, p libauth.Principal) {
	run, err := s.svc.GetFor(r.PathValue("id"), p.ID, p.HasScope(service.ScopeReadAll))
	if err != nil {
		fail(w, err)
		return
	}
	n := 0
	fmt.Sscanf(r.URL.Query().Get("max_bytes"), "%d", &n) //nolint:errcheck // absent means default
	writeJSON(w, 200, s.svc.View(r.Context(), run, service.ClampBytes(n)))
}

func (s *Server) output(w http.ResponseWriter, r *http.Request, p libauth.Principal) {
	q := r.URL.Query()
	attempt, off, n := 0, 0, 0
	fmt.Sscanf(q.Get("attempt"), "%d", &attempt) //nolint:errcheck // absent means the last attempt
	fmt.Sscanf(q.Get("offset"), "%d", &off)      //nolint:errcheck // absent means 0
	fmt.Sscanf(q.Get("max_bytes"), "%d", &n)     //nolint:errcheck // absent means default
	b, err := s.svc.RecallFor(r.Context(), r.PathValue("id"), q.Get("stream"), attempt, p.ID, p.HasScope(service.ScopeReadAll))
	if err != nil {
		fail(w, err)
		return
	}
	off = min(max(off, 0), len(b))
	end := min(off+service.ClampBytes(n), len(b))
	writeJSON(w, 200, map[string]any{"output": string(b[off:end]), "offset": off, "returned": end - off, "total": len(b), "more": end < len(b), "output_trust": "untrusted"})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request, p libauth.Principal) {
	run, err := s.svc.Cancel(r.Context(), r.PathValue("id"), p.HasScope(service.ScopeAdmin))
	if err != nil {
		if run != nil && !errors.Is(err, service.ErrForbidden) {
			writeErr(w, 409, "conflict", err.Error())
			return
		}
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.svc.View(r.Context(), run, 0))
}

func (s *Server) pending(w http.ResponseWriter, r *http.Request, _ libauth.Principal) {
	runs, err := s.svc.Pending()
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]*service.RunView, len(runs))
	for i, run := range runs {
		out[i] = s.svc.View(r.Context(), run, 0)
	}
	writeJSON(w, 200, map[string]any{"pending": out})
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request, _ libauth.Principal) {
	run, err := s.svc.Approve(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.svc.View(r.Context(), run, 0))
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request, _ libauth.Principal) {
	run, err := s.svc.Reject(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.svc.View(r.Context(), run, 0))
}

func (s *Server) runbooks(w http.ResponseWriter, _ *http.Request, _ libauth.Principal) {
	writeJSON(w, 200, map[string]any{"runbooks": s.svc.Runbooks()})
}
