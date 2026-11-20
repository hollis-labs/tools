# Registry-owned launch identities

Registry mints `workstream:<ULID>` and `correlation:<ULID>` once. Application
workstream labels remain labels; this API performs no historical re-keying.
Both operations are atomic SQLite transactions: authored revision, permanent
logical-key mapping and replay receipt commit together or all roll back.

## API reference

| Operation | HTTP | Required authenticated scopes |
|---|---|---|
| `workstream_ensure` | `POST /v1/workstreams/ensure` | `registry.write`, `registry.workstream.ensure` |
| `correlation_register` | `POST /v1/correlations/register` | `registry.write`, `registry.correlation.register` |

Both require a nonempty `Idempotency-Key` header. Bodies are JSON objects with
only the fields below. Caller-supplied `id`, `authority`, principal, status,
verification and other envelope fields are rejected. The effective authority
comes exclusively from the existing server credential binding. The narrow
ensure capability is reserved for the Torque dispatcher; register is reserved
for the Tether launcher. This change does not provision credentials or scopes.
A verified agency reader with `registry.read` can retrieve the authored record
using `GET /v1/record?id=<id>&args={"publication":"draft"}` (URL-encode args).
Minting does not publish a record or bypass publication review.

### Workstreams

Request: `{"key":"stable-application-reference","name":"Optional name","project_urns":["project:example"]}`.
`key` is required and never becomes the canonical id. `name` and `project_urns`
are optional. Ensuring an existing identity does not overwrite authored fields;
a supplied different name returns `version_conflict`.

The logical-key domain is `(bound authority, project_urn, key)`. A request
with multiple distinct projects claims each project binding atomically for the
same identity; a later request selecting any of those projects returns that id.
Project order is irrelevant. Omitting projects uses an authority-wide empty
project domain, isolated from every named project. Ensuring cannot add new
project bindings to an existing identity or merge bindings that already name
different identities: those requests return `version_conflict`.
The durable map is independent of receipt lifetime. Concurrent requests using
different retry keys converge to the same id. Authored closed status, archive or
delete permanently closes the mapping: every subsequent ensure returns
`workstream_closed`, including an old receipt replay. A later generic status
rewrite cannot reopen the mapping. Reopen is a separate operation, outside this
API. Generic conditional updates cannot change a minted project domain.

### Correlations

Required string facts: `urn`, `session`, `run_id`.
Optional string facts: `task_id`, `workstream_id`, `token_jti`, `manifest_digest`,
`export_receipt_id`, `activity_sha256`, `provider_session_id`, `traceparent`.
Unknown facts stay absent. Never send token material; `token_jti` is only its
opaque identifier. Facts are provenance, never caller authentication.

The authored correlation `name` is deterministically `session + " / " + run_id`;
it is a display label, not the identity or uniqueness key.

A launch has one permanent identity per `(authority, session, run_id)`. Later
turns and compaction reuse it. A new retry key with matching facts returns that
identity; conflicting supplied facts return `version_conflict`. Registration
never silently merges changed launch facts.

Facts use their request names as string values in the existing `correlation/1`
payload `metadata` object, except `token_jti`, which maps to `metadata.jti`.
The latter names an opaque identifier and preserves existing shared
sensitive-setting refusal for `token_*` settings; no secret policy is relaxed. The session also populates `links.session` (kind `session`), qualified by the
bound launch-service authority. Optional `workstream_id` populates
`links.workstream` (kind `workstream`) under Registry. Other facts remain typed
metadata: no foreign authority mapping or team-run identity is inferred.
`traceparent` additionally populates `trace.traceparent`. These are descriptive
references, not certifications that remote records exist. No schema is forked
or version bumped; `registry/contracts` remains authoritative.

For late enrichment, read the draft and use existing `record_update`
(`PUT /v1/record`) with its current `version_token` (or `If-Match`), exact id,
schema and record key, and complete replacement payload. Preserve all existing
launch facts and links; add absent optional string facts to metadata and their
corresponding descriptive links/trace when applicable. Existing launch facts
and links cannot be erased or changed. A stale version fails with
`version_conflict`. This requires the existing owning-authority `registry.write`
permission; registration alone does not grant generic editing permissions.
The update retains the same canonical id and emits a new revision token.

### Responses, conflicts and retries

Success is `{"id":"workstream:<ULID>","created":true,"version_token":"opaque"}`
(or `correlation:<ULID>`). `created` describes this committed request, not the
current record state. An identical replay returns the original response,
including its original `created` and `version_token`, even after enrichment.
A distinct retry key that finds the identity returns `created:false` and the
current token.

Replay receipts are scoped by operation, authenticated caller, bound authority
and retry key, and expire after 30 days. Changed arguments with an unexpired
retry key return HTTP 409 `version_conflict`. Receipt expiration never deletes
the identity mapping; retrying after expiration still finds the same id.
Expired receipts are pruned in successful mint transactions. Closed mappings
remain permanently and take precedence over replaying an old success receipt.

Errors use Registry's standard `{"error":{code,message,field,remedy,retryable,request_id}}`
envelope. Missing headers or invalid fields return 400 `validation_error`;
missing capability/binding returns 403 `forbidden`; changed arguments or facts
return 409 `version_conflict`; closed keys return 409 `workstream_closed`;
transient store failures return 503 `unavailable`. Retry lost responses and
429/5xx using the same request and key. Correct authorization, validation and
conflict errors before retrying; never fall back to minting a local identity.

Tether clients should queue registration after their own local launch commit
and keep remote retries outside their startup critical path. This server
change does not alter Torque or Tether clients, deploy the service, or establish
live kind/catalog availability.
