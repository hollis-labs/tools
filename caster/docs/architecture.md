# Caster architecture (Substrate port, in progress)

Caster keeps named text and content-addressed blobs and hands them around by reference
(`caster://`, `cas://`, `torque://`, `tesseract://`). This file is written slice by slice; a section
says which slice it covers.

## Decisions (docs/adopting-substrate.md section 0)

| Question | Answer |
|---|---|
| Migrated data or fresh start? | Migrated, all of it. The live instance has data; `caster migrate` (slice S5) copies it one way and proves the copy. The old files are never written. |
| Binary variant | `app-host` (HTTP, browser UI, stdio MCP, CLI). |
| Who issues identity? | The `identity` module: owner and keys created offline. Caster's own pairing and `cst_` tokens are removed (S3). |
| Database | SQLite only: full-text search is FTS5 and the SQL is SQLite syntax. |

## Layout (S0, S1)

- `cmd/caster`: `substrate.yaml` (manifest), the generated `substrate_wiring.go`, `main.go`,
  `config.go` (data paths), `guard.go` (the legacy guard), `serve.go`.
- `internal/settings`: the `caster:` config block. `internal/hostguard`: Host allow-list.
- `internal/storage`: the database schema (one embedded goose migration), the blob tree and every
  query; `storagetest.Open(t)` gives the tests a throwaway one. It replaces the old `internal/store`.
- `internal/service`: the service layer (S2): notes, refs, actions, shadow mode, whoami. It is the
  one place business rules and authorization live; the transports (HTTP in S3, CLI and MCP in S4)
  only stamp the caller and call it. `internal/resolver` and `internal/ref` are unchanged apart from
  taking the context into the local store.

## Persistence (S1)

- Files, all in the workspace directory (`<data>/workspaces/default`, or where `CASTER_DB_PATH` /
  `CASTER_WORKSPACE` point): `caster.db` (the Substrate database) and `cas/` (the blob tree,
  `cas/sha256/<2 hex>/<rest>`). The pre-Substrate `main.db` and `blobs/` are never opened by the new
  binary; they stay as the rollback.
- Why a new blob directory: the old tree is `blobs/` in the same place, and the rule is that the old
  files are never written, the blob tree included. The new build therefore never opens `blobs/`;
  it writes only `cas/` and `caster.db`. `caster migrate` (S5) copies every blob into `cas/` and
  re-hashes it. Step zero of the cutover plan checks that `caster.db` and `cas/` do not exist yet.
- The legacy guard: when `main.db` exists and `caster.db` does not, start fails and says to run
  `caster migrate` (or move the old directory aside to start empty). It runs before the database
  module and only looks at the files.
- Schema: the old tables, same columns, times still RFC 3339 text (so migration copies rows as they
  are). The old `devices` and `grants` tables become `legacy_devices` and `legacy_grants`: history
  only, no code reads them. `blob_fts` is derived from the text blobs and is rebuilt by the migration.
- Store contract: writes use `WriteTx`, reads the read pool; a blob file is written before the
  transaction that records it (a failed transaction leaves an unreferenced content-addressed file,
  reused by a retry). Search hands the query to FTS5 as given, as before.
- Behaviour changes from the old store, deliberate: the full-text insert is in the same transaction
  as the blob row and its failure now fails the put (it used to be ignored); linking a name reads the
  current revision and inserts the next one in one write transaction.

### Two processes on one database

The service and a local CLI or MCP process open the same `caster.db`; there are no other writers.
SQLite serialises the writers across processes (the store's write connection is one per process,
the file lock does the rest). Blob files are content-addressed and renamed into place, so two
processes writing the same bytes agree. `TestTwoHandlesOneFile` runs two independent store handles
on one file, each linking the same name 15 times in parallel: every link succeeds, the revisions are
consecutive, and every blob reads back. The identity signing key is created only when the database
holds no users or keys, and its path is `identity.key_file` from the same config in every process;
the offline bootstrap (S3) is what creates the owner, so a second process never creates a second key.

## Authorization (S2)

Scopes stay `caster.read`, `caster.resolve`, `caster.write`, `caster.act`, `caster.observe` and
`caster.admin`, held by identity-module API keys and checked in the service layer from the verified
principal in the context (`caster.*` and `*` match all). `service.LocalScopes` is the principal of a
local stdio process or CLI command run without a key: read, resolve, write, observe. It never holds
`caster.admin` or `caster.act`, because local trust is the trust of anyone who can run the binary as
that user, agents included; `TestLocalScopesNeverHoldAdminOrAct` holds it to that. Pairing (grants,
`cst_` device tokens, renewal, device listing and revocation) is removed: identity keys replace it.
The old tables survive only as `legacy_devices` and `legacy_grants` history.

## Removed until their slice

The old transports (`internal/api`, `internal/cli`, `internal/client`, `internal/mcp`) were not
reachable after S0 (the old `serve` is gone) and depended on the pairing code, so S2 deletes them;
S3 re-ports the HTTP API and the browser UI, S4 the CLI, the remote client and the MCP server, from
the reference clone, with their tests.
