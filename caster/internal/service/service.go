// Package service is the service layer of caster: the one place business rules
// live, and the only thing the transports call (internal/cli, internal/mcp,
// internal/api). It is organized as domain services held by Service, each
// method taking a request struct and returning a result struct with JSON tags,
// so a transport is decode → call → encode. See docs/transport-boundary.md.
package service

import (
	"time"

	"github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/tools/caster/internal/resolver"
	"github.com/hollis-labs/tools/caster/internal/store"
)

// DefaultNS is the namespace used when a bare name is given.
const DefaultNS = "scratch"

// Service is the root of the service layer. Transports call the domain
// services: Notes (stored text), Refs (resolve, fetch, snapshot, detect),
// Actions (registered operations on refs), Shadow (detection measured
// without surfacing anything), Access (pairing and device tokens).
type Service struct {
	Notes   NotesAPI
	Refs    RefsAPI
	Actions ActionsAPI
	Shadow  ShadowAPI
	Access  AccessAPI

	// access is the local implementation, kept for the composition root, which
	// needs its Verifier. Nil on a remote Service.
	access *Access

	// store is the underlying store. It is not exported so every caller funnels
	// through the domain services where business rules apply; Store() is the
	// explicit escape hatch for application-service code that composes across
	// domain services. Transports must not use it (.golangci.transport.yml).
	store *store.Store
}

// Verifier returns the device-token verifier for the HTTP transport's chain, or
// nil when this Service is a remote client with no store of its own.
func (s *Service) Verifier() auth.Verifier {
	if s.access == nil {
		return nil
	}
	return s.access.Verifier()
}

// Store returns the underlying store, for application-service code that
// composes across domain services. It is the sanctioned crossing point, not a
// convenience for transports.
func (s *Service) Store() *store.Store { return s.store }

// New constructs a Service wired to the provided store and extra resolvers.
// The local cas:// and caster:// resolver is always registered.
func New(st *store.Store, extra ...resolver.Resolver) *Service {
	rs := append([]resolver.Resolver{resolver.Local{Store: st}}, extra...)
	notes := &Notes{store: st}
	refs := &Refs{reg: resolver.NewRegistry(rs...), notes: notes}
	var w WorkspaceWriter
	for _, r := range extra {
		if ww, ok := r.(WorkspaceWriter); ok {
			w = ww
		}
	}
	access := &Access{store: st, now: time.Now}
	return &Service{
		Notes:   notes,
		Refs:    refs,
		Actions: &Actions{store: st, refs: refs, writer: w},
		Shadow:  &Shadow{store: st, refs: refs},
		Access:  access,
		access:  access,
		store:   st,
	}
}
