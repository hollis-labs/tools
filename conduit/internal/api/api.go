// Package api is conduit's HTTP transport: the addressable "run this on this
// host" surface. Every route authenticates a bearer token and checks a scope
// bound to this host; every run is attributed to the token's principal and
// passes the same policy as a local one. The approval routes need a scope that
// agent tokens are not given, and a principal cannot approve its own run.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/core/panics/httpguard"
	"github.com/hollis-labs/tools/conduit/internal/runbook"
	"github.com/hollis-labs/tools/conduit/internal/service"
	"github.com/hollis-labs/tools/conduit/internal/store"
)

const maxBody = 1 << 20

// Config configures the handler.
type Config struct {
	// AllowDirs are the roots a caller's working directory must sit under. A
	// request with no dir runs in the first one. At least one is required.
	AllowDirs []string
	// RatePerMinute caps requests per principal (default 240).
	RatePerMinute int
}

type server struct {
	svc  *service.Service
	cfg  Config
	host string

	mu       sync.Mutex
	window   map[string]*bucket // per principal
	failures map[string]*bucket // per remote address, failed auth only
}

type bucket struct {
	start time.Time
	n     int
}

// New returns the API handler and starts the loop that executes runs approved
// from outside this process (the operator's CLI) for as long as ctx lives.
func New(ctx context.Context, svc *service.Service, cfg Config) (http.Handler, error) {
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
	s := &server{svc: svc, cfg: cfg, host: svc.Host(), window: map[string]*bucket{}, failures: map[string]*bucket{}}

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// contained per tick: a panic in one pass must not end the loop
				_ = panics.Call("api start-approved", func() error {
					for _, p := range s.owners() {
						_, _ = svc.StartApproved(p)
					}
					return nil
				})
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "host": s.host})
	})
	mux.Handle("POST /v1/check", s.auth(service.ScopeReadOwn, s.check))
	mux.Handle("POST /v1/exec", s.auth(service.ScopeRun, s.exec))
	mux.Handle("GET /v1/runs", s.auth(service.ScopeReadOwn, s.list))
	mux.Handle("GET /v1/runs/{id}", s.auth(service.ScopeReadOwn, s.get))
	mux.Handle("GET /v1/runs/{id}/output", s.auth(service.ScopeReadOwn, s.output))
	mux.Handle("POST /v1/runs/{id}/cancel", s.auth(service.ScopeRun, s.cancel))
	mux.Handle("GET /v1/approvals", s.auth(service.ScopeApprove, s.pending))
	mux.Handle("POST /v1/runs/{id}/approve", s.auth(service.ScopeApprove, s.approve))
	mux.Handle("POST /v1/runs/{id}/reject", s.auth(service.ScopeApprove, s.reject))
	mux.Handle("GET /v1/runbooks", s.auth(service.ScopeReadOwn, s.runbooks))
	return httpguard.Handler(mux), nil
}

// owners are the principals whose approved runs this daemon starts: every token
// principal. (Runs of other principals, such as an MCP server, are started by
// their own process.)
func (s *server) owners() []string {
	toks, err := s.svc.Tokens()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		out = append(out, "token:"+t.Name)
	}
	return out
}

type handler func(w http.ResponseWriter, r *http.Request, p *service.Principal)

func (s *server) auth(scope string, h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remote := remoteKey(r.RemoteAddr)
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		var p *service.Principal
		err := service.ErrUnauthenticated
		if ok {
			p, err = s.svc.Authenticate(tok)
		}
		if err != nil {
			if errors.Is(err, service.ErrUnauthenticated) {
				// Only failures are throttled: a valid token is never locked out by
				// someone else's bad guesses from the same address.
				if s.throttled(s.failures, remote, 10) {
					writeErr(w, 429, "rate_limited", "too many failed authentication attempts")
					return
				}
				s.count(s.failures, remote)
				w.Header().Set("WWW-Authenticate", `Bearer realm="conduit"`)
				writeErr(w, 401, "unauthenticated", "a valid bearer token is required")
				return
			}
			writeErr(w, 500, "internal", "authentication failed")
			return
		}
		if !p.Can(scope, s.host) {
			writeErr(w, 403, "forbidden", fmt.Sprintf("token %q lacks scope %s for host %s", p.Name, scope, s.host))
			return
		}
		if s.throttled(s.window, p.Name, s.cfg.RatePerMinute) {
			writeErr(w, 429, "rate_limited", "request rate exceeded")
			return
		}
		s.count(s.window, p.Name)
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		h(w, r.WithContext(service.As(r.Context(), p.ID())), p)
	})
}

