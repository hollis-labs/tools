# Agency contracts provenance

This module is authoritative for Registry's agency-defined kinds. Applications
keep their own object schemas and sync from Registry explicitly. Substrate's
existing kinds package remains unchanged for its other consumers.

The staged source is `github.com/hollis-labs/substrate/kinds` at
`v0.1.1-0.20261119200612-112799f6da86` (commit `112799f6da86`), the version
Registry pinned when this module was created. Its 13 record kinds and the two
auxiliary relation/classification schemas are copied byte-for-byte. The
machine-readable SHA-256 manifest is `schema-provenance.json`; the upstream
fixture tests are retained with only their import path changed. The original
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
