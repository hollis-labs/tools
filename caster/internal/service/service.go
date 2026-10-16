// Package service is the service layer of caster: the one place business rules
// live, and the only thing the transports (internal/cli, a future internal/api
// or internal/mcp) call. See docs/transport-boundary.md.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/hollis-labs/caster/internal/ref"
	"github.com/hollis-labs/caster/internal/resolver"
	"github.com/hollis-labs/caster/internal/store"
)

// DefaultNS is the namespace used when a bare name is given.
const DefaultNS = "scratch"

// Service is the root of the service layer.
type Service struct {
	store *store.Store
	reg   *resolver.Registry
}

// Store returns the underlying store, for application-service code that
// composes across domain services. Transports must not use it.
func (s *Service) Store() *store.Store { return s.store }

// New constructs a Service wired to the provided store and extra resolvers.
// The local cas:// and caster:// resolver is always registered.
func New(st *store.Store, extra ...resolver.Resolver) *Service {
	rs := append([]resolver.Resolver{resolver.Local{Store: st}}, extra...)
	return &Service{store: st, reg: resolver.NewRegistry(rs...)}
}

// PutInput describes something to store.
type PutInput struct {
	Name   string // "ns/name" or "name" (DefaultNS); empty = anonymous blob
	Title  string
	Tags   string // space separated
	Mime   string
	Source string // provenance: the ref this content came from
	R      io.Reader
}

// PutResult is what Put stored.
type PutResult struct {
	Blob store.Blob
	Name *store.Name
	Ref  ref.Ref // the ref to hand around: caster://... when named, else cas://...
}

// Put stores content and, when named, advances the name to it.
func (s *Service) Put(in PutInput) (PutResult, error) {
	var ns, name string
	if in.Name != "" {
		ns, name = DefaultNS, in.Name
		if strings.Contains(in.Name, "/") {
			ns, name, _ = strings.Cut(in.Name, "/")
		}
		if _, err := ref.Parse("caster://" + ns + "/" + name); err != nil {
			return PutResult{}, err
		}
	}
	b, err := s.store.PutBlob(in.R, in.Mime, in.Source)
	if err != nil {
		return PutResult{}, err
	}
	res := PutResult{Blob: b, Ref: ref.Ref{Scheme: ref.SchemeCAS, Auth: "sha256", Path: b.Hash}}
	if name != "" {
		n, err := s.store.Link(ns, name, b.Hash, in.Title, in.Tags)
		if err != nil {
			return PutResult{}, err
		}
		res.Name = &n
		res.Ref = ref.Ref{Scheme: ref.SchemeCaster, Auth: ns, Path: name, Rev: n.Rev}
	}
	return res, nil
}

// Get materialises any ref.
func (s *Service) Get(ctx context.Context, raw string) (resolver.Content, error) {
	r, err := ref.Parse(raw)
	if err != nil {
		return resolver.Content{}, err
	}
	return s.reg.Materialize(ctx, r)
}

// Head resolves any ref to metadata only.
func (s *Service) Head(ctx context.Context, raw string) (resolver.Meta, error) {
	r, err := ref.Parse(raw)
	if err != nil {
		return resolver.Meta{}, err
	}
	return s.reg.Resolve(ctx, r)
}

// Snapshot materialises a (possibly floating, possibly remote) ref and stores
// the bytes in CAS, recording where and when they came from. The result is a
// pinned cas:// ref; with a name it also advances that name.
func (s *Service) Snapshot(ctx context.Context, raw, name, tags string) (PutResult, resolver.Meta, error) {
	c, err := s.Get(ctx, raw)
	if err != nil {
		return PutResult{}, resolver.Meta{}, err
	}
	if c.State != resolver.StateResolved {
		return PutResult{}, c.Meta, fmt.Errorf("%s: %s", c.Ref, c.State)
	}
	src := c.Ref.String()
	if c.Revision != "" {
		src += " rev=" + c.Revision
	}
	if c.ContentHash != "" && c.Ref.Scheme != ref.SchemeCAS {
		src += " checksum=" + c.ContentHash
	}
	res, err := s.Put(PutInput{Name: name, Title: c.Title, Tags: tags, Mime: c.Mime, Source: src, R: strings.NewReader(string(c.Data))})
	return res, c.Meta, err
}

// List returns the latest revision of named items.
func (s *Service) List(ns, tag string) ([]store.Name, error) { return s.store.List(ns, tag) }

// History returns every revision of a name, newest first.
func (s *Service) History(name string) ([]store.Name, error) {
	ns, n := DefaultNS, name
	if strings.Contains(name, "/") {
		ns, n, _ = strings.Cut(name, "/")
	}
	h, err := s.store.History(ns, n)
	if err == nil && len(h) == 0 {
		return nil, store.ErrNotFound
	}
	return h, err
}

// Search is a full-text search over stored text.
func (s *Service) Search(q string, limit int) ([]store.Hit, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.store.Search(q, limit)
}

// Found is a detected ref and what its resolver said about it.
type Found struct {
	ref.Candidate
	Meta resolver.Meta
	Err  error
}

// Scan detects refs in text and validates each with its resolver. Unresolved
// candidates are returned with their state so callers can stay quiet about them.
func (s *Service) Scan(ctx context.Context, text string) []Found {
	var out []Found
	for _, c := range ref.Detect(text) {
		m, err := s.reg.Resolve(ctx, c.Ref)
		out = append(out, Found{Candidate: c, Meta: m, Err: err})
	}
	return out
}

// Expand replaces each resolved ref in text with its contents, wrapped in a
// labelled block. The wrapper marks the content as untrusted data from its
// source and records the revision actually used. budget caps each insertion in
// bytes (0 = 16 KiB); unresolved refs are left exactly as written.
func (s *Service) Expand(ctx context.Context, text string, budget int) (string, []Found) {
	if budget <= 0 {
		budget = 16 << 10
	}
	found := s.Scan(ctx, text)
	var b strings.Builder
	last := 0
	for i, f := range found {
		if f.Err != nil || f.Meta.State != resolver.StateResolved {
			continue
		}
		c, err := s.reg.Materialize(ctx, f.Ref)
		if err != nil || c.State != resolver.StateResolved {
			continue
		}
		body := string(c.Data)
		trunc := ""
		if len(body) > budget {
			body, trunc = strings.ToValidUTF8(body[:budget], ""), " truncated=\"true\""
		}
		b.WriteString(text[last:f.Start])
		fmt.Fprintf(&b, "<caster-ref ref=%s%s%s trust=\"untrusted\">\n%s\n</caster-ref>",
			strconv.Quote(f.Ref.String()), attr("rev", c.Revision), trunc+attr("sha256", c.ContentHash), strings.TrimRight(body, "\n"))
		last = f.End
		found[i].Meta = c.Meta
	}
	b.WriteString(text[last:])
	return b.String(), found
}

func attr(k, v string) string {
	if v == "" {
		return ""
	}
	return " " + k + "=" + strconv.Quote(v)
}

// IsNotFound reports whether err is a store not-found.
func IsNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
