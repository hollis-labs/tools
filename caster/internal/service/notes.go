package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/hollis-labs/tools/caster/internal/ref"
	"github.com/hollis-labs/tools/caster/internal/store"
)

// Notes is the domain service for stored text: blobs, named items and their
// revisions, and search over them.
type Notes struct {
	store *store.Store
}

// Put stores content and, when named, advances the name to it.
func (s *Notes) Put(ctx context.Context, req PutRequest) (PutResult, error) {
	if err := require(ctx, ScopeWrite); err != nil {
		return PutResult{}, err
	}
	var ns, name string
	if req.Name != "" {
		ns, name = splitName(req.Name)
		if _, err := ref.Parse("caster://" + ns + "/" + name); err != nil {
			return PutResult{}, invalid(err)
		}
	}
	r := req.R
	if r == nil {
		r = strings.NewReader(req.Content)
	}
	b, err := s.store.PutBlob(r, req.Mime, req.Source)
	if err != nil {
		return PutResult{}, classify(err)
	}
	res := PutResult{Ref: "cas://sha256/" + b.Hash, CAS: "cas://sha256/" + b.Hash, Size: b.Size}
	if name != "" {
		n, err := s.store.Link(ns, name, b.Hash, req.Title, req.Tags)
		if err != nil {
			return PutResult{}, classify(err)
		}
		res.Ref = ref.Ref{Scheme: ref.SchemeCaster, Auth: ns, Path: name, Rev: n.Rev}.String()
	}
	return res, nil
}

// List returns the latest revision of named items.
func (s *Notes) List(ctx context.Context, req ListRequest) (ListResult, error) {
	if err := require(ctx, ScopeRead); err != nil {
		return ListResult{}, err
	}
	names, err := s.store.List(req.NS, req.Tag)
	if err != nil {
		return ListResult{}, classify(err)
	}
	out := ListResult{Items: make([]Item, 0, len(names))}
	for _, n := range names {
		out.Items = append(out.Items, Item{Ref: "caster://" + n.NS + "/" + n.Name, Rev: n.Rev, Title: n.Title,
			Tags: n.Tags, CAS: "cas://sha256/" + n.Hash, Updated: n.CreatedAt})
	}
	return out, nil
}

// Log returns every revision of a name, newest first.
func (s *Notes) Log(ctx context.Context, req LogRequest) (LogResult, error) {
	if err := require(ctx, ScopeRead); err != nil {
		return LogResult{}, err
	}
	ns, n := splitName(req.Name)
	h, err := s.store.History(ns, n)
	if err != nil {
		return LogResult{}, classify(err)
	}
	if len(h) == 0 {
		return LogResult{}, classify(store.ErrNotFound)
	}
	out := LogResult{Name: ns + "/" + n, Revisions: make([]Revision, 0, len(h))}
	for _, r := range h {
		out.Revisions = append(out.Revisions, Revision{Rev: r.Rev, CAS: "cas://sha256/" + r.Hash, Title: r.Title, At: r.CreatedAt})
	}
	return out, nil
}

// Search is a full-text search over stored text.
func (s *Notes) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	if err := require(ctx, ScopeRead); err != nil {
		return SearchResult{}, err
	}
	if strings.TrimSpace(req.Query) == "" {
		return SearchResult{}, invalid(fmt.Errorf("empty query"))
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}
	hits, err := s.store.Search(req.Query, req.Limit)
	if err != nil {
		return SearchResult{}, invalid(err) // FTS syntax errors are the caller's
	}
	out := SearchResult{Hits: make([]Hit, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, Hit{CAS: "cas://sha256/" + h.Hash, Names: h.Names, Snippet: h.Snippet})
	}
	return out, nil
}

func splitName(name string) (ns, n string) {
	if ns, n, ok := strings.Cut(name, "/"); ok {
		return ns, n
	}
	return DefaultNS, name
}
