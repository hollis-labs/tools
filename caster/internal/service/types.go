package service

import (
	"errors"
	"io"
	"time"

	"github.com/hollis-labs/caster/internal/resolver"
	"github.com/hollis-labs/caster/internal/store"
)

// Resolution states. A missing or forbidden ref is an outcome carried in
// Meta.State, not an error; errors mean the service could not answer.
const (
	StateResolved = resolver.StateResolved
	StateNotFound = resolver.StateNotFound
	StateDenied   = resolver.StateDenied
)

// Scopes the operations require. Transports that authorise (HTTP, and MCP over
// HTTP) map their routes and tools to these; ScopeFor is the single table.
const (
	ScopeRead  = "caster.read"
	ScopeWrite = "caster.write"
)

// ScopeFor returns the scope an operation needs. Unknown operations get "".
func ScopeFor(op string) string {
	switch op {
	case OpPut, OpSnap, OpAct:
		return ScopeWrite
	case OpGet, OpHead, OpList, OpLog, OpSearch, OpScan, OpExpand, OpActions:
		return ScopeRead
	}
	return ""
}

// Operation names, shared by every transport.
const (
	OpPut, OpGet, OpHead, OpSnap = "put", "get", "head", "snap"
	OpList, OpLog, OpSearch      = "list", "log", "search"
	OpScan, OpExpand             = "scan", "expand"
	OpActions, OpAct             = "actions", "act"
)

// Error kinds, the one vocabulary transports map to exit codes, HTTP statuses
// and MCP error codes.
const (
	KindInvalid     = "invalid"
	KindNotFound    = "not_found"
	KindDenied      = "denied"
	KindUnavailable = "unavailable"
	KindInternal    = "internal"
)

// Error is a classified service error.
type Error struct {
	Kind string
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// KindOf classifies any error returned by the service.
func KindOf(err error) string {
	var se *Error
	if errors.As(err, &se) {
		return se.Kind
	}
	return KindInternal
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	var se *Error
	switch {
	case errors.As(err, &se):
		return err
	case errors.Is(err, store.ErrNotFound):
		return &Error{KindNotFound, err}
	case errors.Is(err, resolver.ErrUnavailable):
		return &Error{KindUnavailable, err}
	}
	return &Error{KindInternal, err}
}

func invalid(err error) error { return &Error{KindInvalid, err} }

// Meta describes a ref without its content.
type Meta struct {
	Ref      string `json:"ref"`
	State    string `json:"state"`
	Kind     string `json:"kind,omitempty"`
	Title    string `json:"title,omitempty"`
	Mime     string `json:"mime,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Revision string `json:"revision,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Floating bool   `json:"floating"`
}

func metaOf(m resolver.Meta) Meta {
	return Meta{Ref: m.Ref.String(), State: m.State, Kind: m.Kind, Title: m.Title, Mime: m.Mime,
		Size: m.Size, Revision: m.Revision, SHA256: m.ContentHash, Floating: m.Ref.Floating()}
}

// RefRequest names one ref.
type RefRequest struct {
	Ref string `json:"ref"`
}

// PutRequest stores content. Content is used unless R is set (streaming callers).
type PutRequest struct {
	Name    string    `json:"name,omitempty"` // [ns/]name; empty = anonymous blob
	Title   string    `json:"title,omitempty"`
	Tags    string    `json:"tags,omitempty"` // space separated
	Mime    string    `json:"mime,omitempty"`
	Source  string    `json:"source,omitempty"` // provenance ref
	Content string    `json:"content"`
	R       io.Reader `json:"-"`
}

// PutResult is what was stored. Ref is the handle to pass around: caster://…@rev
// when named, else cas://….
type PutResult struct {
	Ref  string `json:"ref"`
	CAS  string `json:"cas"`
	Size int64  `json:"size"`
}

// GetRequest fetches content. MaxBytes: 0 = DefaultMaxBytes, <0 = unlimited.
type GetRequest struct {
	Ref      string `json:"ref"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// DefaultMaxBytes bounds Get for callers that do not ask for more.
const DefaultMaxBytes = 64 << 10

// GetResult is Meta plus content. Data carries the (possibly truncated) raw
// bytes for in-process callers; Content is the same as text for JSON
// transports, empty when the bytes are not UTF-8 (Binary).
type GetResult struct {
	Meta
	Content    string `json:"content,omitempty"`
	Binary     bool   `json:"binary,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	TotalBytes int    `json:"total_bytes,omitempty"`
	Data       []byte `json:"-"`
}

// SnapRequest pins a ref's current bytes in CAS.
type SnapRequest struct {
	Ref  string `json:"ref"`
	Name string `json:"name,omitempty"`
	Tags string `json:"tags,omitempty"`
}

// SnapResult reports a snapshot. When State is not resolved nothing was stored.
type SnapResult struct {
	State  string `json:"state"`
	CAS    string `json:"cas,omitempty"`
	Ref    string `json:"ref,omitempty"` // set when Name was given
	Source string `json:"source"`
}

// ListRequest filters named items.
type ListRequest struct {
	NS  string `json:"ns,omitempty"`
	Tag string `json:"tag,omitempty"`
}

// Item is the latest revision of a named item.
type Item struct {
	Ref     string    `json:"ref"`
	Rev     int       `json:"rev"`
	Title   string    `json:"title,omitempty"`
	Tags    string    `json:"tags,omitempty"`
	CAS     string    `json:"cas"`
	Updated time.Time `json:"updated"`
}

// ListResult is a set of items.
type ListResult struct {
	Items []Item `json:"items"`
}

// LogRequest names an item whose history is wanted.
type LogRequest struct {
	Name string `json:"name"`
}

// Revision is one revision of a name.
type Revision struct {
	Rev   int       `json:"rev"`
	CAS   string    `json:"cas"`
	Title string    `json:"title,omitempty"`
	At    time.Time `json:"at"`
}

// LogResult is a name's history, newest first.
type LogResult struct {
	Name      string     `json:"name"`
	Revisions []Revision `json:"revisions"`
}

// SearchRequest is a full-text query.
type SearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// Hit is one search hit.
type Hit struct {
	CAS     string   `json:"cas"`
	Names   []string `json:"names,omitempty"`
	Snippet string   `json:"snippet"`
}

// SearchResult is a set of hits.
type SearchResult struct {
	Hits []Hit `json:"hits"`
}

// ScanRequest is text to search for refs.
type ScanRequest struct {
	Text string `json:"text"`
}

// Problem is a detected ref whose resolver could not answer.
type Problem struct {
	Ref   string `json:"ref"`
	Error string `json:"error"`
}

// ScanResult lists the detected refs that resolve. Unresolved ones are counted,
// not listed (quiet by default); Problems lists resolver failures.
type ScanResult struct {
	Refs       []Meta    `json:"refs"`
	Unresolved int       `json:"unresolved"`
	Problems   []Problem `json:"problems,omitempty"`
}

// ExpandRequest is text whose resolvable refs are replaced by contents.
type ExpandRequest struct {
	Text   string `json:"text"`
	Budget int    `json:"budget,omitempty"` // max bytes inserted per ref (0 = 16 KiB)
}

// ExpandResult is the expanded text and what was inserted.
type ExpandResult struct {
	Text     string `json:"text"`
	Resolved int    `json:"resolved"`
	Refs     []Meta `json:"refs"`
}
