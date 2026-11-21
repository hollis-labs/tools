# Registry

Registry stores operational definitions and typed relationships: projects,
tools, agents, agencies, skills, documents, workstreams, correlation contexts,
services, config, knowledge, MCP servers and launch profiles.
It is a class 2 Substrate service with one SQLite database, an authenticated HTTP
API, HTTP MCP, and installed CLI/stdio MCP clients of that same service.

Registry does not execute workflows, launch agents, own runtime sessions or
leases, or infer permissions from catalog records. Existing authorities retain
those responsibilities. Project parsing establishes structural validity, not
class certification. No consumers are repointed by installing Registry.

Definitions are drafts until explicitly published. Default reads select the
published revision; use `--publication draft` to inspect edits. See
[publication and approval](docs/publication.md) for the receipt protocol, pinned
dependencies and the fail-closed elevated-publication boundary.
Deterministic export planning, snapshot authorization, drift detection, changes
feeds, and atomic temporary imports are documented in
[materialization and import](docs/materialization.md).

## Build and run

```sh
go build -o registry ./cmd/registry
./registry --help
./registry serve --managed
```

A managed run requires a configured Substrate verifier. Operator config is read
from `$XDG_CONFIG_HOME/registry/config.yaml` (fallback `~/.config/registry/config.yaml`),
or `SUBSTRATE_CONFIG`. The canonical database defaults to
`$XDG_DATA_HOME/registry/registry.db` (fallback `~/.local/share/registry/registry.db`).
An explicit `database.sqlite.path` must be absolute. SQLite migrations are embedded;
one writer, WAL, immutable revisions and transactional current pointers provide
persistence. Secrets use `$XDG_STATE_HOME/registry/secrets`. Paths are independent
of the source checkout. Client/help commands do not open a database or secret store.

Use an operator-owned env file or existing protected secret mechanism for
credentials. Never put credentials in a descriptor, repository, command argument
or task log. Convenience server credentials are separate:

| Variable | Principal | Granted scopes | Default role |
|---|---|---|---|
| `REGISTRY_TOKEN` | service | registry.read, registry.mcp | reader |
| `REGISTRY_WRITE_TOKEN` | writer | registry.read, registry.write, registry.mcp | publisher for authority registry |
| `REGISTRY_REVIEW_TOKEN` | reviewer | registry.read, registry.review, registry.mcp | ground reviewer |

Existing Substrate JWT or hashed API-key verifiers may instead be configured.
`registry.authorities` maps authenticated principal IDs to publication authorities;
`registry.reviewers` maps reviewer IDs to `ground`, `elevated` or `owner`.
These bindings are server configuration, never request fields. A foreign authority
cannot update another authority's record, and identical canonical IDs conflict
across authorities. Read scope sees the catalog; it is not a tenant isolation system.

Only health is public (`GET /healthz`). `GET /readyz` and `/v1/diagnostics` require
read scope. Diagnostics report readiness/schema versions, never credentials.
MCP-enabled HTTP always requires enforced auth, even in development.

## CLI and stdio MCP

Set `REGISTRY_URL` or pass `--url`; the default is the configured loopback service
port. Clients use `--token-file`, `REGISTRY_TOKEN_FILE`, or `REGISTRY_TOKEN` as an
outbound bearer credential. Token files are reread on each request. For writes,
point the client at a credential that actually holds write scope.

```sh
registry kinds --json
registry schema --schema document/1 --json
registry create --file request.json --idempotency-key authoring-1 --json
registry get 'document:guide/../operations' --publication draft --json
registry update 'document:guide/../operations' --version CURRENT_TOKEN --file replacement.json --json
registry search --query 'backup' --kind document --full --limit 20 --json
registry history 'document:guide/../operations' --json
registry snapshot --full --limit 100 --json
registry mcp --token-file /absolute/protected/credential
```

