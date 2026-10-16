// Package client is caster's remote transport in reverse: an HTTP client that
// implements the service contract (service.NotesAPI and friends) against a
// home node's `caster serve`, so the CLI and the MCP server work unchanged
// whether the store is local or on another machine.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/caster/internal/service"
)

const maxResponse = 16 << 20

// TokenSource yields the bearer token for a request. It is called per request
// so a rotated credential is picked up without restarting.
type TokenSource func(ctx context.Context) (string, error)

// Client talks to one home node.
type Client struct {
	base  *url.URL
	token TokenSource
	hc    *http.Client
}

// ValidateURL accepts https anywhere, and plain http only to loopback or a
// tailnet address (the bearer token would otherwise cross the network in the
// clear), unless allowHTTP says the operator has accepted that.
func ValidateURL(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("remote must be an http(s) URL, got %q", raw)
	}
	if u.Scheme == "http" && !allowHTTP {
		hostport := u.Host
		if u.Port() == "" {
			hostport += ":80"
		}
		if auth.ValidateBind(hostport, auth.ModeOff) != nil {
			return nil, fmt.Errorf("refusing plain http to %s: the token would cross the network unencrypted. Use https (e.g. Tailscale Serve), an SSH forward to localhost, a tailnet IP, or pass -allow-http / set CASTER_ALLOW_HTTP=1 to accept it", u.Host)
		}
	}
	return u, nil
}

