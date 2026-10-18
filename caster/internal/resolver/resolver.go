// Package resolver turns refs into metadata and content. One resolver per
// scheme. Resolve is metadata-only in spirit: an unknown ref is a successful
// "not_found" outcome, not an error. Errors mean the resolver could not answer.
package resolver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/hollis-labs/caster/internal/ref"
)

// States a resolver reports.
const (
	StateResolved = "resolved"
	StateNotFound = "not_found"
	StateDenied   = "denied"
)

// ErrUnavailable means the backing system could not be reached.
var ErrUnavailable = errors.New("unavailable")

// Meta describes a resolved ref without carrying its content.
type Meta struct {
	Ref         ref.Ref
	State       string
	Kind        string
	Title       string
	Mime        string
	Size        int64
	Revision    string
	ContentHash string // sha256 of the materialized bytes, when known
}

// Content is a materialized ref.
type Content struct {
	Meta
	Data []byte
}

// Resolver resolves one scheme.
type Resolver interface {
	Schemes() []string
	Resolve(ctx context.Context, r ref.Ref) (Meta, error)
	Materialize(ctx context.Context, r ref.Ref) (Content, error)
}

// Registry dispatches by scheme.
type Registry struct{ byScheme map[string]Resolver }

// NewRegistry builds a registry from resolvers.
func NewRegistry(rs ...Resolver) *Registry {
	reg := &Registry{byScheme: map[string]Resolver{}}
	for _, r := range rs {
		for _, s := range r.Schemes() {
			reg.byScheme[s] = r
		}
	}
	return reg
}

func (g *Registry) get(r ref.Ref) (Resolver, error) {
	res, ok := g.byScheme[r.Scheme]
	if !ok {
		return nil, fmt.Errorf("no resolver for scheme %q", r.Scheme)
	}
	return res, nil
}

// Resolve resolves r.
func (g *Registry) Resolve(ctx context.Context, r ref.Ref) (Meta, error) {
	res, err := g.get(r)
	if err != nil {
		return Meta{}, err
	}
	return res.Resolve(ctx, r)
}

// Materialize returns r's content.
func (g *Registry) Materialize(ctx context.Context, r ref.Ref) (Content, error) {
	res, err := g.get(r)
	if err != nil {
		return Content{}, err
	}
	return res.Materialize(ctx, r)
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
