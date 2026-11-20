# Materialization and temporary import connectors

Registry exports published definitions. Generic writes and imports create drafts.
It does not activate agents, install permissions, resolve secrets, cut consumers
over, or enable a scheduler.

HTTP, HTTP-client CLI and MCP share these operations. MCP prefixes names with
`registry_`; CLI replaces underscores with hyphens and accepts `--file` JSON.

| Operation | HTTP | Required scope |
| --- | --- | --- |
| `export_plan` | POST `/v1/export/plan` | registry.read |
| `export_diff` | POST `/v1/export/diff` | registry.read |
| `export_authorize` | POST `/v1/export/authorize` | registry.write |
| `export_apply` | POST `/v1/export/apply` | registry.write |
| `export_attempt_get` | GET `/v1/export/attempt` | registry.read |
| `changes_list` | GET `/v1/changes` | registry.read |
| `import_prepare` | POST `/v1/import/prepare` | registry.write |
| `import_commit` | POST `/v1/import/commit` | registry.write |
| `import_preparation_get` | POST `/v1/import/preparation` | registry.write |
| `import_checkpoint` | GET `/v1/import/checkpoint` | registry.read |

Writes/import checkpoints also require the configured owning authority. Only the
existing explicit publication approval path can publish imported drafts. Reserved
elevated publication remains closed without its verified human integration.

## Published export plan and authorization

`registry.export_targets` maps operator-selected logical names to absolute
isolated output directories. An empty map disables materialization. Remote input
never supplies filesystem paths. One Registry process must own each output root.

```json
{"export":{"target":"fixture","ids":["project:example"],"expected_generation":""}}
```

Plan is read-only: it creates no authorization, receipt, cursor or canonical row.
It returns a deterministic `bundle` and the shared materializer's on-disk change
plan. Omitted IDs select every active supported publication; explicit IDs select
an exact scope. Drafts are excluded. `export_diff` takes that `bundle` plus its
`hash` and recomputes a read-only change report.

A separate authorization request accepts `bundle`, `hash`, a scoped
`idempotency_key` and optional `expires_at`. It verifies exact selected published
record/edge revisions, recursively pinned dependencies and their current
eligibility in one Registry transaction. It binds the manifest bytes, destination,
caller, owning authority, output-directory identity and baseline manifest, active
policy revision, authorization time and start
deadline. Default deadline is 15 minutes; an explicit deadline must be within
24 hours. Same-key/same-request replay returns the same authorization, including
after expiry; a changed request conflicts. It never silently renews permission.

There is no global graph hash: unrelated publication does not invalidate an
explicitly selected scope. A newer dependency publication does not repin an
existing authored reference. Archived/deleted dependencies block new
authorizations. Every exported source records its exact record and edge-set
revision, path and content digest; `registry-sources.json` is an owned output.
The bundle also includes exact publication subjects for its pinned closure.

To start one authorized attempt:

```json
{"receipt_id":"<receipt_id>","hash":"<bundle.digest>"}
```

