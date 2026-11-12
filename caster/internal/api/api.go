// Package api is Caster's HTTP transport: JSON over a small set of routes under /api/, one per
// service operation. Handlers decode the request, call the service layer and encode the result.
// The route's coarse scope is declared in Routes and enforced by the auth guard; what an identity may
// do with a ref (local or remote-backed) is decided by the service layer, not here.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/hollis-labs/substrate/apierr"

	"github.com/hollis-labs/tools/caster/internal/service"
)

const maxBody = 8 << 20

// Handler serves the API routes.
type Handler struct{ svc *service.Service }

// New returns the API over svc.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// Mux returns the routes for httphost.Options.Routes. The guard, the Host check and the CSRF check
// are outside it.
func (h *Handler) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, rt := range h.routes() {
		mux.HandleFunc(rt.Method+" "+rt.Pattern, rt.handler)
	}
	return mux
}

func (h *Handler) routes() []route {
	svc := h.svc
	return []route{
		{http.MethodGet, "/api/whoami", ScopeAuthenticated, func(w http.ResponseWriter, r *http.Request) {
			res, err := svc.Identity.Whoami(r.Context(), struct{}{})
			respond(w, "", res, err)
		}},
		{http.MethodGet, "/api/head", ScopeAuthenticated, func(w http.ResponseWriter, r *http.Request) {
			res, err := svc.Refs.Head(r.Context(), service.RefRequest{Ref: r.URL.Query().Get("ref")})
			respond(w, res.State, res, err)
		}},
		{http.MethodGet, "/api/get", ScopeAuthenticated, h.get},
		{http.MethodGet, "/api/list", service.ScopeRead, func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			res, err := svc.Notes.List(r.Context(), service.ListRequest{NS: q.Get("ns"), Tag: q.Get("tag")})
			respond(w, "", res, err)
		}},
		{http.MethodGet, "/api/log", service.ScopeRead, func(w http.ResponseWriter, r *http.Request) {
			res, err := svc.Notes.Log(r.Context(), service.LogRequest{Name: r.URL.Query().Get("name")})
			respond(w, "", res, err)
		}},
		{http.MethodGet, "/api/search", service.ScopeRead, func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			limit, _ := strconv.Atoi(q.Get("limit"))
			res, err := svc.Notes.Search(r.Context(), service.SearchRequest{Query: q.Get("q"), Limit: limit})
			respond(w, "", res, err)
		}},
		{http.MethodGet, "/api/actions", ScopeAuthenticated, func(w http.ResponseWriter, r *http.Request) {
			res, err := svc.Actions.List(r.Context(), service.ActionsRequest{Ref: r.URL.Query().Get("ref")})
			respond(w, res.State, res, err)
		}},
		{http.MethodGet, "/api/report", service.ScopeAdmin, func(w http.ResponseWriter, r *http.Request) {
			var req service.ReportRequest
			if d, err := time.ParseDuration(r.URL.Query().Get("since")); err == nil && d > 0 {
				req.Since = time.Now().Add(-d)
			}
			res, err := svc.Shadow.Report(r.Context(), req)
			respond(w, "", res, err)
		}},
		{http.MethodPost, "/api/put", service.ScopeWrite, post(svc.Notes.Put, func(service.PutResult) string { return "" })},
		{http.MethodPost, "/api/snap", ScopeAuthenticated, post(svc.Refs.Snapshot, func(r service.SnapResult) string { return r.State })},
		{http.MethodPost, "/api/scan", ScopeAuthenticated, post(svc.Refs.Scan, func(service.ScanResult) string { return "" })},
		{http.MethodPost, "/api/expand", ScopeAuthenticated, post(svc.Refs.Expand, func(service.ExpandResult) string { return "" })},
		{http.MethodPost, "/api/act", service.ScopeAct, post(svc.Actions.Run, func(r service.ActResult) string { return r.State })},
		{http.MethodPost, "/api/observe", service.ScopeObserve, post(svc.Shadow.Observe, func(service.ObserveResult) string { return "" })},
		{http.MethodPost, "/api/label", service.ScopeAdmin, post(svc.Shadow.Label, func(service.LabelResult) string { return "" })},
	}
}

// get serves a ref. With raw=1 a resolved ref is sent as plain text, never as the stored mime, so
// stored text cannot render as HTML.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	maxBytes, _ := strconv.Atoi(q.Get("max_bytes"))
	res, err := h.svc.Refs.Get(r.Context(), service.GetRequest{Ref: q.Get("ref"), MaxBytes: maxBytes})
	if err == nil && res.State == service.StateResolved && q.Get("raw") == "1" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Caster-Ref", res.Ref)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(res.Data)
		return
	}
	respond(w, res.State, res, err)
}

// post adapts a service method to a JSON-body handler. state extracts the resolution state of a
// result, "" when the operation has none.
func post[Q, R any](fn func(context.Context, Q) (R, error), state func(R) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var q Q
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
		if err := dec.Decode(&q); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				fail(w, http.StatusRequestEntityTooLarge, service.KindInvalid, "request body too large")
				return
			}
			fail(w, http.StatusBadRequest, service.KindInvalid, "invalid JSON body: "+err.Error())
			return
		}
		res, err := fn(r.Context(), q)
		respond(w, state(res), res, err)
	}
}

// respond writes a result. A ref that is not resolved is an outcome that still carries its body
// (404 not_found, 403 denied), distinct from an error, which is the flat {code, message} body.
func respond(w http.ResponseWriter, state string, body any, err error) {
	if err != nil {
		fail(w, statusOfKind(service.KindOf(err)), service.KindOf(err), err.Error())
		return
	}
	switch state {
	case service.StateNotFound:
		write(w, http.StatusNotFound, body)
	case service.StateDenied:
		write(w, http.StatusForbidden, body)
	default:
		write(w, http.StatusOK, body)
	}
}

func statusOfKind(kind string) int {
	switch kind {
	case service.KindUnauthenticated:
		return http.StatusUnauthorized
	case service.KindInvalid:
		return http.StatusBadRequest
	case service.KindNotFound:
		return http.StatusNotFound
	case service.KindDenied:
		return http.StatusForbidden
	case service.KindUnavailable:
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// fail writes the framework's flat {code, message} error body.
func fail(w http.ResponseWriter, status int, code, msg string) {
	apierr.Write(w, status, code, msg, nil)
}

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
