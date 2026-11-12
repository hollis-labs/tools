# Conversion notes: Conduit onto Substrate

Status: ready for review (branch `feat/conduit-substrate`). Pinned to Substrate `4f82c47f3f4d`, BOM security/auth v0.3.1, Go 1.26.11.
The cutover is written and rehearsed on a copy, not run: `docs/cutover-plan.md`. The shape of the port: `docs/architecture.md`.

## Gaps

Each of the first four is a framework piece Conduit needed and did not find. None is worked around. The fifth is a note.

1. **`libs/core/panics` was not in the BOM. Closed.** `substrate pins sync` refused the import until Substrate #31 added `github.com/hollis-labs/libs/core/panics` v0.1.0 to `app_libs`. Conduit is pinned to Substrate `4f82c47f3f4d`; `pins sync` reports it is in step and `pins check` passes (go-sandbox v0.6.0 raised nothing; go-apppaths is not used).
2. **Telemetry (`go-otel`, opt-in by `CONDUIT_OTEL_ENABLED`, tagged by `CONDUIT_ENV`) has no Substrate counterpart. Ruled: dropped.** It was off in the live unit. The two variables are no longer read.
3. **Local CLI `approve`. Ruled: approval is a signed-in person's act.** Removed with it: the terminal prompt that approved from `/dev/tty`, `-yes`, `-shadow` (record the verdict but do not require approval: an Ask ran without one; Deny still blocked), and `conduit reject` and `conduit token` as local commands. `conduit approve|reject --server URL --user NAME <run>` is a client of the running server: the password is read from the terminal only (refused when stdin is not a terminal, no flag, environment variable or file), the session is held in memory for one request and signed out again, nothing is stored. The client shows the whole command (control characters made visible, never truncated), the working directory, the effect and the submitter, and sends the approval only after `yes` is typed at the terminal; reject asks for nothing. A page for approvals is a later task.
4. **`hostguard` is a copy.** `/mcp` behind a proxy name is refused by the MCP library's localhost protection unless the proxy rewrites `Host`, and the framework has no per-app allowed-hosts check yet. `internal/hostguard` is Mentat's copy (and Loom's, and Tangent's) with its tests; `conduit.allowed_hosts` lists the names. It goes when the held framework change lands (ruled: copy, same as the other apps).
5. **Not a gap, a note (ruled): unknown `/api` paths and the placeholder page.** The auth guard in front of the routes answers 401 to a caller it does not know and 403 `route_not_mapped` to a verified caller on a path no scope is mapped for; neither serves a page or data, which was the point of the ruling. The route set's own JSON 404 shows only where no guard stands (the unit tests). The old `/v1/` prefix is an open mount that answers a JSON 404 to anyone, tested at the route set and against the real binary. `httphost` serves its "Web UI not built" placeholder for every other non-API path: Conduit has no UI, a mount that would shadow the SPA is refused by `httphost`, and there is no option to pass no assets, so it is left as is.

## Old to new

| Old | New |
|---|---|
| data dir `<data>/workspaces/default/main.db` | `<data>/conduit.db` (one goose migration); an old layout is refused by a guard, never opened |
| `CONDUIT_BLOBS` (`local` or `caster`) | `conduit.blobs`; the caster backend and its contract are unchanged: `caster put -m MIME -s SRC -t TAGS -` prints the ref, `caster get REF` exits 2 on not found; its test sets `CASTER_LOCAL`, `CASTER_DB_PATH`, `CASTER_WORKSPACE` and the `XDG_*` variables and skips when `caster` is not installed |
| `CONDUIT_POLICY` | `conduit.policy_file` |
| `CONDUIT_RUNBOOKS` | `conduit.runbooks_file` |
| `-allow-dir` (repeatable) | `conduit.allow_dirs` |
| `-addr`, `-unix`, `-unsafe-bind` | `httphost.addr`; `serve` refuses a non-loopback address; no unix socket |
| `conduit token create/list/revoke` | removed: identity API keys (`conduit identity key create --name N --scope S --out FILE`) |
| old sha256 token rows | copied to `retired_tokens` with a retirement time; **never honoured** (a retired hash as a bearer answers 401) |
| `CONDUIT_OTEL_ENABLED`, `CONDUIT_ENV` | dropped (Gap 2) |
| data directory override | `CONDUIT_DATA_DIR`, the only environment variable read |
| `conduit approve <run>` on a terminal, `-yes`, `-shadow`, `conduit reject` | removed; `conduit approve|reject --server URL --user NAME <run>` (Gap 3) |
| `/v1/...` | `/api/...` (the table below); `/v1/` answers a JSON 404 |

