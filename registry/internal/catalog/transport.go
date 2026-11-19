package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gmcp "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolErrorContent preserves the structured error over MCP as well as HTTP/CLI.
func (e *Error) ToolErrorContent() any { return e }

type Route struct{ Operation, Method, Path string }

var Routes = []Route{
	{"export_plan", "POST", "/v1/export/plan"}, {"export_diff", "POST", "/v1/export/diff"}, {"export_apply", "POST", "/v1/export/apply"},
	{"changes_list", "GET", "/v1/changes"}, {"import_prepare", "POST", "/v1/import/prepare"}, {"import_commit", "POST", "/v1/import/commit"}, {"import_checkpoint", "GET", "/v1/import/checkpoint"},
	{"publication_approval_prepare", "POST", "/v1/publication/prepare"}, {"publication_approval_get", "GET", "/v1/publication/approval"}, {"publication_approval_decide", "POST", "/v1/publication/decide"}, {"publication_publish", "POST", "/v1/publication/publish"}, {"publication_policy_get", "GET", "/v1/publication/policy"}, {"publication_policy_set", "PUT", "/v1/publication/policy"},
	{"kinds_list", "GET", "/v1/kinds"}, {"schema_get", "GET", "/v1/schema"},
	{"record_create", "POST", "/v1/records"}, {"record_get", "GET", "/v1/record"},
	{"record_list", "GET", "/v1/records"}, {"record_search", "GET", "/v1/search"},
	{"record_update", "PUT", "/v1/record"}, {"record_archive", "POST", "/v1/record/archive"}, {"record_delete", "DELETE", "/v1/record"}, {"record_verify", "POST", "/v1/record/verify"},
	{"revision_list", "GET", "/v1/revisions"}, {"revision_get", "GET", "/v1/revision"},
	{"relation_create", "POST", "/v1/relations"}, {"relation_list", "GET", "/v1/relations"}, {"relation_remove", "DELETE", "/v1/relations"},
	{"tool_resolve", "GET", "/v1/tools/resolve"}, {"tool_classify", "GET", "/v1/tools/classification"}, {"tool_propose", "POST", "/v1/tools/proposals"},
	{"review_list", "GET", "/v1/reviews"}, {"review_decide", "POST", "/v1/reviews/decisions"},
	{"snapshot_get", "GET", "/v1/snapshot"}, {"diagnostics", "GET", "/v1/diagnostics"},
}

type CallFunc func(context.Context, string, Args) (any, error)

func Handler(call CallFunc) http.Handler {
	mux := http.NewServeMux()
	for _, route := range Routes {
		mux.HandleFunc(route.Method+" "+route.Path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			requestID := newID()
			w.Header().Set("X-Request-ID", requestID)
			var a Args
			var err error
			if r.Method == "GET" {
				raw := r.URL.Query().Get("args")
				if raw == "" {
					raw = "{}"
				}
				err = Decode([]byte(raw), &a)
				if id := r.URL.Query().Get("id"); id != "" {
					if a.ID != "" && a.ID != id {
						err = failure("validation_error", "id", "conflicting identity selectors")
					}
					a.ID = id
				}
				if v := r.URL.Query().Get("schema"); v != "" {
					a.Schema = v
				}
			} else {
				raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
				if e != nil {
					err = failure("validation_error", "request", "unable to read request within 2 MiB limit")
				} else {
					err = Decode(raw, &a)
				}
			}
			if v := r.Header.Get("If-Match"); v != "" {
				v = strings.Trim(v, "\"")
				if a.Version != "" && a.Version != v && a.Version != etagRevision(v) {
					err = failure("validation_error", "version", "If-Match disagrees with version")
				}
				a.Version = v
			}
			if key := r.Header.Get("Idempotency-Key"); key != "" {
				if a.IdempotencyKey != "" && a.IdempotencyKey != key {
					err = failure("validation_error", "idempotency_key", "header disagrees with request")
				}
				a.IdempotencyKey = key
			}
			var result any
			if err == nil {
				result, err = call(r.Context(), route.Operation, a)
			}
			if err != nil {
				e := internalError(err)
				wireError := *e
				wireError.RequestID = requestID
				w.WriteHeader(Status(wireError.Code))
				_ = json.NewEncoder(w).Encode(map[string]any{"error": wireError})
				return
			}
			if record, ok := result.(Record); ok {
				// Resolution can change when a target changes without a source revision.
				// Conditional reads of a representation with edges must be revalidated.
				if len(record.Edges) == 0 {
					w.Header().Set("ETag", `"`+recordETag(record)+`"`)
				}
			}
			if page, ok := result.(Page); ok {
				w.Header().Set("ETag", `"`+digest(struct {
					Records  []Record
					Boundary int64
				}{page.Records, page.Boundary})+`"`)
			}
			if r.Method == "GET" && r.Header.Get("If-None-Match") != "" && r.Header.Get("If-None-Match") == w.Header().Get("ETag") {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_ = json.NewEncoder(w).Encode(result)
		})
	}
	return mux
}
func Status(code string) int {
	switch code {
	case "forbidden":
		return 403
	case "not_found":
		return 404
	case "deleted":
		return 410
	case "version_conflict", "alias_conflict":
		return 409
	case "unavailable":
		return 503
	default:
		return 400
	}
}

