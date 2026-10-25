# Changelog

All notable changes to caster are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases use `caster/vX.Y.Z` tags in the tools monorepo. Add a `## vX.Y.Z`
section before running the repository release script. No releases are tagged yet.

## Unreleased

## v0.0.1 - 2026-10-25

First tagged release. Pre-1.0: interfaces can change between 0.x versions.

### Security

- Require Go 1.26.10 for GO-2026-6809 (crypto/x509); no behavior change.
### Changed

- Published in the public `hollis-labs/tools` monorepo with preserved history,
  MIT licensing, portable setup examples and per-module Go checks.
- Updated the Go module and install path to `github.com/hollis-labs/tools/caster`.

### Added

- Initial scaffold (folio `go-service-app`).
- Store: write-once content-addressed blobs (sha256, digest verified on write and read), SQLite index with name revisions and FTS5; named pointers `caster://ns/name[@rev]`, bare names in ns `scratch`.
- Commands: `put`, `get`, `head`, `snap`, `ls`, `log`, `search`, `scan`, `expand`, `paths`, `token`; exit codes follow the helper protocol (`0/2/3/1`).
- Resolvers: `cas://`, `caster://`, `torque://task/<id>` (Torque HTTP), `tesseract://item|revision/<id>` and `tesseract://<domain>/<ns>/<key>` through the Tether catalog's Tesseract MCP server (caster holds no Tesseract credentials), legacy `tesseract://<ns>/<key>` through the local CLI.
- Service layer with domain services (`Notes`, `Refs`, `Actions`, `Shadow`, `Access`) as interfaces, one authorization point inside the service (scopes `caster.read|resolve|write|act|observe|admin`), thin CLI, MCP (stdio) and HTTP transports, and the transport-boundary lint gate.
- Actions: `caster actions` / `caster act`; `save-handoff` snapshots a ref and writes a Tesseract workspace draft with provenance, idempotent by content and session, never into `user/*`.
- Shadow mode: `caster shadow observe|report|label`, `POST /v1/observe`; extractors for URIs and Torque ids (high confidence) and bare ULIDs and hashes (low), stores no message text.
- HTTP API (`caster serve`) on loopback with go-auth in enforce mode: device tokens, Tether-issued JWTs (`aud=caster`) and the local operator key.
- Caster-native pairing: `caster pair` (one-use code, 5 minutes default, 1 hour max) exchanged for a 30-day, renewable, revocable device token; hashes only; open exchange route is rate-limited and size-limited and answers every failure identically; attempts are logged by shape, never the code; `caster devices`, `caster revoke`.
- Remote client: `caster login|whoami|remote|renew|logout`; the CLI and `caster mcp` run against a home node with the token in the secrets library (keychain, or a 0600 file store), profile holding only a URL and a reference; plain http only to loopback or a tailnet address, redirects never followed.
- Browser UI at `/` on `caster serve`: list and search, markdown view built from DOM calls, edit and save (new revision), history, restore into editor, and a pairing dialog opened by `caster pair -link <url>` (code carried in the URL fragment). Strict CSP, no inline script.
- Deployment: Cerberus resource `caster-uat` (systemd user service on the home node), `make dist` client binaries for darwin and linux (arm64, amd64), `deploy/tailscale-serve.sh` for tailnet-only HTTPS, draft Tether catalog entries for a scoped MCP server and a `caster` agent in `deploy/tether/`.
- Docs split by topic under `docs/`; `CONTRIBUTING.md` lists the invariants and the tests that guard them.

- Client accepts a literal device token from the environment (`CASTER_TOKEN`, or a `cst_…` value in `CASTER_TOKEN_REF`) for launchers that resolve secret references themselves, as Tether's gateway does; it is never saved to the profile or printed.
- MCP `tags` accepts a list as well as the documented space-separated string.

### Known issues

- No delete operation; no garbage collection.
- Store timestamps are `RFC3339Nano` strings compared lexicographically; they should be fixed-width.
- A task id inside a URL path is a false positive for the `torque-id` extractor.
- Revocation of a Tether-issued JWT relies on its short lifetime (the deny-list needs a credential); Tether cannot yet mint `caster.*` scopes for paired devices (`CW-20261016-0178`).
