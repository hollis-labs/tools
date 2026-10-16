package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/caster/internal/ref"
	"github.com/hollis-labs/caster/internal/resolver"
)

// Refs is the domain service for refs: resolve, fetch, snapshot into CAS, and
// detect/expand refs in text.
type Refs struct {
	reg   *resolver.Registry
	notes *Notes
}

// Get materialises any ref.
func (s *Refs) Get(ctx context.Context, req GetRequest) (GetResult, error) {
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

func (s *Refs) materialize(ctx context.Context, raw string) (resolver.Content, error) {
	r, err := parseRef(raw)
	if err != nil {
		return resolver.Content{}, err
	}
	c, err := s.reg.Materialize(ctx, r)
	return c, classify(err)
}

// Head resolves any ref to metadata only.
func (s *Refs) Head(ctx context.Context, req RefRequest) (Meta, error) {
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
func (s *Refs) Snapshot(ctx context.Context, req SnapRequest) (SnapResult, error) {
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
	res, err := s.notes.Put(ctx, PutRequest{Name: req.Name, Title: c.Title, Tags: req.Tags, Mime: c.Mime, Source: src, R: strings.NewReader(string(c.Data))})
	if err != nil {
		return SnapResult{}, err
	}
	out.CAS = res.CAS
	if req.Name != "" {
		out.Ref = res.Ref
	}
	return out, nil
}

type found struct {
	ref.Candidate
	meta resolver.Meta
	err  error
}

func (s *Refs) scan(ctx context.Context, text string) []found {
	var out []found
	for _, c := range ref.Detect(text) {
		m, err := s.reg.Resolve(ctx, c.Ref)
		out = append(out, found{c, m, err})
	}
	return out
}

// Scan detects refs in text and validates each with its resolver. Detection
// fetches nothing; only refs that resolve are listed.
func (s *Refs) Scan(ctx context.Context, req ScanRequest) (ScanResult, error) {
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
func (s *Refs) Expand(ctx context.Context, req ExpandRequest) (ExpandResult, error) {
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

func parseRef(raw string) (ref.Ref, error) {
	r, err := ref.Parse(raw)
	if err != nil {
		return ref.Ref{}, invalid(err)
	}
	return r, nil
}