At the materializer boundary, Registry verifies receipt identity, bundle bytes,
configured destination binding and deadline, then atomically records **started**
and releases its DB transaction. Only then does it invoke the BOM-selected
[harness/core Engine](https://github.com/hollis-labs/libs/tree/main/harness/core/workspace/materialize).
Registry rejects existing symlink components and pins the output manifest bytes in
the authorization, checking them again before invoking Apply. The engine checks
output generation and observed ownership/drift during planning. These checks do
not provide race-proof per-file CAS: another writer can change paths or bytes
between inspection and mutation, including the manifest save. Use an isolated
root with one cooperative writer. Filesystem failure can leave partial writes;
there is no atomic filesystem rollback.
Registry records completed/failed outcome and any partial materializer report
against the consumed receipt. `export_attempt_get` reads that outcome. No
Registry DB transaction is held during filesystem work.

Authorization is a snapshot boundary, not a cross-system revocation fence.
Archive before authorization blocks it. Archive after authorization blocks new
authorizations but does not retract the already-issued receipt or undo external
writes. The deadline bounds when an attempt may **start**, not when all writes
finish. The receipt grants no launch/deploy authority. A consumed receipt cannot
start another attempt: even identical retries return a conflict and direct the
caller to its outcome. After success, failure or a crash with an uncertain
started outcome, inspect/reconcile the output and obtain fresh authorization for
any new attempt. A fresh authorization is required even for a deterministic rerun.

Initial apply requires an absent destination. Refresh supplies the current
materializer generation in `expected_generation`. A fresh authorization for
identical content reconciles the same generation and unchanged files. External
owned-file edits are reported by diff and refused by apply; unowned files remain.
Registry rechecks each write/removal and the control-manifest commit against its
observed baseline; Cairn commits manifests through anchored directory handles,
including no-op refresh. These checks do not claim an OS-wide external-writer lock.
Filesystem refresh follows the shared materializer's partial-failure/recovery
contract, not SQLite atomicity. Export scopes are bounded to 200 records in the dependency closure and 2 MiB
of rendered content. Large scopes must fit the HTTP request cap when
resubmitted for authorization; partition explicit scopes when necessary.

## Output mappings and source bundles

`registry-export/1` writes agents to `agents/<hex-identity>/definition.yaml`,
skills to `skills/<hex-identity>/SKILL.md`, documents/knowledge bodies to `docs/`,
config settings to `config/`, project descriptors to
`projects/<hex-identity>/project.yaml`, and launch-profile source references to
`launch_profiles/`. Opaque complete canonical IDs use lowercase hex encoding;
keys and path segments are never reinterpreted as identity. Config secret
locators are not resolved and source launch semantics are not inferred.

[Substrate agents.dir](https://github.com/hollis-labs/substrate/blob/main/docs/agents.md)
is the config key, defaulting to `agents/`. Source adapters can preserve native
`agent.md` and `host.yaml` in `metadata.agent_files`, following the
[Folio directory preset](https://github.com/hollis-labs/tools/tree/main/folio/presets/agent-dir).
Native bundles require the exact lowercase names `agent.md` and `host.yaml`;
missing bundles are non-materializable. The shared agent-definition parser and
agent-directory host-profile validator check both files before authorization. No runtime permission/host field is inferred from a catalog role.

`metadata.skill_files` retains auxiliary text. Both attachment envelopes are
arrays of `{ "path": "references/guide.md", "content": "text" }`. Paths are
confined relative paths, unique under case folding, with the primary generated
filename reserved. Attachments use mode 0600, are never executed, and each gets
its own source manifest entry. Binary attachments require a future explicit
encoding/admission contract and must be reported as unsupported, never dropped.

## Publication changes

`changes_list` returns immutable publication events in ascending sequence,
including lifecycle removals. Draft edits never appear. It accepts only
`query.cursor` and `query.limit` (1–200) and always returns a caller-bound cursor,
even when empty. Idle polls reuse the cursor. It can be replayed and can resume
future publications; consumers handle the returned lifecycle explicitly.

## Durable import preparation

```json
{"idempotency_key":"prepare-snapshot-1","import":{"connector":"temporary-example","source_authority":"example","mapping_version":"example/1","expected_checkpoint":"","checkpoint":"snapshot-1","entries":[{"source_key":"stable-native-id","source_revision":"immutable-v1","record":{"schema":"project/1","key":"example","payload":{}}}]}}
```

Payload above is abbreviated; full kind/schema/secret admission is mandatory.
Preparation is a distinct durable staging store, not a canonical reservation.
It allocates immutable proposed record/edge-set revisions and freezes its sorted
manifest, exact bases, source positions and candidate edges. Forward/cyclic
references resolve only inside the staging set plus pinned existing records.
Normal record/graph APIs cannot see staging. Multiple preparations can target
one identity; only the first still-valid commit wins its canonical CAS.

The scoped preparation key and canonical full request digest are retained.
Same-key/same-request retries return the original preparation/IDs even when
expired; changed requests conflict. Expiry never refreshes or reallocates a key.
No cleanup job deletes evidence or idempotency keys. Invalid rejected payloads
are omitted from persisted staging; only sanitized diagnostics remain.

Default explicit limits are 2 MiB per preparation request, 200 items, 20 active
uncommitted preparations per authenticated caller and 24-hour commit eligibility.
`registry.import_limits` can lower `max_bytes`, `max_items`,
`max_active_preparations`, and `eligibility_seconds`; values cannot exceed these
defaults. Quota failure allocates nothing and returns `quota_exceeded`. Existing
key replay precedes current quota checks. Reprepare after expiry needs a new
key, new IDs and any required approval.

Preparation `planned_counts` are prospective, never committed/publication counts.
Commit takes `plan_id`, the immutable plan `hash`, a confirmed `source_digest`
and a separate scoped commit `idempotency_key`. Its one transaction rechecks
expiry, authority, all record/edge/source/checkpoint bases, current schema and
target eligibility/cycle policy, then inserts the **allocated** candidate
revisions, source tuples, checkpoint and result receipt atomically. It does not
regenerate different revision or edge IDs. Publication is a separate operation.

Any canonical failure rolls back all records, edges, aliases, checkpoints and
success receipt, while retaining staging and separate sanitized failure evidence.
The structured error reports zero committed created/updated/unchanged, with
`rejected + not_committed = input_count`. A successful result has
`created + updated + unchanged = input_count`. A committed exact-key replay
returns its original receipt/counts with `replayed:true`, including after expiry;
it does not rewrite rows/checkpoints. A committed plan cannot be applied under a
new key. Failed attempts may retry before expiry if every baseline still matches.

Each source checkpoint records authority/native ID, accepted source revision and
body digest, mapping version, **resulting** Registry and edge-set revisions and
import receipt identity. Unchanged source plus those same resulting revisions
reports unchanged. Local edits conflict even when upstream is unchanged;
concurrent source edits produce a two-sided conflict. Mapping changes require a
new candidate. Changed source revision labels require
`expected_source_revision` equality evidence against the accepted source base;
unordered opaque revisions are not guessed newer. Missing records never imply
deletions. Generic imports cannot stamp review/publication.

## Temporary connectors

`Connector` and `RunConnector` require an explicit stable run identity. The
caller or external scheduler chooses it before the first request, reuses it for
uncertain retries, and chooses a new one for an intended new synchronization.
The run key excludes source bytes and mutable Registry checkpoints.

The runner first calls `import_preparation_get` with `idempotency_key`. This
caller/authority-scoped lookup recovers the frozen preparation and whether it
committed. A committed run replays its original receipt without reading current
sources or replacing its baseline. An uncommitted run confirms the source against
its saved checkpoint before committing; changed bytes conflict. Response loss at
prepare or commit therefore survives process restart. A new run reads the current
checkpoint, snapshots the source, prepares, confirms it and commits atomically.

A remote source cannot be locked by SQLite: adapters must use immutable snapshots
or hold their own lease through commit. There is no reverse push or implied
cross-source atomicity.

`registry import-run --connector NAME --run-id RUN --file snapshot.json` is the
concrete temporary JSON snapshot adapter. It reads an explicit file rather than
stdin, uses HTTP only, and opens no local Registry DB. Retain stable connector and
native keys and supply exact source authority/mapping/revisions. Failed runs leave
the last committed checkpoint available. Expired preparations are not renewed;
inspect the old run and choose a new run ID after reviewing current bases. Use the
same run ID to recover uncertainty, even if the source has since moved. A new run
checks current local drift; replay intentionally returns its historical result.
No scheduler is installed or enabled; an external scheduler can invoke the same
command during migration. Remove temporary adapters at consumer cutover.
