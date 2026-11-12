# Callers: who reaches Caster, and what changes for each

One row per caller of the running service or of the shared store. "Host presented" is the `Host` header the
service sees; every non-loopback name must be in `caster.allowed_hosts`, and its proxy must send exactly one
`X-Forwarded-Proto: https` from a loopback peer (docs/architecture.md, HTTP). Real names are in the owner's note,
not in this public repository: `<public-host>` is the public reverse-proxy name, `<tailnet-host>` the Tailscale
name served on 8443.

| Caller | How it reaches Caster | Credential before | Credential after | Host presented | What changes |
|---|---|---|---|---|---|
| Browser, anywhere | `https://<public-host>` (reverse proxy to `127.0.0.1:8991`) | paired device token (`cst_`) in `sessionStorage` | owner sign-in (`POST /api/login`, session cookie) | `<public-host>` | pairing dialog replaced by a username and password form; `/v1` calls became `/api`. The page is the same small hand-written one. |
| Browser on the tailnet | `https://<tailnet-host>:8443` (Tailscale Serve to `127.0.0.1:8991`) | same | same | `<tailnet-host>` | add the name to `caster.allowed_hosts` |
| CLI on the home node (`put`, `get`, `ls`, `search`, ...) | the local store directly (no network) | the local operator with every scope | `local:operator`: read, resolve, write, observe | none | `act`, `shadow report` and `shadow label` exit 3 unless run as a client with a key that holds `caster.act` / `caster.admin`; `CASTER_DB_PATH` is read as the old database file's location |
| CLI on other machines (the paired devices) | `caster login <url> <code>`, then every command is an HTTP call | `cst_` device token, 30 days | identity API key stored under a secrets reference; profile in `remote.json` | the URL's host | each machine runs `caster login` again with a pairing code minted by an administrator (`POST /api/identity/pairing-codes`, HTTP only) or gets a key file from an offline `identity key create --out`; the old tokens stop working |
| Conduit (`caster put -m MIME -s SRC -t TAGS -` and `caster get REF`) | subprocess, the local store | the local operator | `local:operator` (read, resolve, write, observe) | none | nothing, if the new `caster` is the one on its `PATH`: argv, stdout (the ref only) and exit codes (2 not found) are pinned by `TestConduitContract`. Service and CLI share one SQLite file safely (`TestServiceAndCLIAreTwoProcessesOnOneStore`) |
| Tether hub gateway: the `caster` MCP child | `caster mcp` as a client of the service | a paired device token (read, write, resolve) in the catalog entry | an identity key, scopes chosen by the owner (cutover plan, step 6) | `127.0.0.1:8991` (loopback) | catalog edit below; the child reaches the service on loopback instead of the tailnet name, so it no longer depends on Tailscale Serve or the proxy |
| Agents on the mesh, through the hub | the child above | the hub's device | the hub's key | as above | the tools `caster_act`, `caster_observe`, `caster_shadow_report`, `caster_shadow_label` are refused by scope unless the owner grants more than read, write, resolve |
| Ops Chat shadow feed (`apps/ops-chat`, off unless `VITE_CASTER_SHADOW=1`) | its dev server calls `POST /api/observe` | a paired token with `caster.observe` only (none is in the database that was copied: check where it comes from) | an identity key with `caster.observe` only, held in that server's environment | as configured there | new key; route `/v1/observe` became `/api/observe`; that app is not changed by this port |
| Cerberus (`caster-uat`) | builds and supervises the unit; health check | `-addr` flag; URL `/health` | `serve` without a flag; URL `/healthz` | `<public-host>` for the URL | the descriptor edit in the cutover plan, step 9; no apply before it |
| `fleet-install` | `deploy.yaml`: binary, unit drop-in, backup preflight | n/a | n/a | n/a | unchanged; the drop-in carries `SUBSTRATE_CONFIG` |
| Outbound: Torque, Tesseract | Caster calls them for `torque://` and `tesseract://` refs | `CASTER_TORQUE_URL`, `CASTER_TESSERACT_*`, the Tether catalog entry `tesseract` | `caster.torque_url`, `caster.tesseract_mcp`, `caster.tesseract_bin` in the config file | n/a | env to config keys; read as the home node, so `caster.resolve` stays a deliberate grant |

## Catalog edit for the hub child (plan text, not applied)

The entry `mcp-servers/caster.yaml` in the Tether catalog (the repository's `deploy/tether/mcp-server.caster.yaml`
is the shape):

```yaml
id: caster
transport: stdio
command: <path of the new caster binary>
args: [mcp]
env:
  CASTER_REMOTE: http://127.0.0.1:8991          # was the tailnet https URL; plain http to loopback is accepted
  CASTER_TOKEN_REF: file://<absolute path of the hub key file>   # preferred; CASTER_TOKEN only if the gateway can pass nothing else
```

The key file is `0600` and outside every repository. The client environment variables (`CASTER_REMOTE`,
`CASTER_TOKEN_REF`, `CASTER_TOKEN`, `CASTER_ALLOW_HTTP`, `CASTER_LOCAL`) are the one place Caster reads settings
from the environment: they configure a client process, not the service.

## Proxies (plan text, the owner's edits)

- Reverse proxy: unchanged upstream (`127.0.0.1:8991`); it must pass the original `Host` and send
  `X-Forwarded-Proto: https`. Health checks go to `/healthz`.
- Tailscale Serve: unchanged (`tailscale serve --https=8443 http://127.0.0.1:8991`, see `deploy/tailscale-serve.sh`).
  Check after the start that the tailnet name answers `/healthz` (a `403 Invalid host` means the name is not in
  `caster.allowed_hosts` or no single `X-Forwarded-Proto: https` arrives).
- Both reach the service as a loopback peer, which is the only peer whose forwarded name is believed.
