# Materialization and temporary import connectors

Registry exports published definitions; generic writes and imports create drafts.
This facility does not activate agents, install runtime permissions, resolve
secret references, cut consumers over, or enable a scheduler.

The HTTP API, HTTP-client CLI and MCP share these operations:

| Operation | HTTP | CLI |
| --- | --- | --- |
| `export_plan` | POST `/v1/export/plan` | `export-plan --file request.json` |
| `export_diff` | POST `/v1/export/diff` | `export-diff --file receipt.json` |
| `export_apply` | POST `/v1/export/apply` | `export-apply --file receipt.json` |
| `changes_list` | GET `/v1/changes` | `changes-list --cursor CURSOR` |
| `import_prepare` | POST `/v1/import/prepare` | `import-prepare --file batch.json` |
| `import_commit` | POST `/v1/import/commit` | `import-commit --file commit.json` |
| `import_checkpoint` | GET `/v1/import/checkpoint` | `import-checkpoint --id CONNECTOR` |

MCP prefixes each operation with `registry_`. All CLI commands accept `--url`,
`--token-file` and `--json`; arguments use the same JSON envelope as HTTP/MCP.
Plan, diff and changes need `registry.read`; export apply and import prepare and
commit need `registry.write`. Import operations additionally require a configured
owning authority. Existing publication approvals remain the only path to published
records, including the reserved agent/config decisions.

## Export

Materialization is disabled unless the operator configures
`registry.export_targets` as a map from logical target names to absolute output
directories. Request JSON never supplies a filesystem path. Configure only isolated
outputs for evaluation. A single Registry process must own each output root.

```json
{"export":{"target":"fixture","ids":["project:example"],"expected_generation":""}}
```

An omitted ID list selects all active, exportable published records. Explicit IDs
must be published and active. The export mappings are versioned by
`registry-export/1`: agents become `agents/<identity>/definition.yaml`, launch-profile source references
become YAML in `launch_profiles/`, skills become
`skills/<identity>/SKILL.md`, documents and knowledge become bodies in `docs/`,
config settings become JSON in `config/`, and project descriptors become YAML in
`projects/<identity>/project.yaml`. Every filename identity is the lowercase hex
encoding of the complete canonical ID, preserving opaque identity without path
interpretation or key-rename collisions. These are catalog exports, not claims of
provider-native launch compatibility. Config secret locators are never resolved.