// Client never opens a local store. IDs travel as JSON/query values, not path
// components, so dot segments and slash-bearing canonical IDs survive routers.
type Client struct {
	URL   string
	HTTP  *http.Client
	Token func() (string, error)
}

func (c *Client) Call(ctx context.Context, op string, a Args) (any, error) {
	var route Route
	for _, r := range Routes {
		if r.Operation == op {
			route = r
			break
		}
	}
	if route.Operation == "" {
		return nil, failure("not_found", "operation", "unknown operation")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	u := strings.TrimRight(c.URL, "/") + route.Path
	var body io.Reader
	if route.Method == "GET" {
		u += "?" + url.Values{"args": {string(raw)}}.Encode()
	} else {
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, route.Method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != nil {
		token, tokenErr := c.Token()
		if tokenErr != nil {
			return nil, tokenErr
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, failure("unavailable", "service", "could not reach Registry")
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var wire struct {
			Error *Error `json:"error"`
		}
		if json.Unmarshal(data, &wire) == nil && wire.Error != nil {
			return nil, wire.Error
		}
		return nil, failure("forbidden", "credential", "service rejected credentials or scope")
	}
	if !json.Valid(data) {
		return nil, failure("unavailable", "response", "invalid service response")
	}
	return json.RawMessage(data), nil
}

// RegisterMCP is shared by the same-service HTTP handler and the stdio HTTP
// client. Authenticated service calls remain the sole authority for mutations.
type rawArgumentsKey struct{}

// RegisterMCP captures original argument bytes before generic float64 decoding.
// This preserves definition absent/null and numeric admission across transports.
func RegisterMCP(server *gmcp.Server, call CallFunc) {
	server.SDKServer().AddReceivingMiddleware(func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			if method == "tools/call" {
				if params, ok := req.GetParams().(*mcpsdk.CallToolParamsRaw); ok {
					ctx = context.WithValue(ctx, rawArgumentsKey{}, params.Arguments)
				}
			}
			return next(ctx, method, req)
		}
	})
	for _, op := range Operations {
		behavior := gmcp.Reads("reads Registry definitions and revision snapshots")
		if scope(op) != "registry.read" {
			behavior = gmcp.Writes()
		}
		server.RegisterChecked(gmcp.Tool{Name: "registry_" + op, Description: "Registry " + strings.ReplaceAll(op, "_", " ") + "; IDs are exact opaque values. Mutations require current version tokens.", InputSchema: argumentSchema(op), Handler: func(ctx context.Context, args map[string]any) (any, error) {
			raw, ok := ctx.Value(rawArgumentsKey{}).(json.RawMessage)
			var err error
			if !ok {
				raw, err = json.Marshal(args)
			}
			if err != nil {
				return nil, invalid("arguments", err)
			}
			var a Args
			if checkErr := Decode(raw, &a); checkErr != nil {
				return nil, checkErr
			}
			out, err := call(ctx, op, a)
			if err != nil {
				return nil, internalError(err)
			}
			// JSON client responses must become structured MCP objects, not opaque text.
			if b, ok := out.(json.RawMessage); ok {
				var value any
				if checkErr := json.Unmarshal(b, &value); checkErr != nil {
					return nil, checkErr
				}
				return value, nil
			}
			return out, nil
		}}, behavior)
	}
}
func argumentSchema(op string) any {
	// Explicit operation-specific fields avoid a tool discoverer guessing which
	// subset of the common transport envelope applies to a particular operation.
	props := map[string]any{}
	required := []string{}
	add := func(name, typ, description string, need bool) {
		props[name] = map[string]any{"type": typ, "description": description}
		if need {
			required = append(required, name)
		}
	}
	switch op {
	case "export_plan":
		add("export", "object", "Target binding, optional exact ids and expected_generation; published records only", true)
	case "export_diff", "export_apply":
		add("receipt_id", "string", "Caller-owned export authorization receipt", true)
		add("hash", "string", "Exact deterministic bundle digest", true)
	case "changes_list":
		add("query", "object", "Publication changes cursor and limit", false)
	case "import_checkpoint":
		add("id", "string", "Connector identity", true)
	case "import_prepare":
		add("import", "object", "Temporary connector snapshot: connector, checkpoint, expected_checkpoint, entries with source_key/source_revision/record", true)
	case "import_commit":
		add("plan_id", "string", "Prepared immutable plan", true)
		add("hash", "string", "Exact prepared plan digest", true)
		add("source_digest", "string", "Confirmed current source snapshot digest", true)
		add("idempotency_key", "string", "Caller-scoped retry key", true)
	case "publication_policy_get":
	case "publication_policy_set":
		add("version", "string", "Current policy revision", true)
		add("roles", "object", "Principal to publication role map", true)
	case "publication_approval_prepare":
		add("candidates", "array", "Exact candidate id/revision/expected_published_revision set", true)
		add("expires_at", "string", "Optional RFC3339 approval expiry", false)
	case "publication_approval_get":
		add("approval_id", "string", "Approval identity", true)
	case "publication_approval_decide", "publication_publish":
		add("approval_id", "string", "Approval identity", true)
		add("approval_revision", "string", "Exact approval receipt revision", true)
		add("hash", "string", "Exact batch_manifest_digest reviewed", true)
		if op == "publication_approval_decide" {
			add("decision", "string", "approved, denied or withdrawn", true)
		} else {
			add("candidates", "array", "Exact approved candidate and expected pointer set", true)
			add("idempotency_key", "string", "Caller-scoped committed replay key", true)
		}
	case "schema_get":
		add("schema", "string", "Versioned schema, such as project/1", true)
	case "record_create":
		add("record", "object", "Authored Input: schema, key, payload, optional summary/source/metadata", true)
		add("idempotency_key", "string", "Caller-scoped creation retry key", false)
		add("allow_unresolved", "boolean", "Explicitly accept missing local relation targets", false)
	case "record_list", "record_search", "snapshot_get", "review_list":
		add("query", "object", "Filters kind/subtype/lifecycle/authority/owner/key/review_state/relation/search; full, limit 1..200, cursor", false)
	case "record_get", "tool_classify", "relation_list":
		add("id", "string", "Exact canonical record ID", true)
		add("publication", "string", "published (default) or draft", false)
		if op == "relation_list" {
			add("direction", "string", "outgoing (default), incoming or both; inverses preserve the stored direction", false)
		}
	case "tool_resolve":
		add("publication", "string", "published (default) or draft", false)
		add("id", "string", "Exact tool ID, or use alias selectors", false)
		add("alias_system", "string", "Alias system", false)
		add("alias", "string", "Alias within system", false)
	case "revision_get":
		add("id", "string", "Exact record ID", true)
		add("revision", "string", "Immutable revision ID", true)
	case "revision_list":
		add("id", "string", "Exact record ID", true)
		add("revision", "string", "Continue before this revision", false)
		add("query", "object", "History page limit", false)
	case "kinds_list", "diagnostics":
	default:
		add("id", "string", "Exact canonical record ID", true)
		add("version", "string", "Current version_token (optimistic concurrency)", true)
		switch op {
		case "record_update":
			add("record", "object", "Complete replacement authored Input", true)
			add("allow_unresolved", "boolean", "Allow missing local relation targets", false)
		case "relation_create":
			add("relation", "object", "relation/1 typed proposed relationship", true)
			add("allow_unresolved", "boolean", "Allow missing local target", false)
		case "relation_remove":
			add("edge_id", "string", "Stored edge ID", true)
		case "tool_propose", "review_decide":
			add("hash", "string", "Current definition hash", true)
			add("classification", "object", "classification/1 value", op == "tool_propose")
			add("reason", "string", "Review rationale", true)
			if op == "review_decide" {
				add("decision", "string", "reviewed, denied, quarantined or needs_human", true)
				add("approval", "object", "Existing approval/evidence reference required for elevated decisions", false)
			}
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

var _ interface {
	error
	ToolErrorContent() any
} = (*Error)(nil)
