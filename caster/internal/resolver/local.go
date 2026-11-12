package resolver

import (
	"context"
	"errors"
	"strconv"

	"github.com/hollis-labs/tools/caster/internal/ref"
	"github.com/hollis-labs/tools/caster/internal/storage"
)

// Local resolves cas:// and caster:// refs from the local store.
type Local struct{ Store *storage.Storage }

// Schemes implements Resolver.
func (Local) Schemes() []string { return []string{ref.SchemeCAS, ref.SchemeCaster} }

func (l Local) target(ctx context.Context, r ref.Ref) (hash, title string, rev int, found bool, err error) {
	if r.Scheme == ref.SchemeCAS {
		return r.Path, "", 0, true, nil
	}
	n, err := l.Store.Lookup(ctx, r.Auth, r.Path, r.Rev)
	if errors.Is(err, storage.ErrNotFound) {
		return "", "", 0, false, nil
	}
	if err != nil {
		return "", "", 0, false, err
	}
	return n.Hash, n.Title, n.Rev, true, nil
}

// Resolve implements Resolver.
func (l Local) Resolve(ctx context.Context, r ref.Ref) (Meta, error) {
	m := Meta{Ref: r, State: StateNotFound}
	hash, title, rev, ok, err := l.target(ctx, r)
	if err != nil || !ok {
		return m, err
	}
	b, err := l.Store.HeadBlob(ctx, hash)
	if errors.Is(err, storage.ErrNotFound) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	m.State, m.Kind, m.Title, m.Mime, m.Size, m.ContentHash = StateResolved, "blob", title, b.Mime, b.Size, hash
	if r.Scheme == ref.SchemeCaster {
		m.Kind, m.Revision = "note", strconv.Itoa(rev)
	}
	return m, nil
}

// Materialize implements Resolver.
func (l Local) Materialize(ctx context.Context, r ref.Ref) (Content, error) {
	m, err := l.Resolve(ctx, r)
	if err != nil || m.State != StateResolved {
		return Content{Meta: m}, err
	}
	data, _, err := l.Store.OpenBlob(ctx, m.ContentHash)
	if errors.Is(err, storage.ErrNotFound) {
		m.State = StateNotFound
		return Content{Meta: m}, nil
	}
	return Content{Meta: m, Data: data}, err
}