Success output defaults to JSON. `--readable` renders record/history tables and readable
field details; it is mutually exclusive with `--json`. `--json` emits one structured error envelope, including input decoding and
file errors, before returning a nonzero exit status. `--file -` reads operation arguments from stdin.
`create` and `update` take an operation envelope, for example:

```json
{
  "record": {
    "schema": "document/1",
    "key": "operations-guide",
    "summary": "Restore procedure",
    "payload": {
      "schema": "document/1",
      "id": "guide/../operations",
      "name": "Operations guide",
      "subtype": "procedure",
      "format": "markdown",
      "body": "# Restore\n\nRead the backup manifest first.\n"
    }
  }
}
```

Authored input accepts only `schema`, `key`, `summary`, `source` (uri, revision,
observed_at), `payload`, `metadata` and tool-only `examples`. Updates replace this whole input, with
omission clearing optional fields; they are not PATCH. The common operation
arguments include `id`, `version`, `allow_unresolved` and `idempotency_key` where
applicable. Use discovery to retrieve each kind's strict schema.

`registry mcp` is an HTTP client using stdio framing; it has no local database,
local-trust bypass or independent authority. Its named `registry_*` tools have
operation-specific input schemas. HTTP MCP at `/mcp` calls the same service layer.
Both preserve original argument JSON before generic SDK float conversion, so
strict numeric admission across payloads and envelope metadata, plus absent/null
definition hashing, agree with HTTP. The shared admission policy rejects unsafe
integral values, nonzero underflow and overflow before any mutation or receipt.
Represent exact values outside the safe numeric range as strings.

## HTTP and tool contract

IDs are JSON or query values, **never path components**. In particular,
`tool:mcp/example/..`, `tool:cli/git/push` and slash-bearing content IDs survive
all three transports exactly. Use `GET /v1/record?id=<URL-encoded whole ID>`.
GET operations also accept `args=<URL-encoded JSON operation envelope>`; all
other methods accept that envelope as JSON. `schema` is a convenience query
selector on `/v1/schema`. This fixed-route contract avoids dot-segment cleaning
by routers/proxies.

| HTTP | CLI | MCP |
|---|---|---|
| GET /v1/kinds, /v1/schema | kinds, schema | registry_kinds_list, registry_schema_get |
| POST /v1/records | create | registry_record_create |
| GET /v1/record | get | registry_record_get |
| GET /v1/records, /v1/search | list, search | registry_record_list, registry_record_search |
| PUT /v1/record | update | registry_record_update |
| POST /v1/record/archive | archive | registry_record_archive |
| DELETE /v1/record | delete | registry_record_delete |
| POST /v1/record/verify | verify | registry_record_verify |
| GET /v1/revisions, /v1/revision | history, revision | registry_revision_list, registry_revision_get |
| POST/GET/DELETE /v1/relations | relation-create/list/remove | registry_relation_create/list/remove |
| GET /v1/tools/resolve, /v1/tools/classification | tool-resolve, tool-classify | registry_tool_resolve, registry_tool_classify |
| POST /v1/tools/proposals | tool-propose | registry_tool_propose |
| GET /v1/reviews; POST /v1/reviews/decisions | review-list, review-decide | registry_review_list, registry_review_decide |
| GET /v1/snapshot, /v1/diagnostics | snapshot, doctor | registry_snapshot_get, registry_diagnostics |
| GET /v1/references | references | registry_references_report |
| POST /v1/record/migrate; GET /v1/migrations | migrate, migrations | registry_record_migrate, registry_migration_list |
| POST /v1/relations/decide | relation-decide | registry_relation_decide |
| POST /v1/maintenance/retention | retention | registry_retention_run |
| POST /v1/maintenance/backup | backup | registry_backup_create |

For operations with more fields, CLI `--file` carries the same operation
arguments as HTTP/MCP. Discovery documents `relation`, `edge_id`, `direction`,
`hash`, `classification`, `decision`, `reason`, `approval`, and alias selectors.
No shell interpolation is needed for authored bodies.