### Routes

| Old | New | Scope |
|---|---|---|
| `POST /v1/check` | `POST /api/check` | `conduit.read` |
| `POST /v1/exec` | `POST /api/exec` | `conduit.run` |
| `GET /v1/runs` | `GET /api/runs` | `conduit.read` (own) or `conduit.read.all` (every principal's) |
| `GET /v1/runs/{id}` | `GET /api/runs/{id}` | same |
| `GET /v1/runs/{id}/output` | `GET /api/runs/{id}/output` | same |
| `POST /v1/runs/{id}/cancel` | `POST /api/runs/{id}/cancel` | `conduit.run` (own) or `conduit.admin` |
| `GET /v1/approvals` | `GET /api/approvals` | `conduit.approve` |
| `POST /v1/runs/{id}/approve`, `/reject` | `POST /api/runs/{id}/approve`, `/reject` | `conduit.approve` **and** a signed-in person |
| `GET /v1/runbooks` | `GET /api/runbooks` | `conduit.read` |

### Scopes

| Old | New |
|---|---|
| `exec.read.own` | `conduit.read` |
| `exec.read` | `conduit.read.all` |
| `exec.run` | `conduit.run` (grant `conduit.read` as well: it is not implied) |
| `exec.approve` | `conduit.approve` (a person only: a key that holds it still cannot approve) |
| `exec.admin` | `conduit.admin` |
| the `:host` binding | gone: a key is not bound to a host |

## Behaviour that changed on purpose

- **Approving is a signed-in person's act, and nothing else's.** `service.AsPerson` is set only by the HTTP layer, only for a verified principal of kind `user` that arrived through a password session (a session id and no token id). A token a person minted from a session carries a token id and is refused (`TestUserTokenWithApproveScopeIsRefused`): an agent that can read the file holds it; `Approve` and `Reject` refuse every other caller (`ErrNotPerson`), and a principal cannot approve its own run. An API key that holds `conduit.approve` answers 403. The MCP tools have no approve or reject tool; the stdio process runs under `LocalTrust` with `LocalScopes` read and run only.
- **An Ask waits.** A run the policy answers Ask is recorded `pending` and waits for a person; `-shadow` and the terminal prompt are gone. Deny is still not overridable by anyone.
- **Migrated history is inert.** Rows copied from the old database carry `legacy=1`: they are listed and recalled, but never approved, started, cancelled, signalled or reaped, and a pending or running old row is not shown as live (`ErrLegacy`; `TestMigratedRunsAreNeverReapedStartedApprovedCancelledOrSignalled`, checked by mutation). A `pending` run that was waiting when the service stopped is history, not a request: it is resubmitted if it is still wanted.
- **The daemon starts approved runs** for principals prefixed `key:` or `user:` (the serving process); the stdio MCP process starts them for `local:operator`. Nothing else executes a run another transport approved.

## Command plan

Help, version, config, health, completion, an unknown word and `--help` at any level start no module that opens a file and create nothing. `check`, `parse`, `runbooks`, `init`, `paths`, `approve` and `reject` build the service without a database (`approve` and `reject` start no identity module either: they are HTTP clients). `run`, `cancel` and `reap` may create the database, never beside only the old one; `mcp` and every other command need an existing new-layout database. `serve`, `migrate-legacy`, `identity setup` and `identity key create` start the full module set and may create the data directory, the database and the signing key. A data directory that holds only `workspaces/default/main.db` is refused before the database module opens it (`TestOldDatabaseAloneIsRefusedAndNothingIsCreated`). Tests: `cmd/conduit/plan_test.go`.

## Data migration

`conduit migrate-legacy --from OLD.db [--blobs-from DIR] [--allow-missing-blobs]` copies every row of `runs`, `attempts`, `panics` and the old tokens (into `retired_tokens`) and every blob, in one write transaction, into an empty new database, then proves the copy: counts, every column of every row, foreign keys, and each blob by its sha256 (a blob name must be 64 lower-case hex). The old file is opened read-only and immutable and is refused when it has an unmerged `-wal`; it is never written (its sha256 is unchanged by a run), and a second run is refused because the target is no longer empty. Tested in `internal/legacy` and by a real-binary rehearsal on a copy of the live database (`docs/cutover-plan.md`, section 9).

## Not verified

- The deploy descriptor and the build workflow have not run on a runner or a host: they are modelled on Mentat's and Loom's and read against the shared workflow's inputs.
- `conduit approve` was exercised against the real binary through a pty, not from a human's terminal.
- macOS: Conduit's process identity and orphan reaping use `/proc`; the monorepo check excludes it, as before.
