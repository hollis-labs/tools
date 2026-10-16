// Package api is caster's HTTP transport: JSON over a small set of routes, one
// per service operation. Handlers decode the request, call the service layer
// and encode the result; authentication and scope checks are the guard
// middleware's job (see Scopes), not the handlers'.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/caster/internal/service"
)

const maxBody = 8 << 20

// Scopes is the route → scope table, derived from the service's operation scopes.
func Scopes() *auth.ScopeTable {
	return auth.NewScopeTable().
		Route("GET", "/v1/head", service.ScopeFor(service.OpHead)).
		Route("GET", "/v1/get", service.ScopeFor(service.OpGet)).
		Route("GET", "/v1/list", service.ScopeFor(service.OpList)).
		Route("GET", "/v1/log", service.ScopeFor(service.OpLog)).
		Route("GET", "/v1/search", service.ScopeFor(service.OpSearch)).
		Route("GET", "/v1/actions", service.ScopeFor(service.OpActions)).
		Route("POST", "/v1/act", service.ScopeFor(service.OpAct)).
		Route("POST", "/v1/scan", service.ScopeFor(service.OpScan)).
		Route("POST", "/v1/expand", service.ScopeFor(service.OpExpand)).
		Route("POST", "/v1/put", service.ScopeFor(service.OpPut)).
		Route("POST", "/v1/snap", service.ScopeFor(service.OpSnap))
}

// Handler serves the API. guard may be nil only in tests; the server always
// passes one.
func Handler(svc *service.Service, guard *auth.Guard) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		status := "auth=none"
		if guard != nil {
			status = guard.Status()
		}
		write(w, http.StatusOK, map[string]string{"status": "ok", "auth": status})
	})
	mux.HandleFunc("GET /v1/head", func(w http.ResponseWriter, r *http.Request) {
		res, err := svc.Refs.Head(r.Context(), service.RefRequest{Ref: r.URL.Query().Get("ref")})
		respond(w, res.State, res, err)
	})
	mux.HandleFunc("GET /v1/get", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		max, _ := strconv.Atoi(q.Get("max_bytes"))
		res, err := svc.Refs.Get(r.Context(), service.GetRequest{Ref: q.Get("ref"), MaxBytes: max})
		if err == nil && res.State == service.StateResolved && q.Get("raw") == "1" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8") // never the stored mime: stored text must not render as HTML
			w.Header().Set("X-Caster-Ref", res.Ref)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(res.Data)
			return
		}
		respond(w, res.State, res, err)
	})
	mux.HandleFunc("GET /v1/list", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res, err := svc.Notes.List(r.Context(), service.ListRequest{NS: q.Get("ns"), Tag: q.Get("tag")})
		respond(w, "", res, err)
	})
	mux.HandleFunc("GET /v1/log", func(w http.ResponseWriter, r *http.Request) {
		res, err := svc.Notes.Log(r.Context(), service.LogRequest{Name: r.URL.Query().Get("name")})
		respond(w, "", res, err)
	})
	mux.HandleFunc("GET /v1/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		res, err := svc.Notes.Search(r.Context(), service.SearchRequest{Query: q.Get("q"), Limit: limit})
		respond(w, "", res, err)
	})
	mux.HandleFunc("GET /v1/actions", func(w http.ResponseWriter, r *http.Request) {
		res, err := svc.Actions.List(r.Context(), service.ActionsRequest{Ref: r.URL.Query().Get("ref")})
		respond(w, res.State, res, err)
	})
	mux.HandleFunc("POST /v1/act", post(svc.Actions.Run, func(r service.ActResult) string { return r.State }))
	mux.HandleFunc("POST /v1/put", post(svc.Notes.Put, func(service.PutResult) string { return "" }))
	mux.HandleFunc("POST /v1/snap", post(svc.Refs.Snapshot, func(r service.SnapResult) string { return r.State }))
	mux.HandleFunc("POST /v1/scan", post(svc.Refs.Scan, func(service.ScanResult) string { return "" }))
	mux.HandleFunc("POST /v1/expand", post(svc.Refs.Expand, func(service.ExpandResult) string { return "" }))
	if guard == nil {
		return mux
	}
	return guard.Middleware(mux)
}

// post adapts a service method to a JSON-body handler. state extracts the
// resolution state of a result, "" when the operation has none.
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

// respond writes a result. A non-resolved resolution state is an outcome that
// still carries the body (404/403), distinct from an error.
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

func fail(w http.ResponseWriter, status int, code, msg string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
