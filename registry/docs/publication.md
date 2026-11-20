# Publication

Every record starts as a draft. Editing payloads or authored relationships makes
another immutable draft; the published revision and outgoing graph remain visible
until an explicit publication succeeds. Existing databases migrate to drafts, with
no inferred approval or automatic publication.

Normal get, list, search, tool resolution, relation and snapshot reads select
published records. Use `publication: "draft"` for a single read or
`query.publication: "draft"` for lists; the CLI accepts `--publication draft`.
History APIs remain explicit immutable revision inspection. The review queue
selects drafts. An archived/deleted published record retains historical evidence;
archive/delete immediately changes its publication availability. A payload edit
never publishes that edit. Future materializers must use the published view and
pinned dependencies, not draft/history selectors.

## Prepare, decide, publish

1. A writer calls `publication_approval_prepare` with `candidates`, each containing
   `id`, exact current draft `revision`, and `expected_published_revision` (empty
   for first publication). The result is a pending approval with an immutable
   manifest and subject digests. Optional `expires_at` is a future RFC3339 instant.
2. A reviewer calls `publication_approval_decide` with `approval_id`, current
   `approval_revision`, the exact `batch_manifest_digest` as `hash`, and decision
   `approved`, `denied` or `withdrawn`. Approval cannot be stamped by generic
   record writes or the separate tool-classification review.
3. A writer calls `publication_publish` with the approved candidate set,
   `approval_id`, approved `approval_revision`, manifest `hash`, and a nonempty
   `idempotency_key`. CLI: `registry publish --file publication.json`.

Publication checks the entire batch and commits every pointer, graph snapshot,
publication event and result receipt in one SQLite transaction. Any stale draft,
stale expected pointer, unavailable dependency, changed schema, invalid approval,
expired receipt or changed policy rejects the whole transaction. No partial
publication is reported.

The result receipt is separate from the approval receipt. An exact caller/authority/
key replay is checked first, requires current read and write authorization, and
returns the original committed result with `replayed: true` and separately labeled
`current_availability`. Withdrawal, expiry, archival or a later publication never
makes replay restore an old pointer. A changed request under an old key conflicts.
Candidate order is normalized by exact ID bytes. Receipts are retained; no cleanup
or retention job is added.

## Approval policy and elevated denial

The existing configured reviewer bindings bootstrap a Registry-local publication
policy once. Subsequent runtime role changes do not silently modify that policy.
`publication_policy_get` exposes its revision; `publication_policy_set` requires
an existing policy `owner`, review scope, current revision and complete `roles`
map. Each policy revision is retained. Configure an administrative owner before
first publication use if policy administration will be needed. New decisions and
publications bind the current policy revision; policy changes invalidate unused
approvals. Already committed replay remains historical evidence.

Ground, elevated and owner review roles can approve low-risk publication. Agents,
agencies, config, MCP servers, launch profiles, and tools without a reviewed
conservative ground classification require verified human approval. **That human
integration is unavailable in this version: approval and publication of these
records fail closed for every credential, including owner-role credentials.**
An external approval reference, shared operator token, role label or source
attestation is not evidence that a human decided. This change creates no human
session binding, new grant, external issuer integration or live elevated record.

Withdrawal and publication serialize on the same database transaction boundary.
Withdrawal first blocks publication; publication first remains committed and
withdrawal blocks future use. No remote revocation or filesystem atomicity is
claimed. This phase performs no materialization, imports or deployment.

## Publication subject version 1

`registry.publication-subject/1` uses the shared accepted JSON numeric domain and
JCS UTF-8 canonicalization followed by SHA-256, separately from TR-1 tool hashing.
The explicit projection contains:

- `schema`, `action: "publish"`;
- `record`: canonical ID, versioned kind, immutable revision, authenticated
  owning authority, and complete authored content digest;
- `edge_set_revision` (the immutable source revision, including empty sets);
- `edges`, sorted by unique exact ID: edge ID/revision, relation, pinned
  `target: {id, revision}`, and complete typed relation attributes;
- `admission`: complete shared schema-bundle and edge-cycle-policy digest, `mapping_version: "native/1"`,
  and `envelope_version: "registry.authored-envelope/2"`.

The content digest covers the complete admitted payload and every enumerated
Input envelope field: schema, key, summary, source (URI/revision/observed_at), and
metadata. Explicit envelope versioning prevents unenumerated export inputs.
Server-created verification/review stamps, timestamps, publication state and
operational observations are excluded. `record_verify` appends a separate
observation and preserves the authored revision and approval subject.

Local semantic dependencies pin published revisions or members of the same
approved atomic batch. New target drafts/publications never move existing pins.
Archived/deleted targets block new publication. External semantic dependencies
without a supported immutable version contract fail closed; native upstream
provenance is descriptive and does not imply execution authority. Published
record and relation responses include target pins. Incoming relations derive
from each source's published graph. No physical deletion of pinned history occurs.

Secret-like values are refused before persistence with value-free errors;
structured secret locators are retained without resolution. Detection is
conservative, not proof that every opaque string is non-secret. App-owned
configuration remains in its application database.

The schema bundle includes every supported version from agency-owned
`registry/contracts`. Adding `project/2` changes the admission digest and requires
fresh approval preparation for subsequent publication. Existing published
snapshots and immutable revisions retain their original bytes.
