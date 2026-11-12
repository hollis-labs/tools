# Conversion notes: Conduit onto Substrate

Status: in progress (branch `feat/conduit-substrate`). Pinned to Substrate `da9f59b`, BOM security/auth v0.3.1, Go 1.26.11.

## Gaps (each stops the work until the driver rules)

1. **`libs/core/panics` is not in the BOM.** `substrate pins sync --app .` fails: `github.com/hollis-labs/libs/core/panics is a libs module the bom does not pin (add it to app_libs or drop the import)`. Conduit uses `panics` (recovered-panic refs, `Capture`, `Call`, `Clean`) and `panics/httpguard` across the service, CLI, API and MCP transports. Proposed: a one-line Substrate PR adding `github.com/hollis-labs/libs/core/panics: v0.1.0` to `app_libs`. Not worked around.
2. **Telemetry (`go-otel`, opt-in by `CONDUIT_OTEL_ENABLED`) has no Substrate counterpart.** It was off in the live unit (no such variable set). Dropped from the new build pending a ruling.
3. **Local CLI `approve`.** `conduit approve` runs as whoever can run the binary and open the data file, which on a host where agents run includes agents. The old build had the same property. Needs a ruling: keep (documented local trust) or move approval to a signed-in person only.

## Old to new

| Old | New |
|---|---|
| data dir `<data>/workspaces/default/main.db` | `<data>/conduit.db` (one goose migration); old layout refused by a guard |
| `CONDUIT_BLOBS` | `conduit.blobs` |
| `CONDUIT_POLICY` | `conduit.policy_file` |
| `CONDUIT_RUNBOOKS` | `conduit.runbooks_file` |
| `-allow-dir` | `conduit.allow_dirs` |
| `-addr` | `httphost.addr` |
| `conduit token ...` | removed: identity API keys (`conduit identity key create`) |
| `CONDUIT_OTEL_ENABLED`, `CONDUIT_ENV` | dropped (Gap 2) |
| data directory override | `CONDUIT_DATA_DIR` (the only environment read) |

## Command plan

Help, version, config, health, an unknown word and `--help` at any level start no module that opens a file and create nothing. `check`, `parse`, `runbooks`, `init` and `paths` build the service without a database. `run`, `cancel` and `reap` may create the database (never beside only the old one); every other command needs an existing new-layout database.
