# Conversion notes

Old-to-new map and gaps for the Substrate port. Filled in per slice; the old-to-new tables for HTTP,
CLI and MCP arrive with the slices that change them.

## Dropped

| What | Why |
|---|---|
| OpenTelemetry (`CASTER_OTEL_ENABLED`, `CASTER_ENV`, go-otel) | Opt-in, off on the live instance. Not part of the framework shell; follow-up if wanted. |
| Pairing: `caster pair`, `renew`, `revoke`, `devices`, `token`, `POST /v1/pair/exchange`, `/v1/renew`, `cst_` tokens, the operator token file | Replaced by the identity module's API keys and pairing codes; the old rows are kept as history (`legacy_devices`, `legacy_grants`). |
| The local operator with every scope (`*`) | `service.LocalScopes`: read, resolve, write, observe. admin and act need a key. |

## HTTP: old to new

| Old | New |
|---|---|
| `GET /health` | `GET /healthz` (framework) |
| `GET /v1/{head,get,list,log,search,actions,report,whoami}` | `GET /api/...` same names, same query parameters |
| `POST /v1/{put,snap,scan,expand,act,observe,label}` | `POST /api/...` same bodies |
| `POST /v1/pair/exchange`, `POST /v1/renew`, `GET /v1/devices`, `POST /v1/devices/revoke` | removed: identity keys (`caster identity key create`, `POST /api/identity/keys`) |
| bearer `cst_` device token, operator token file, optional Tether JWT (`CASTER_TETHER_URL`) | identity API key or session cookie; a Tether-issued JWT is `auth.jwks_url` + `auth.audience: caster` in the config file (off by default, as before) |
| error body `{"error":{"code","message"}}` | flat `{"code","message"}`; a missing or denied ref is still a 404/403 outcome with its `Meta` body |
| browser: paste a pairing code | sign in with a username and password |
| unknown paths under `/` | the UI page for a path outside `/api` and `/healthz` (the host's single-page fallback); `/v1`, `/v1/...`, `/api` and an `/api/` path with no route are 404 `{"code":"not_found"}`, never the page (`api.NotFound`, tested on the real binary) |

## CLI and MCP: old to new

| Old | New |
|---|---|
| `caster put/get/resolve/head/snap/ls/log/search/scan/expand/actions/act/shadow/whoami` | same flags, same stdout, same exit codes (0 ok, 2 not found, 3 denied, 1 error, 64 bad input); `internal/cli` is the old implementation with the pairing commands removed, `TestConduitContract` pins `put`/`get` for Conduit |
| bare `caster` served | bare `caster` prints help; only `caster serve` serves (`serve -addr` is `httphost.addr`) |
| the CLI and `caster mcp` as the local operator with every scope | the local principal `local:operator` holds `caster.read, resolve, write, observe`; `act`, `shadow report` and `shadow label` answer exit 3 / a `denied` tool error. Run them as a client of the service with a key that holds `caster.act` / `caster.admin` |
| `caster pair`, `devices`, `revoke`, `renew`, `token` | removed (see Dropped) |
| `caster login <url> <code>` exchanged a Caster code for a `cst_` token | redeems an identity pairing code (`POST /api/pair`) for an API key and stores it under a secrets reference, plus `remote.json`; the key is never printed |
| `caster logout`, `caster remote`, `caster paths` | same; `remote` shows the key's scopes, not an expiry (identity keys have none by default) |
| `caster mcp`: hand-written JSON-RPC, 15 tools | the framework's stdio server (`mcp.ServeStdio`) with the same tool names and arguments; also served at `/mcp` on the service for a key with `caster.mcp`. Errors are tool errors with `{"code","message"}`; a missing ref is still an outcome |
| `CASTER_REMOTE`, `CASTER_TOKEN_REF`, `CASTER_TOKEN`, `CASTER_ALLOW_HTTP`, `CASTER_LOCAL` | unchanged, and still read from the environment, the one allowed exception to "no `os.Getenv` for settings" (driver ruling): they configure a client process, not the service, and the gateway hands a child its settings that way (the live Tether catalog entry and Conduit's test pass them). Prefer `CASTER_TOKEN_REF` everywhere; `CASTER_TOKEN` is for the gateway child only. A `key_...` literal replaces `cst_...` in `CASTER_TOKEN` / `CASTER_TOKEN_REF`. Gap 3 |
| `CASTER_NAMESPACE`, `CASTER_SESSION_ID`, `CASTER_AGENT_ID`, `CASTER_WORKSTREAM_ID` (defaults for `caster act`) | unchanged |
| `CASTER_DB_PATH` named the database file | the rule, exactly: the value is read as the location of the old database file; its **parent directory** is the store directory, and `caster.db` and `cas/` go there. The file name is ignored, so `<dir>/caster.db` (Conduit's test sets that), `<dir>/main.db` and `<dir>/anything` all mean `<dir>`; a bare directory name `<dir>` means its parent. Pinned by `TestCasterDBPathRule`. `caster migrate` reads the old files from the same directory unless `--from` says otherwise |

A data command or `caster mcp` on the local store may create `caster.db` and `cas/` (and refuses a directory that
holds only `main.db`, naming `caster migrate`). It starts no identity module, so it never creates the signing
key. As a client of a home node (`CASTER_REMOTE` or a saved profile) it opens no file at all.

## Environment and paths (so far)

| Old | New |
|---|---|
| `CASTER_TORQUE_URL` | `caster.torque_url` |
| `CASTER_TESSERACT_MCP` | `caster.tesseract_mcp` |
| `CASTER_TESSERACT_BIN` | `caster.tesseract_bin` |
| `HOLLIS_AUTH`, `HOLLIS_AUTH_CASTER` | `auth.mode` (default `enforce`) |
| `-addr` flag | `httphost.addr` |
| `CASTER_DB_PATH`, `CASTER_WORKSPACE` | unchanged: the data-directory override (apppaths) |
| `workspaces/default/main.db` | `workspaces/default/caster.db` beside it |
| `workspaces/default/blobs/` | `workspaces/default/cas/` |

## Gaps

1. Docs, not code: `docs/adopting-substrate.md` section 5.3 says to use `authenticated` for any verified caller when
   registering routes on `auth.ScopeTable(app).Route(...)`. For a route registered that way the word is a
   literal scope that only a `*` holder has: an API key with `caster.read,caster.write` got 403
   `required scope: authenticated` (found by the real-binary test). Authentication only is the empty
   scope there; `authenticated` is the `ui.json` word that `module.AddRoutes` translates to `""`. Caster uses `""`
   (`api.ScopeAuthenticated`) and tests it with a scoped key. The guide should say so.
2. Pairing. Caster's own pairing is removed. The identity module covers machine callers (an offline `identity key
   create --out FILE`, or an administrator's `POST /api/identity/keys`) and app-to-app redemption
   (`POST /api/identity/pairing-codes`, then `POST /api/pair`, which returns a key). What it does not give:
   a pairing code can be minted only over HTTP by an administrator (there is no offline command for it),
   where Caster's rule was that grants are created at the home node's terminal and never over HTTP.
   Decision for the client slice (S4): `caster login` redeems an identity pairing code. Observation for the framework: `internal/hostguard` is an app-local copy of Loom's package.
3. Framework rule vs. client selection. The guide says not to read `os.Getenv` for settings. `CASTER_REMOTE`,
   `CASTER_TOKEN_REF`, `CASTER_TOKEN`, `CASTER_ALLOW_HTTP` and `CASTER_LOCAL` stay as environment variables because
   they pick the *client's* connection (a CLI or stdio child), the Tether gateway passes a child's secrets through
   its `env`, and Conduit's test sets `CASTER_LOCAL`. If the driver wants them under `caster:` in the config file
   instead, that is a small change plus a catalog edit at cutover.
4. The hub's `caster` child. Today it is a remote client with a device token. After the port it is `caster mcp`
   as a client of the service with an identity API key (`CASTER_REMOTE`, `CASTER_TOKEN_REF`), unchanged in shape.
   Both run on the same host, so `CASTER_REMOTE` should be the loopback address (`http://127.0.0.1:8991`), not the
   tailnet name: plain http to loopback is allowed by the client, the `Host` is a loopback name the Host check
   always serves (no `allowed_hosts` entry), and the call no longer depends on Tailscale Serve or the proxy.
   The cutover plan (S6) carries the catalog change as a plan, not applied.