Mutations require the current `version` token; HTTP also accepts `If-Match`.
Responses contain immutable `revision_id`, `version_token` and timestamps.
Records without edges also carry a strong representation ETag, accepted in
`If-Match` while its authored revision and observation state remain current.
Verification changes that ETag without changing `version_token`; clients using
the token directly condition only authored content. Live records with resolved edges omit
it because target state can change independently of the source version; their
`version_token` still controls writes. Fixed-boundary snapshots retain ETags.
Stale writes/reviews return `version_conflict` (409). Creation receipts
bind the authenticated caller, authority, key and complete request content;
changed-content retries conflict. A retry returns the original creation response.

`query` supports kind, subtype, lifecycle, authority, owner, key, review_state,
relation, search, full, limit and cursor. Limit is 1–200, default 50. Search uses SQLite FTS5 token phrases over
name/summary/payload bodies, with separate immutable draft/publication indexes. Default lists omit bodies and archived/deleted
records; `full` includes payloads, and lifecycle `*` includes tombstones.
A cursor binds the caller and complete query to immutable record and edge
revisions at a fixed boundary. Updates, archives, deletion and later creation
cannot change later pages. Snapshots return schemas, boundary and as_of; ETags
support conditional GET. History uses `revision` as a before-revision cursor.
Cursor/receipt/draft-history retention is configurable and defaults to disabled.
Every revision of an ever-published identity is preserved. See the
[hardening and restore runbook](docs/hardening.md) for protection rules and
the prepared, disabled backup job.

Archive retains content and history. Delete requires archive and creates a
permanent tombstone: no purge, ID reuse or cascading deletion. Reading a deleted
record returns `deleted` (410); snapshots/history retain the tombstone and prior
revisions. Canonical IDs are globally unique: project IDs are `project:<slug>`,
tool IDs follow TR-1 verbatim, and other IDs prefix `<kind>:` to the exact native
payload ID. Keys are unique within kind and publication authority.

Source attestation (`last_verified`, `verified_by`, `verification_scope`) is
server-assigned on accepted publication or explicit verification. It means the
authenticated provider attested this definition, not that references exist.
Generic writes cannot set effective reviews, reviewer identities or these stamps.
Workstream names may be empty/absent in payload; the response display name then
falls back to its canonical ID without rewriting the authored payload.

## Relationships and review

Stored edges use shared FC-8/TR-1 types. `from` is the local Registry canonical
reference; external targets preserve `{authority, kind, id, uri?}`. Missing local
targets require `allow_unresolved`; external targets remain `unchecked`. Reads
show resolved/unresolved/archived/deleted state and check time. Incoming reads
(`direction: incoming` or `both`) derive named FC-8 inverses while preserving
the stored direction. Every graph mutation revises the source atomically;
project payload edges are a projection of the same collection. Project edges
use local canonical targets because project/1's authored edge shape has no
external-authority slot. Other kinds can use external graph references.

Generic graph writes create proposals, including mined edges; they cannot claim
reviewed evidence. Launch profiles and other authored-body references remain
retrievable even when unavailable. Reading a reference never grants launch or
execution permission. `relation-decide` accepts a configured independent reviewer, current source
version, edge ID, returned edge `hash`, `decision: reviewed` and a reason. Weights
are explicit shared-schema values in [0, 1], never inferred or permission grants.
Generic writes cannot claim reviewed status. Changing attributes requires a new
proposal; a stale version/hash cannot approve replacement content. Legacy edges
without a known proposer must be recreated before review. Elevated agent/config,
agency and launch-profile relationships require the existing owner reviewer.
This decision revises only the draft; publication is still separately approved.

Tool inputs may include up to 50 uniquely named authored `examples` in the
Registry envelope, each with `name`, optional `description`, object `arguments`
and optional JSON `result`. They are secret-scanned documentation, never executed
or asserted to be runtime-tested. They affect publication content approval while
leaving the tool definition/classification hash unchanged. Example:

