# Agency contracts provenance

This module is authoritative for Registry's agency-defined kinds. Applications
keep their own object schemas and sync from Registry explicitly. Substrate's
existing kinds package remains unchanged for its other consumers.

The staged source is `github.com/hollis-labs/substrate/kinds` at
`v0.1.1-0.20261119200612-112799f6da86` (commit `112799f6da86`), the version
Registry pinned when this module was created. Its 13 record kinds and the two
auxiliary relation/classification schemas are copied byte-for-byte. The
machine-readable SHA-256 manifest is `schema-provenance.json`; the upstream
fixture tests are retained with only their import path changed. In addition,
`testdata/pinned-fixtures.json` freezes 30 accepted/rejected outcomes across all
15 schemas, independently checked against that exact upstream validator before
switching Registry imports; `TestPinnedFixtureParity` runs the frozen corpus. The original
license is retained. Validators, references, hashes and secret checks were
staged from that same source to preserve admission behavior.

New versions are authored here. `project/2` adds required `type` and
`disposition`, restricts `class` to integer tiers 1 (core), 2 (service), 3 (app),
and permits an absent class for framework, monorepo and lib. Only type lib
requires exclusively lib interfaces. No default, metadata fallback or implicit
migration is applied. `project/1` continues to validate under its own unchanged
schema; the project parser dispatches by the authored schema version.

The module path is `github.com/hollis-labs/tools/registry/contracts`. Future
semver tags use `registry/contracts/vX.Y.Z`; this change does not release a tag.
Run `go test -race ./...` and `go vet ./...` in this directory independently of
the parent Registry module.

## tool-package/1

Agency-authored for CW-20261120-0220. Unlike the staged upstream schemas listed
in schema-provenance.json, tool-package/1 originates here. Its strict wire shape,
identity checks and secret admission are covered by tool_package_test.go.
Registry owns immutable version mapping; consumers own archive verification.

## document/1 agency extension

CW-20261121-0046 adds the architecture, template and communication subtypes to
`document/1`. `schemas/document-1-registry.json` is the authoritative agency
extension exposed by `Schema`; the staged `schemas/document-1.json` remains
unchanged and checked against the original manifest. The frozen source fixtures
still pass; original document payloads require no migration. The extension adds
subtype-specific required objects without changing existing subtype admission.
See the README for fields and the pointer/edge-over-copy content guide.

Registry publication admission hashes cover the complete exposed schema bundle.
This extension changes that digest; publication plans prepared under the previous
bundle must be prepared again after adopting the updated contracts.

## Nanite materialization agency extensions

CW-20261121-0055 adds optional materialization fields through
`schemas/agent-1-registry.json` and `schemas/launch-profile-1-registry.json`.
Schema discovery exposes these extensions; the original staged schemas and
manifest remain byte-for-byte unchanged. Existing fixture admission is retained.
As with document/1, adopting this schema bundle changes the publication digest;
previously prepared publication plans must be prepared again.
