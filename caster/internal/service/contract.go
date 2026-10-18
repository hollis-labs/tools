package service

import "context"

// The contract the transports program against. Each interface is satisfied by
// the local implementation in this package and by the remote client in
// internal/client, so a CLI or MCP server works the same against a local store
// or a home node reached over HTTP.

// NotesAPI is stored text: blobs, named items, revisions, search.
type NotesAPI interface {
	Put(context.Context, PutRequest) (PutResult, error)
	List(context.Context, ListRequest) (ListResult, error)
	Log(context.Context, LogRequest) (LogResult, error)
	Search(context.Context, SearchRequest) (SearchResult, error)
}

// RefsAPI resolves, fetches, snapshots and detects refs.
type RefsAPI interface {
	Get(context.Context, GetRequest) (GetResult, error)
	Head(context.Context, RefRequest) (Meta, error)
	Snapshot(context.Context, SnapRequest) (SnapResult, error)
	Scan(context.Context, ScanRequest) (ScanResult, error)
	Expand(context.Context, ExpandRequest) (ExpandResult, error)
}

// ActionsAPI is registered operations on refs.
type ActionsAPI interface {
	List(context.Context, ActionsRequest) (ActionsResult, error)
	Run(context.Context, ActRequest) (ActResult, error)
}

// ShadowAPI is detection measured without surfacing anything.
type ShadowAPI interface {
	Observe(context.Context, ObserveRequest) (ObserveResult, error)
	Report(context.Context, ReportRequest) (ReportResult, error)
	Label(context.Context, LabelRequest) (LabelResult, error)
}

// AccessAPI is pairing and device tokens. CreateGrant is local-only by rule; a
// remote client refuses it.
type AccessAPI interface {
	CreateGrant(context.Context, PairRequest) (PairResult, error)
	Exchange(context.Context, ExchangeRequest) (ExchangeResult, error)
	Whoami(context.Context, struct{}) (WhoamiResult, error)
	Renew(context.Context, RenewRequest) (DeviceInfo, error)
	Devices(context.Context, struct{}) (DevicesResult, error)
	Revoke(context.Context, RevokeRequest) (RevokeResult, error)
}

var (
	_ NotesAPI   = (*Notes)(nil)
	_ RefsAPI    = (*Refs)(nil)
	_ ActionsAPI = (*Actions)(nil)
	_ ShadowAPI  = (*Shadow)(nil)
	_ AccessAPI  = (*Access)(nil)
)

// Compose assembles a Service from any implementations of the contract. The
// remote client uses it; there is no store behind such a Service.
func Compose(n NotesAPI, r RefsAPI, a ActionsAPI, s ShadowAPI, ac AccessAPI) *Service {
	return &Service{Notes: n, Refs: r, Actions: a, Shadow: s, Access: ac}
}