// remoteKey keys throttling by client address without the ephemeral port, so
// opening a new connection does not reset the count.
func remoteKey(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func (s *server) throttled(m map[string]*bucket, key string, limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := m[key]
	return b != nil && time.Since(b.start) < time.Minute && b.n >= limit
}

func (s *server) count(m map[string]*bucket, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := m[key]
	if b == nil || time.Since(b.start) >= time.Minute {
		if len(m) > 4096 { // drop expired windows so the maps cannot grow without bound
			for k, v := range m {
				if time.Since(v.start) >= time.Minute {
					delete(m, k)
				}
			}
		}
		b = &bucket{start: time.Now()}
		m[key] = b
	}
	b.n++
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
func (s *server) dir(d string) (string, error) { return service.ResolveDir(s.cfg.AllowDirs, d) }

func (s *server) check(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
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

func (s *server) exec(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
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

func (s *server) list(w http.ResponseWriter, r *http.Request, p *service.Principal) {
	q := r.URL.Query()
	limit := 20
	fmt.Sscanf(q.Get("limit"), "%d", &limit) //nolint:errcheck // a bad limit keeps the default
	runs, err := s.svc.ListFor(p.ID(), p.SeesAll(s.host), q.Get("session"), q.Get("state"), min(max(limit, 1), 200))
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

func (s *server) get(w http.ResponseWriter, r *http.Request, p *service.Principal) {
	run, err := s.svc.GetFor(r.PathValue("id"), p.ID(), p.SeesAll(s.host))
	if err != nil {
		fail(w, err)
		return
	}
	n := 0
	fmt.Sscanf(r.URL.Query().Get("max_bytes"), "%d", &n) //nolint:errcheck // absent means default
	writeJSON(w, 200, s.svc.View(r.Context(), run, service.ClampBytes(n)))
}

func (s *server) output(w http.ResponseWriter, r *http.Request, p *service.Principal) {
	q := r.URL.Query()
	attempt, off, n := 0, 0, 0
	fmt.Sscanf(q.Get("attempt"), "%d", &attempt) //nolint:errcheck // absent means the last attempt
	fmt.Sscanf(q.Get("offset"), "%d", &off)      //nolint:errcheck // absent means 0
	fmt.Sscanf(q.Get("max_bytes"), "%d", &n)     //nolint:errcheck // absent means default
	b, err := s.svc.RecallFor(r.Context(), r.PathValue("id"), q.Get("stream"), attempt, p.ID(), p.SeesAll(s.host))
	if err != nil {
		fail(w, err)
		return
	}
	off = min(max(off, 0), len(b))
	end := min(off+service.ClampBytes(n), len(b))
	writeJSON(w, 200, map[string]any{"output": string(b[off:end]), "offset": off, "returned": end - off, "total": len(b), "more": end < len(b), "output_trust": "untrusted"})
}

func (s *server) cancel(w http.ResponseWriter, r *http.Request, p *service.Principal) {
	run, err := s.svc.Cancel(r.Context(), r.PathValue("id"), p.Can(service.ScopeAdmin, s.host))
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

func (s *server) pending(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
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

func (s *server) approve(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	run, err := s.svc.Approve(r.Context(), r.PathValue("id"), "api")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.svc.View(r.Context(), run, 0))
}

func (s *server) reject(w http.ResponseWriter, r *http.Request, _ *service.Principal) {
	run, err := s.svc.Reject(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.svc.View(r.Context(), run, 0))
}

func (s *server) runbooks(w http.ResponseWriter, _ *http.Request, _ *service.Principal) {
	writeJSON(w, 200, map[string]any{"runbooks": s.svc.Runbooks()})
}
