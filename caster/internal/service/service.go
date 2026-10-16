// Package service is the service layer of caster: the one place business rules
// live, and the only thing the transports call (internal/cli, internal/mcp,
// internal/api). Every method takes a request struct and returns a result
// struct with JSON tags, so a transport is decode → call → encode. See
// docs/transport-boundary.md.
package service

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

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

func parseRef(raw string) (ref.Ref, error) {
	r, err := ref.Parse(raw)
	if err != nil {
		return ref.Ref{}, invalid(err)
	}
	return r, nil
}

func splitName(name string) (ns, n string) {
	if ns, n, ok := strings.Cut(name, "/"); ok {
		return ns, n
	}
	return DefaultNS, name
}

// Put stores content and, when named, advances the name to it.
func (s *Service) Put(_ context.Context, req PutRequest) (PutResult, error) {
	var ns, name string
	if req.Name != "" {
		ns, name = splitName(req.Name)
		if _, err := ref.Parse("caster://" + ns + "/" + name); err != nil {
			return PutResult{}, invalid(err)
		}
	}
	var r io.Reader = req.R
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

// Get materialises any ref.
func (s *Service) Get(ctx context.Context, req GetRequest) (GetResult, error) {
	c, err := s.materialize(ctx, req.Ref)
	if err != nil {
		return GetResult{}, err
	}
	out := GetResult{Meta: metaOf(c.Meta)}
	if c.State != StateResolved {
		return out, nil
	}
	data, max := c.Data, req.MaxBytes
	if max == 0 {
		max = DefaultMaxBytes
	}
	if max > 0 && len(data) > max {
		out.Truncated, out.TotalBytes = true, len(data)
		data = data[:max]
		if utf8.Valid(c.Data) { // do not cut a multi-byte rune in half
			data = []byte(strings.ToValidUTF8(string(data), ""))
		}
	}
	out.Data = data
	if utf8.Valid(data) {
		out.Content = string(data)
	} else {
		out.Binary = true
	}
	return out, nil
}

func (s *Service) materialize(ctx context.Context, raw string) (resolver.Content, error) {
	r, err := parseRef(raw)
	if err != nil {
		return resolver.Content{}, err
	}
	c, err := s.reg.Materialize(ctx, r)
	return c, classify(err)
}

// Head resolves any ref to metadata only.
func (s *Service) Head(ctx context.Context, req RefRequest) (Meta, error) {
	r, err := parseRef(req.Ref)
	if err != nil {
		return Meta{}, err
	}
	m, err := s.reg.Resolve(ctx, r)
	if err != nil {
		return Meta{}, classify(err)
	}
	return metaOf(m), nil
}

// Snapshot materialises a (possibly floating, possibly remote) ref and stores
// the bytes in CAS, recording where and when they came from. The result is a
// pinned cas:// ref; with a name it also advances that name.
func (s *Service) Snapshot(ctx context.Context, req SnapRequest) (SnapResult, error) {
	c, err := s.materialize(ctx, req.Ref)
	if err != nil {
		return SnapResult{}, err
	}
	out := SnapResult{State: c.State, Source: c.Ref.String()}
	if c.State != StateResolved {
		return out, nil
	}
	src := out.Source
	if c.Revision != "" {
		src += " rev=" + c.Revision
	}
	if c.ContentHash != "" && c.Ref.Scheme != ref.SchemeCAS {
		src += " checksum=" + c.ContentHash
	}
	res, err := s.Put(ctx, PutRequest{Name: req.Name, Title: c.Title, Tags: req.Tags, Mime: c.Mime, Source: src, R: strings.NewReader(string(c.Data))})
	if err != nil {
		return SnapResult{}, err
	}
	out.CAS = res.CAS
	if req.Name != "" {
		out.Ref = res.Ref
	}
	return out, nil
}

// List returns the latest revision of named items.
func (s *Service) List(_ context.Context, req ListRequest) (ListResult, error) {
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
func (s *Service) Log(_ context.Context, req LogRequest) (LogResult, error) {
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
func (s *Service) Search(_ context.Context, req SearchRequest) (SearchResult, error) {
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

type found struct {
	ref.Candidate
	meta resolver.Meta
	err  error
}

func (s *Service) scan(ctx context.Context, text string) []found {
	var out []found
	for _, c := range ref.Detect(text) {
		m, err := s.reg.Resolve(ctx, c.Ref)
		out = append(out, found{c, m, err})
	}
	return out
}

// Scan detects refs in text and validates each with its resolver. Detection
// fetches nothing; only refs that resolve are listed.
func (s *Service) Scan(ctx context.Context, req ScanRequest) (ScanResult, error) {
	out := ScanResult{Refs: []Meta{}}
	for _, f := range s.scan(ctx, req.Text) {
		switch {
		case f.err != nil:
			out.Problems = append(out.Problems, Problem{f.Ref.String(), f.err.Error()})
		case f.meta.State == StateResolved:
			out.Refs = append(out.Refs, metaOf(f.meta))
		default:
			out.Unresolved++
		}
	}
	return out, nil
}

// Expand replaces each resolved ref in text with its contents, wrapped in a
// labelled block. The wrapper marks the content as untrusted data from its
// source and records the revision actually used. Unresolved refs are left
// exactly as written.
func (s *Service) Expand(ctx context.Context, req ExpandRequest) (ExpandResult, error) {
	budget := req.Budget
	if budget <= 0 {
		budget = 16 << 10
	}
	out := ExpandResult{Refs: []Meta{}}
	var b strings.Builder
	last := 0
	for _, f := range s.scan(ctx, req.Text) {
		if f.err != nil || f.meta.State != StateResolved {
			continue
		}
		c, err := s.reg.Materialize(ctx, f.Ref)
		if err != nil || c.State != StateResolved {
			continue
		}
		body, trunc := string(c.Data), ""
		if len(body) > budget {
			body, trunc = strings.ToValidUTF8(body[:budget], ""), " truncated=\"true\""
		}
		b.WriteString(req.Text[last:f.Start])
		fmt.Fprintf(&b, "<caster-ref ref=%s%s%s trust=\"untrusted\">\n%s\n</caster-ref>",
			strconv.Quote(f.Ref.String()), attr("rev", c.Revision), trunc+attr("sha256", c.ContentHash), strings.TrimRight(body, "\n"))
		last = f.End
		out.Resolved++
		out.Refs = append(out.Refs, metaOf(c.Meta))
	}
	b.WriteString(req.Text[last:])
	out.Text = b.String()
	return out, nil
}

func attr(k, v string) string {
	if v == "" {
		return ""
	}
	return " " + k + "=" + strconv.Quote(v)
}