// New builds a client for baseURL.
func New(baseURL string, token TokenSource, allowHTTP bool) (*Client, error) {
	u, err := ValidateURL(baseURL, allowHTTP)
	if err != nil {
		return nil, err
	}
	return &Client{base: u, token: token, hc: &http.Client{
		Timeout: 90 * time.Second,
		// Never follow a redirect: it could carry the bearer token to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// BaseURL is the home node's address.
func (c *Client) BaseURL() string { return c.base.String() }

// Service returns a service.Service whose every operation is an HTTP call.
func (c *Client) Service() *service.Service {
	return service.Compose(notes{c}, refs{c}, actions{c}, shadow{c}, access{c})
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func kindOf(status int, code string) string {
	switch code {
	case service.KindInvalid, service.KindNotFound, service.KindDenied, service.KindUnauthenticated, service.KindUnavailable:
		return code
	}
	switch {
	case status == http.StatusUnauthorized:
		return service.KindUnauthenticated
	case status == http.StatusForbidden:
		return service.KindDenied
	case status == http.StatusNotFound:
		return service.KindNotFound
	case status == http.StatusBadRequest || status == http.StatusRequestEntityTooLarge:
		return service.KindInvalid
	case status == http.StatusTooManyRequests || status >= 502:
		return service.KindUnavailable
	}
	return service.KindInternal
}

func fail(kind, format string, a ...any) error {
	return &service.Error{Kind: kind, Err: fmt.Errorf(format, a...)}
}

// call performs one request. in is JSON-encoded for POST. A non-2xx response
// whose body is a result (an outcome such as not_found, carrying "state") is
// decoded into out and returns no error, exactly as the local service does;
// one whose body is an error envelope becomes a classified service.Error.
func (c *Client) call(ctx context.Context, method, path string, q url.Values, in, out any, authed bool) error {
	body, status, err := c.raw(ctx, method, path, q, in, authed)
	if err != nil {
		return err
	}
	if status >= 400 {
		var env errorEnvelope
		if json.Unmarshal(body, &env) == nil && env.Error.Code != "" {
			msg := env.Error.Message
			if status == http.StatusUnauthorized && authed {
				msg += " (the token may be expired or revoked: try `caster renew`, or pair again with `caster login`)"
			}
			return fail(kindOf(status, env.Error.Code), "%s", msg)
		}
		if out == nil || !hasState(body) {
			return fail(kindOf(status, ""), "home node answered HTTP %d", status)
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fail(service.KindInternal, "unexpected response from the home node: %v", err)
	}
	return nil
}

func hasState(body []byte) bool {
	var probe struct {
		State *string `json:"state"`
	}
	return json.Unmarshal(body, &probe) == nil && probe.State != nil
}

func (c *Client) raw(ctx context.Context, method, path string, q url.Values, in any, authed bool) ([]byte, int, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	var rd io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, 0, fail(service.KindInvalid, "encode request: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, 0, fail(service.KindInvalid, "%v", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "caster-client")
	if authed {
		tok, terr := c.token(ctx)
		if terr != nil {
			return nil, 0, fail(service.KindUnauthenticated, "no usable credential for %s: %v", c.base.Host, terr)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, fail(service.KindUnavailable, "cannot reach the home node at %s: %v", c.base.Host, errors.Unwrap(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, 0, fail(service.KindUnavailable, "the home node redirected (HTTP %d); redirects are not followed because they could carry the token elsewhere", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, 0, fail(service.KindUnavailable, "read response: %v", err)
	}
	if len(b) > maxResponse {
		return nil, 0, fail(service.KindInternal, "response over %d bytes", maxResponse)
	}
	return b, resp.StatusCode, nil
}

func qs(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			v.Set(kv[i], kv[i+1])
		}
	}
	return v
}

// ---- NotesAPI

type notes struct{ c *Client }

func (n notes) Put(ctx context.Context, req service.PutRequest) (service.PutResult, error) {
	if req.R != nil {
		b, err := io.ReadAll(io.LimitReader(req.R, 8<<20+1))
		if err != nil {
			return service.PutResult{}, fail(service.KindInvalid, "read input: %v", err)
		}
		if len(b) > 8<<20 {
			return service.PutResult{}, fail(service.KindInvalid, "input over 8 MiB: the remote API takes text up to that size")
		}
		req.Content, req.R = string(b), nil
	}
	var out service.PutResult
	return out, n.c.call(ctx, http.MethodPost, "/v1/put", nil, req, &out, true)
}

func (n notes) List(ctx context.Context, req service.ListRequest) (service.ListResult, error) {
	var out service.ListResult
	return out, n.c.call(ctx, http.MethodGet, "/v1/list", qs("ns", req.NS, "tag", req.Tag), nil, &out, true)
}

func (n notes) Log(ctx context.Context, req service.LogRequest) (service.LogResult, error) {
	var out service.LogResult
	return out, n.c.call(ctx, http.MethodGet, "/v1/log", qs("name", req.Name), nil, &out, true)
}

func (n notes) Search(ctx context.Context, req service.SearchRequest) (service.SearchResult, error) {
	var out service.SearchResult
	return out, n.c.call(ctx, http.MethodGet, "/v1/search", qs("q", req.Query, "limit", itoa(req.Limit)), nil, &out, true)
}

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// ---- RefsAPI

type refs struct{ c *Client }

func (r refs) Get(ctx context.Context, req service.GetRequest) (service.GetResult, error) {
	q := qs("ref", req.Ref, "max_bytes", itoa(req.MaxBytes))
	var out service.GetResult
	if err := r.c.call(ctx, http.MethodGet, "/v1/get", q, nil, &out, true); err != nil {
		return out, err
	}
	if out.State != service.StateResolved {
		return out, nil
	}
	if !out.Binary {
		out.Data = []byte(out.Content)
		return out, nil
	}
	// JSON cannot carry non-UTF-8 bytes: fetch them raw.
	q.Set("raw", "1")
	b, status, err := r.c.raw(ctx, http.MethodGet, "/v1/get", q, nil, true)
	if err != nil {
		return out, err
	}
	if status != http.StatusOK {
		return out, fail(kindOf(status, ""), "home node answered HTTP %d fetching raw bytes", status)
	}
	out.Data = b
	return out, nil
}

func (r refs) Head(ctx context.Context, req service.RefRequest) (service.Meta, error) {
	var out service.Meta
	return out, r.c.call(ctx, http.MethodGet, "/v1/head", qs("ref", req.Ref), nil, &out, true)
}

func (r refs) Snapshot(ctx context.Context, req service.SnapRequest) (service.SnapResult, error) {
	var out service.SnapResult
	return out, r.c.call(ctx, http.MethodPost, "/v1/snap", nil, req, &out, true)
}

func (r refs) Scan(ctx context.Context, req service.ScanRequest) (service.ScanResult, error) {
	var out service.ScanResult
	return out, r.c.call(ctx, http.MethodPost, "/v1/scan", nil, req, &out, true)
}

func (r refs) Expand(ctx context.Context, req service.ExpandRequest) (service.ExpandResult, error) {
	var out service.ExpandResult
	return out, r.c.call(ctx, http.MethodPost, "/v1/expand", nil, req, &out, true)
}

// ---- ActionsAPI

type actions struct{ c *Client }

func (a actions) List(ctx context.Context, req service.ActionsRequest) (service.ActionsResult, error) {
	var out service.ActionsResult
	return out, a.c.call(ctx, http.MethodGet, "/v1/actions", qs("ref", req.Ref), nil, &out, true)
}

func (a actions) Run(ctx context.Context, req service.ActRequest) (service.ActResult, error) {
	var out service.ActResult
	return out, a.c.call(ctx, http.MethodPost, "/v1/act", nil, req, &out, true)
}

// ---- ShadowAPI

type shadow struct{ c *Client }

func (s shadow) Observe(ctx context.Context, req service.ObserveRequest) (service.ObserveResult, error) {
	var out service.ObserveResult
	return out, s.c.call(ctx, http.MethodPost, "/v1/observe", nil, req, &out, true)
}

func (s shadow) Report(ctx context.Context, req service.ReportRequest) (service.ReportResult, error) {
	since := ""
	if !req.Since.IsZero() {
		since = time.Since(req.Since).Round(time.Second).String() // the HTTP route takes a relative duration
	}
	var out service.ReportResult
	return out, s.c.call(ctx, http.MethodGet, "/v1/report", qs("since", since), nil, &out, true)
}

func (s shadow) Label(ctx context.Context, req service.LabelRequest) (service.LabelResult, error) {
	var out service.LabelResult
	return out, s.c.call(ctx, http.MethodPost, "/v1/label", nil, req, &out, true)
}

// ---- AccessAPI

type access struct{ c *Client }

func (access) CreateGrant(context.Context, service.PairRequest) (service.PairResult, error) {
	return service.PairResult{}, fail(service.KindDenied, "pairing codes are created at the home node's own terminal (`caster pair` there), never over the network")
}

func (a access) Exchange(ctx context.Context, req service.ExchangeRequest) (service.ExchangeResult, error) {
	var out service.ExchangeResult
	return out, a.c.call(ctx, http.MethodPost, "/v1/pair/exchange", nil, req, &out, false)
}

func (a access) Whoami(ctx context.Context, _ struct{}) (service.WhoamiResult, error) {
	var out service.WhoamiResult
	return out, a.c.call(ctx, http.MethodGet, "/v1/whoami", nil, nil, &out, true)
}

func (a access) Renew(ctx context.Context, req service.RenewRequest) (service.DeviceInfo, error) {
	var out service.DeviceInfo
	return out, a.c.call(ctx, http.MethodPost, "/v1/renew", nil, req, &out, true)
}

func (a access) Devices(ctx context.Context, _ struct{}) (service.DevicesResult, error) {
	var out service.DevicesResult
	return out, a.c.call(ctx, http.MethodGet, "/v1/devices", nil, nil, &out, true)
}

func (a access) Revoke(ctx context.Context, req service.RevokeRequest) (service.RevokeResult, error) {
	var out service.RevokeResult
	return out, a.c.call(ctx, http.MethodPost, "/v1/devices/revoke", nil, req, &out, true)
}

var (
	_ service.NotesAPI   = notes{}
	_ service.RefsAPI    = refs{}
	_ service.ActionsAPI = actions{}
	_ service.ShadowAPI  = shadow{}
	_ service.AccessAPI  = access{}
)

// Health asks the home node's open /health route, with no credential. It
// returns the node's auth mode string.
func (c *Client) Health(ctx context.Context) (string, error) {
	var out struct {
		Auth string `json:"auth"`
	}
	if err := c.call(ctx, http.MethodGet, "/health", nil, nil, &out, false); err != nil {
		return "", err
	}
	return out.Auth, nil
}