```json
{"record":{"schema":"tool/1","key":"lookup","payload":{"schema":"tool/1","id":"tool:mcp/example/lookup","source":"mcp","origin":"example","lifecycle":"active","definition":{"name":"lookup","input_schema":{"type":"object"}}},"examples":[{"name":"Lookup guide","arguments":{"id":"document:guide"},"result":{"found":true}}]}}
```

New tool definitions open an unreviewed queue item. Hash changes invalidate the
current effective classification atomically; previous decisions remain in
immutable history. Display-only changes retain approval when the exact TR-1
hash is unchanged. Proposals and decisions require both version and current
hash. Classification reads return declared hints, current review and conservative
defaults; absent fields and non-reviewed states must be treated pessimistically.
Archived/deleted definitions are not active tools. This service does not install
runtime enforcement or policy exports.

Ground reviewers can confirm only read-only, ground-tier, no-egress/no-secret
classifications consistent with explicit non-destructive/read-only/closed-world
declared hints. Conditional argument/flag rules require elevation. A ground
reviewer cannot override a same-hash restrictive, denied or quarantined decision,
even after a new proposal; the latest immutable decision remains authoritative.
Everything else stays `needs_human` for a ground reviewer.
Configured elevated/owner reviewers require an `approval` typed reference to an
existing approval/evidence record for those decisions, plus a reason. Registry
records that attestation, bound only to the new decision; the existing authority
owns the approval workflow.
No request can select its own reviewer role. Review scopes and publication
scopes are separate.

Errors preserve code, message, field, remedy, retryable and request_id (HTTP)
over API/CLI/MCP. Main codes are validation_error, unknown_schema, not_found,
deleted, reference_unresolved, alias_conflict, version_conflict, forbidden and
unavailable. Bodies are limited to 2 MiB; no bulk import or telemetry ingestion
is included.

## Registry-owned identities

`POST /v1/workstreams/ensure` and `POST /v1/correlations/register` mint stable
Registry identities with atomic, 30-day replay receipts and permanent logical
uniqueness. See [API, authorization and enrichment reference](docs/minting.md).

## Verification and deployment

The offline [project draft migration](docs/project2-migration.md) promotes
authored metadata into `project/2` fields once, with agency-owned `registry/contracts` validation.
The contracts module preserves each supported version independently, including
`project/1`; `project/2` requires authored type and disposition. Applications keep
their own object schemas and sync from Registry explicitly.
See [contracts provenance](contracts/PROVENANCE.md).

```sh
go test -race ./...
go vet ./...
(cd contracts && go test -race ./... && go vet ./...)
go build ./...
go run github.com/hollis-labs/substrate/cmd/substrate pins check --app .
```

The repository-root registry check/build workflows produce versioned fleet
artifacts. See [deployment plan](docs/deployment.md) before installation.

### Publication operations

| Operation / MCP suffix | HTTP | CLI |
|---|---|---|
| publication_approval_prepare | POST /v1/publication/prepare | publication-approval-prepare |
| publication_approval_get | GET /v1/publication/approval | publication-approval-get |
| publication_approval_decide | POST /v1/publication/decide | publication-approval-decide |
| publication_publish | POST /v1/publication/publish | publish |
| publication_policy_get | GET /v1/publication/policy | publication-policy-get |
| publication_policy_set | PUT /v1/publication/policy | publication-policy-set |

All accept the common JSON operation envelope (`--file`). Prepare/publish require
write scope, decisions/policy changes require review scope, and inspection requires
read scope. Publication additionally requires read scope for historical replay.
The same operation schemas are exposed through HTTP and stdio MCP.

Published export plan/diff/apply, publication changes cursors and temporary batch
import contracts are described in [materialization](docs/materialization.md).
The [agent guide skill](skills/use-registry/SKILL.md) covers lookup, authoring,
reference reporting and the separate review/publication boundaries.

Tool package publication and installation metadata: [tool-package/1](docs/tool-packages.md).

### Host inventory

The [`host/1` contract](docs/hosts.md) describes current machines and their
service/config/runbook pointers, with operator and project graph edges.
