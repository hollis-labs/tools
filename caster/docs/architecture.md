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
  `config.go` (data paths), `guard.go` (the legacy guard), `serve.go`, `client.go` (data commands, login,
  remote), `mcp.go`.
- `internal/api`: the HTTP transport (S3), `internal/webui`: the embedded browser UI (S3), `internal/cli`,
  `internal/client`, `internal/mcptools` (S4).
- `internal/settings`: the `caster:` config block. `internal/hostguard`: Host allow-list.
- `internal/storage`: the database schema (one embedded goose migration), the blob tree and every
  query; `storagetest.Open(t)` gives the tests a throwaway one. It replaces the old `internal/store`.
- `internal/service`: the service layer (S2): notes, refs, actions, shadow mode, whoami. It is the
  one place business rules and authorization live; the transports (HTTP, CLI, MCP)
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

## HTTP, identity and the UI (S3)

- Routes live under `/api/` and are listed once in `internal/api.Routes()`, which builds the guard's scope
  table; `TestEveryRouteHasItsScope` fails for a route outside `/api/` or without a table entry, and
  `TestRouteScopesMatchTheDocumentedTable` pins each scope. A request with no table entry is refused by
  the guard in `enforce`.

  | Route | Table scope | What the service then checks |
  |---|---|---|
  | `GET /api/list`, `/api/log`, `/api/search` | `caster.read` | read |
  | `POST /api/put` | `caster.write` | write |
  | `POST /api/act` | `caster.act` | act |
  | `POST /api/observe` | `caster.observe` | observe |
  | `GET /api/report`, `POST /api/label` | `caster.admin` | admin |
  | `GET /api/get`, `/api/head`, `/api/actions`, `POST /api/snap`, `/api/scan`, `/api/expand`, `GET /api/whoami` | authentication only | `caster.read` for `cas://` and `caster://`, `caster.resolve` for `torque://` and `tesseract://` |

  The ref routes ask the guard only for a verified caller because the scope depends on the ref's scheme,
  which only the service can read. Authentication only is the empty scope in the table.
- Identity is the module's: sign-in `POST /api/login` (session cookie, `X-Requested-With` and a same-origin
  `Origin` on cookie writes), `POST /api/logout`, `/api/identity/*`, `/api/token`, `/api/pair`. The owner
  and machine keys are created offline: `caster identity setup --username NAME` (password on stdin) and
  `caster identity key create --name N --scope S,... --out FILE`. `GET /api/setup` reports
  `setup_required`. There is no open setup form.
- Errors are the framework's flat `{code, message}`. A ref that is not resolved is an outcome and keeps its
  body: 404 `not_found` or 403 `denied` with the `Meta` object. A malformed full-text query is 400 `invalid`.
- Every route is `public: false`: there is no open route and no open mount. `/healthz` and the UI shell are
  the framework's and the page's.
- The browser UI is the old static page, ported: three files and a favicon under `internal/webui/ui`, no build
  step, served as the host's assets. It signs in with a username and password (`/api/login`) instead of
  pasting a pairing code, and sends `X-Requested-With` on every call. A CSP and `nosniff` ride on every
  response outside `/api/` and `/healthz`. The app-shell rebuild is a later task for the GUI track.
- Host check (`internal/hostguard`): loopback names plus `caster.allowed_hosts`; a listed name is believed
  only from a loopback peer carrying exactly one `X-Forwarded-Proto: https`.

## CLI, MCP and the client (S4)

- Which command opens what is decided before boot, in `cmd/caster/main.go` `plan`:

  | Command | Modules | May create |
  |---|---|---|
  | `serve` (exactly that) | all | database, `cas/`, signing key (only when the database holds no identity rows) |
  | `identity setup`, `identity key create` | config, logging, secrets, auth, database, identity, cli | the same |
  | `put`, `get`, `ls`, ... (the data commands) and `mcp`, on the local store | config, logging, secrets, database, (mcp), cli | `caster.db`, `cas/`; never the signing key (no identity module) |
  | the same as a client of a home node | config, logging, (mcp), cli | nothing |
  | `login`, `logout`, `remote`, `paths` | config, logging, health, cli | `remote.json` and the stored key (`login` only) |
  | bare `caster`, help, version, config, health, completion, unknown, bad flag | config, logging, health, cli | nothing |

  Every one of them runs the legacy guard before the database opens: a directory that has only `main.db`
  is refused, naming `caster migrate`.
- Two ways to reach the data, chosen per process (`client.Active`): a client of a home node (`CASTER_REMOTE`
  and a key, or the profile `caster login` saved; every operation is an HTTP call that carries the key, so the
  service decides) or the local store. In the local store the caller is `service.LocalPrincipal()`
  (`local:operator`, `caster.read/resolve/write/observe`): it can never act or read the shadow report.
- `internal/cli` keeps the old argv, stdout and exit codes (Conduit uses `put` and `get`). `internal/client`
  is the HTTP client behind the `service` interfaces. `internal/mcptools` is the tool set, registered on the
  mcp module's server and served both over stdio and at `/mcp`; every tool has its scope in `ToolScopes`
  (the ref tools ask for authentication only and the service applies the scope of the ref's scheme).
- One service process and any number of CLI or stdio processes share one SQLite file (WAL, one write
  connection per process); the signing key is created only by `serve` or `identity setup` and only when the
  database has no identity rows. `TestServiceAndCLIAreTwoProcessesOnOneStore` runs both against one directory.