`agents.dir` is the [Substrate config key](https://github.com/hollis-labs/substrate/blob/main/docs/agents.md),
not a literal directory name. The output uses its default `agents/` with one
subdirectory per identity. Source adapters can preserve native `agent.md` and
`host.yaml` in `metadata.agent_files`, matching the
[Folio agent-directory preset](https://github.com/hollis-labs/tools/tree/main/folio/presets/agent-dir).
These bytes are copied as inert authored files; no runtime permission or host
configuration is inferred from a catalog role. Native bundles require both files.
Without that source bundle the agent directory contains only `definition.yaml`.

Skills can retain auxiliary text files in `metadata.skill_files`. Both metadata
envelopes are arrays of `{ "path": "references/guide.md", "content": "text" }`.
Paths must be confined relative paths, unique even under case folding, with the
generated primary filename reserved (`SKILL.md` or `definition.yaml`). Files use
mode 0600 and are never executed. Every attachment gets its own source-manifest
entry. Binary source attachments require an explicit future encoding contract;
the current connector must report them as rejected instead of dropping them.
Existing kind schemas and admission remain authoritative.

`registry-sources.json` records source IDs, exact published revisions, destination
paths and SHA-256 content digests. The deterministic bundle also carries shared
materializer artifacts with ownership and provenance. Generation depends on the
published snapshot and content, independent of the expected previous generation.

Plan issues an opaque, server-stored authorization receipt. To diff or apply:

```json
{"receipt_id":"<receipt_id>","hash":"<bundle.digest>"}
```

At the materializer boundary Registry verifies the caller, authority, action,
bundle digest and current published event boundary/revisions, then invokes the
shared [harness/core materializer](https://github.com/hollis-labs/libs/tree/main/harness/core/workspace/materialize).
Draft edits do not affect an authorized published snapshot. Any publication since
plan requires a fresh plan, including additions or lifecycle removals. A receipt
cannot be copied to another principal or changed to point to another target.

The adapter uses the BOM-selected shared artifact types and Engine Plan/Apply;
it does not implement a second filesystem writer. Initial apply requires an absent
output. Refresh requires the current materializer generation in
`expected_generation`. Identical apply retries reconcile the same generation.
Diff reports owned output drift; apply refuses conflicts. Hand-authored unowned
files are preserved. The shared reconcile engine reports partial failures; a
filesystem refresh is not a SQLite transaction. Reconcile again after inspecting
its report. No source or consumer directories are implicitly targeted.

## Publication changes

Changes are immutable publication events in ascending sequence, including archive
and deletion events. Draft changes never appear. Only `query.cursor` and
`query.limit` (1–200) are accepted. The response always includes a caller-bound
cursor, even when empty, to resume future polls. Changing the page size is allowed.
An idle cursor is reused, and a cursor may be replayed to retrieve the same prefix
plus later committed events. Consumers must handle the returned lifecycle.

## Import transaction

```json
{"import":{"connector":"temporary-example","expected_checkpoint":"","checkpoint":"source-snapshot-1","entries":[{"source_key":"stable-source-key","source_revision":"immutable-source-revision","record":{"schema":"project/1","key":"example","payload":{}}}]}}
```

The payload above is abbreviated; each record must pass its full shared kind
schema and secret checks. Batches contain 1–200 entries. Prepare sorts source keys,
validates admission, binds each stable source key to one immutable Registry
identity, captures Registry revision baselines and checks the source checkpoint.
It returns an immutable caller/authority-bound plan, source snapshot digest,
per-entry action/reason and created/updated/unchanged/rejected counts. Prepare
writes only its plan, never records, source bindings or checkpoints.

```json
{"plan_id":"<plan_id>","hash":"<digest>","source_digest":"<confirmed source_digest>","idempotency_key":"stable-commit-key"}
```

Commit requires an exact source snapshot confirmation and a caller-scoped retry
key. A committed identical retry returns its original counts/checkpoint with
`replayed:true`; a changed request using that key conflicts. All Registry baselines
and the checkpoint are rechecked inside one write transaction. Any rejection or
late failure rolls back the entire batch, including revisions, aliases, source
bindings, checkpoint and receipt. Forward references within a batch are admitted
only when the resulting graph resolves. Imports never publish or attest review.

Tracked sources distinguish source-only updates, unchanged content, Registry-only
edits and edits on both sides. Registry-side drift is never silently overwritten;
`registry_changed` or `both_sides_changed` rejects it. Source revision reuse with
changed bytes, key reassignment, an already claimed Registry identity, tombstones
and archived records are rejected. There is no deletion inferred from absence.

`Connector` and `RunConnector` provide a reusable temporary adapter contract using
these same service operations. The runner reads a snapshot, prepares it, reads it
again, compares the confirmed digest and commits. Connectors must supply immutable
snapshot/revision identities or hold a source lease through commit: Registry cannot
atomically lock an external service. Cross-source atomicity is not implied. No
scheduler is registered or enabled; an external scheduler can explicitly invoke a
connector later. Failed runs leave the last committed checkpoint available for
rerun. Source counts describe only the submitted observed snapshot.

The concrete `registry import-run --connector NAME --file snapshot.json` adapter
reads an operator-selected JSON import envelope twice and invokes this runner over
HTTP. It requires a seekable file instead of stdin so the source can be confirmed
again before commit. File inputs are temporary snapshots, not live services;
use immutable snapshot files during a run. The same connector name and stable
entry keys are retained across reruns. No Registry database is opened by this CLI.
