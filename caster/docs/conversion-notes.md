# Conversion notes

Old-to-new map and gaps for the Substrate port. Filled in per slice; the old-to-new tables for HTTP,
CLI and MCP arrive with the slices that change them.

## Dropped

| What | Why |
|---|---|
| OpenTelemetry (`CASTER_OTEL_ENABLED`, `CASTER_ENV`, go-otel) | Opt-in, off on the live instance. Not part of the framework shell; follow-up if wanted. |

| Pairing: `caster pair`, `login`, `renew`, `revoke`, `devices`, `POST /v1/pair/exchange`, `/v1/renew`, `cst_` tokens | Replaced by the identity module's API keys (S3/S4); the old rows are kept as history. |
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
