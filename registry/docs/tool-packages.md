# Tool packages

`tool-package/1` records publish Nanite package metadata. Package bytes live in
Caster; Registry neither downloads nor executes them. The agency-owned schema
is in `contracts/schemas/tool-package-1.json`.

## Identity and content

The payload requires `schema`, `id`, `publisher`, `name`, `version`, `digest`,
`artifact`, `entry`, `execution`, `needs`, `env` and `provenance`. Only optional
`summary` and `owner` fields are also accepted; unknown fields at every object
level are refused. `publisher` is an opaque Tether `agt_*` agent ID; `name` uses
lowercase letters, digits and hyphens, and `version` is SemVer 2.0.0 (including
optional prerelease and build metadata). The payload ID must equal
`<publisher>/<name>@<version>`; the canonical record ID is
`tool-package:<publisher>/<name>@<version>`. IDs are preserved verbatim.

`digest` is `sha256:<64 lowercase hex>`: Nanite's whole-package digest.
`artifact` is `cas://sha256/<64 lowercase hex>`: the Caster content reference
for a deterministic tar of the package tree. These are separate digests and
need not match. The tar represents regular files, relative paths and modes;
symlinks, hardlinks, special files, absolute paths and traversal are forbidden.
Nanite verifies the artifact hash, safely validates the archive and verifies
the whole-package digest during installation. Registry validates reference
shape only; it cannot certify archive contents or availability.

`entry` names a package-relative file without traversal. `execution` contains
only `kind: command|mcp`. `env` is an array of environment variable names, never
assignments or values. `needs` contains `{audience, scopes: [...], env: [...]}`;
its env arrays also contain names only. Empty arrays express no needs. Credential
values and token-like strings are refused rather than scrubbed or stored.
Credentials are minted at execution time by the consumer.

`provenance` contains `publisher`, a `msg://agent/<authority>/<publisher>` URN
matching the publisher ID, and optional `source_ref`. Neither this consistency
check nor the optional `owner` asserts authenticated publisher ownership.

A package payload is immutable after creation, including its digest, Caster
artifact and execution metadata. Publish a new version to change it. Native
updates and import writes share the persistence check. Lifecycle archive is
allowed and retains the original mapping; deletion leaves a tombstone and
cannot free that version for reuse. An alternate human key or writer authority
cannot claim a second digest for the same publisher/name/version identity.
SemVer build metadata is part of the exact version identity.

## Transport draft

1. Store the deterministic package archive in Caster and retain its content ref.
2. Publish metadata with `registry_record_create`, `publication: "draft"`, via
   the mcp-gateway **`registry-write`** upstream. Set `record.schema` and
   `record.payload.schema` to `tool-package/1`, and use the package identity as
   `record.key`. A draft write is not shared-namespace promotion.
3. Resolve with `registry_record_get` or `registry_record_search` through the
   **`registry`** upstream. Supply the whole canonical ID as a JSON/query value,
   with `publication: "draft"` when resolving drafts; published reads use the
   existing Registry publication contract.
4. Fetch the resolved `artifact` content ref with **`caster_get`** via Tether MCP,
   then perform both install-time hash checks and safe archive validation.

During the development window, write authority is the gateway's **shared writer
credential**. `owner` and `provenance.publisher` are **asserted, not verified**;
Registry's source attestation identifies the configured gateway writer, not an
individual publishing agent. A publisher ID is a namespace and consistency
constraint, not a per-agent authorization boundary. Per-agent Registry
principals remain deferred hardening (CW-20261120-0136). Do not send credential
values in payloads, transport examples or package metadata.

Promotion to a shared namespace, package evals and Gatecrasher approval are
separate deferred work. This contract grants no execution or credential access.
