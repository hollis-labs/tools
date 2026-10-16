# caster

Local content-addressed store and ref resolver for scratchpads, drafts and notes (mostly text/markdown), that can also pull Torque tasks and Tesseract records by id or key. Spike; design notes in `infra/compute-hosting/reference-store-brief.md`.

## Use

```sh
echo "draft" | caster put -n spike/notes -t "draft"   # -> caster://spike/notes@1
caster get spike/notes            # floating: latest;  caster get spike/notes@1 is pinned
caster log spike/notes            # revision history
caster ls [-ns spike] [-t draft]
caster search walrus              # FTS over stored text
caster get CW-20261016-0015       # Torque task as markdown   (torque://task/<id>)
caster get tesseract://adr/ADR-022
caster snap -n snaps/t15 CW-20261016-0015   # pin any ref's bytes into CAS, with provenance
caster scan  < message.txt        # which refs in this text actually resolve
caster expand < message.txt       # inline their contents (labelled <caster-ref … trust="untrusted">)
```

Exit codes follow the secrets helper protocol: `0` ok, `2` not found, `3` denied, `1` error. `caster get <ref>` is therefore usable as a `helper`-style resolver.

## Refs

| Ref | Meaning |
|---|---|
| `cas://sha256/<hex>` | immutable blob, always pinned |
| `caster://<ns>/<name>[@rev]` | named pointer; floating, or pinned with `@rev`. Bare `name` uses ns `scratch` |
| `torque://task/CW-YYYYMMDD-NNNN` | Torque task, via `CASTER_TORQUE_URL` (default `http://127.0.0.1:8990`) |
| `tesseract://item/<id>` | current Tesseract item (floating); metadata via `tesseract_ref_resolve` (no read, no reinforcement), content via `tesseract_get` |
| `tesseract://revision/<id>` | exact immutable revision (pinned) |
| `tesseract://<domain>/<namespace>/<key>` | `memory`, `knowledge`, `event`, `workspace` or `context` by key; the key is the last segment |
| `tesseract://<ns>/<key>` | legacy: local `tesseract` CLI context store (`CASTER_TESSERACT_BIN`) |

Bare `ns/name`, a task id or a 64-hex hash is accepted wherever a ref is.

## Tesseract gateway

MCP-store refs go through a stdio MCP client (`internal/mcpclient`) that launches the server from your Tether catalog entry `~/.tether/catalog/mcp-servers/tesseract.yaml` (today: ssh to the hub), so caster uses the access you already have and holds no credentials. Override with `CASTER_TESSERACT_MCP="tesseract mcp"`. Env values that are secret references are not passed. One server process serves a whole invocation (~1 s ssh start).

## For agents: `caster mcp`

Stdio MCP server with `caster_put`, `caster_get`, `caster_head`, `caster_snap`, `caster_ls`, `caster_log`, `caster_search`, `caster_scan`, `caster_expand`. Register it as `{"command":"caster","args":["mcp"]}`. Missing refs are outcomes (`state: not_found`), not errors; `caster_get` truncates at 64 KiB by default. The stdio server is launched by the operator and is not authenticated (same trust as the CLI).

## Detection in shadow mode: `caster shadow`

Measures how noisy detection is before any card is shown. A UI or ingester hands each message to `observe`; caster runs every extractor (including the low-confidence ones: bare ULIDs → `tesseract://item/…`, bare 64-hex → `cas://…`), validates each candidate with its resolver, applies the card/quiet policy as a **dry run**, and records the outcome. It returns counts only, so nothing can leak into the UI, and it never stores message text (only ref, extractor, state, verdict).

```sh
caster shadow observe -source chat:<session>/<msg> < message.txt   # shadow: 6 candidates, 3 resolved, 2 would show
caster shadow report [-since 24h] [-json]    # per extractor: candidates, resolve rate, would-show, distinct refs, precision
caster shadow label <ref> true|false         # human verdict on a detected ref -> precision
```

Verdicts: `card` (resolved, high confidence), `quiet_index` (resolved, low confidence: never shown), `ignore` (did not resolve), `error` (resolver could not answer), `skipped` (over the 50-resolves-per-text budget). Identical text at the same extractor generation (`ref.Generation`, bump it when an extractor changes) is recorded once; later sightings count as repeats. HTTP: `POST /v1/observe`, `GET /v1/report?since=24h`, `POST /v1/label`; MCP: `caster_observe`, `caster_shadow_report`, `caster_shadow_label`.

**Feeding it from a chat UI.** Ops Chat (`apps/ops-chat`) sends finished messages through an observe-only dev-server endpoint that keeps a paired `caster.observe`-only token in its Node process (see its README, `VITE_CASTER_SHADOW=1`). Any other browser client should do the same: the token must not reach the browser, and a generic pass-through proxy would let any web page drive the whole API.

Known false-positive classes (from the first run): a task id inside a URL path matches `torque-id`. Not extracted yet: git SHAs, `org/repo#N`, bare file paths.

## Actions: `caster actions`, `caster act`

An action is a registered operation on a ref whose parameters bind from ambient context instead of being typed. One exists so far:

```sh
export CASTER_NAMESPACE=project/<id>/workspace/handoffs CASTER_SESSION_ID=<session> CASTER_AGENT_ID=<agent>
caster actions caster://handoffs/today          # what applies, and what context it needs
caster act save-handoff caster://handoffs/today -dry-run
caster act save-handoff caster://handoffs/today  # -> tesseract://item/<id>
```

`save-handoff` pins the source in CAS, then creates a Tesseract **workspace draft** (replaceable, promoted only after review) with provenance (`source_ref`, `snapshot`, `sha256`) in the item's `data`. It is a remote write, reported as such. The idempotency key is content hash + session, so a retry or double click replays the original item instead of creating another; the first run is recorded in the local `action_log`. Namespaces must look like `{scope}/{id}/workspace/{purpose}` and may not be in `user/`. A source that does not resolve causes no write. Over MCP the same thing is `caster_actions` / `caster_act` (with `dry_run`); over HTTP `GET /v1/actions`, `POST /v1/act` (scope `caster.write`).

## HTTP API: `caster serve`

```sh
caster serve [-addr 127.0.0.1:8991]
TOKEN=$(cat "$(caster token)")                       # the local operator's key (all scopes); other clients pair, see below
curl -H "Authorization: Bearer $TOKEN" 'localhost:8991/v1/get?ref=CW-20261016-0015'
curl -H "Authorization: Bearer $TOKEN" -d '{"name":"api/x","content":"hi"}' localhost:8991/v1/put
```

Routes: `GET /v1/head|get|list|log|search|actions|devices|report`, `POST /v1/put|snap|scan|expand|act|observe|label|renew|devices/revoke|pair/exchange`, `GET /health` (JSON bodies are the service request structs). A missing ref is HTTP 404 with a Meta body; a bad ref is 400; no identity is 401; a missing scope is 403; an unreachable backend is 502. Serve on loopback and reach it over an SSH forward or Tailscale Serve (the ADR 0062 pattern); `-addr` on another interface is allowed only in enforce mode.

## Using a home node from other machines

One machine is the **home node**: it owns the store, resolves Torque and Tesseract, and runs `caster serve` (bound to loopback). Every other machine, human or agent, is a client of it: the CLI and `caster mcp` call the home node's HTTP API through the same service contract, with no local store.

```sh
# on the home node, once per client
caster serve                                  # loopback; reach it over an SSH forward or `tailscale serve --bg 8991` (https)
caster pair -scope read,resolve,write -label work-laptop     # prints a one-use code (5 min)

# on the client (the URL is what the client can reach: https://<name>.<tailnet>.ts.net, or http://localhost:8991 through `ssh -L 8991:localhost:8991 <home>`)
caster login https://home-node.<tailnet>.ts.net <code>
caster whoami                                 # who the home node takes you for, your scopes, expiry
caster put -n notes/today < today.md          # now stored on the home node
caster remote                                 # mode, reachability, scopes, days left;   caster renew   extends 30 days
```

`login` redeems the code and stores the device token with the **secrets lib** (OS keychain, else its managed 0600 file store on headless Linux; `-ref` picks another reference such as `op://…`). The saved profile (`~/.config/caster/remote.json`) holds only the URL and that reference, never the token. After login every command on that machine uses the home node, including `caster mcp`, so an agent platform only needs `{"command": "caster", "args": ["mcp"]}` (give the absolute path where `PATH` is minimal). `CASTER_LOCAL=1` forces a local store (use it on the home node itself); `CASTER_REMOTE` + `CASTER_TOKEN_REF` configure a machine without a profile. `caster logout` forgets the home node locally; end the device itself with `caster revoke <dev_…>` there.

The client refuses plain http except to loopback or a tailnet address (`-allow-http` / `CASTER_ALLOW_HTTP=1` to accept it), never follows redirects, and says what to do when its token is expired or revoked. Commands that only make sense on the home node (`serve`, `token`, `pair`, `paths`) say so instead of acting.

## Access and security

Authorization lives in the service layer, so the CLI, MCP and HTTP are held to the same rules by the same code; a transport only stamps who is calling. The CLI and stdio MCP stamp the local operator (the process owner, all scopes); HTTP stamps whoever its bearer token resolves to. No identity is refused.

| Scope | Allows |
|---|---|
| `caster.read` | `get`/`list`/`log`/`search`/`scan`/`expand`/`actions` over the local store (`cas://`, `caster://`) |
| `caster.resolve` | the same for refs backed by another system (`torque://`, `tesseract://`), read **as caster**. Separate so a read-only holder cannot read everything the home node can reach |
| `caster.write` | `put`, `snap` |
| `caster.act` | actions with effects on other systems (`save-handoff`) |
| `caster.observe` | the shadow-mode feed (records refs and verdicts, returns counts; it validates candidates, so it can learn whether an id exists) |
| `caster.admin` | shadow reports and labels, listing and revoking devices |

`scan`/`expand` treat a ref the caller lacks the scope for as unresolved, never as forbidden.

**Pairing** (the Tether Outpost shape): at the local terminal, `caster pair -scope read,resolve -label laptop` prints a one-use code (5 min by default, 1 h max). The client redeems it once (`caster login <url> <code>`, which calls `POST /v1/pair/exchange`) for a `cst_` device token (30 days; only the SHA-256 of codes and tokens is stored; every failure answers the same 401; the route is rate-limited and takes at most 4 KiB). Scopes can only be narrowed at exchange and renewal, `POST /v1/renew` extends a device, `caster devices` lists, `caster revoke <dev_…>` ends one on its next request. Pairing grants can only be created at the home node's own terminal, never over HTTP, so a leaked token cannot enroll anything. `caster pair -scope observe -token-file PATH` redeems locally and writes the token (0600), for a client on the same machine such as the Ops Chat feed.

**Identity sources** (`caster serve`, tried in order): device tokens; Tether-issued JWTs when `CASTER_TETHER_URL` is set (`aud=caster`, scopes `caster.<verb>`, verified offline against Tether's JWKS with go-auth; `CASTER_JWT_ISSUER` pins `iss`, `CASTER_JWT_BOOTSTRAP_KEY` is a PEM or JWKS file used before the first fetch); and the local operator's static key (the token file `caster token` prints, `caster.*`). Tether cannot yet mint `caster.*` scopes for paired devices or launch grants, which is why caster has its own pairing for now. Issued-token revocation relies on the token's short lifetime: Tether requires a credential to read its deny-list and the poller is not wired.

**Known limits.** One shared owner, no per-namespace permissions inside caster. `caster.resolve` reads Tesseract/Torque with the home node's access, so grant it deliberately. Same-uid processes can read the store and token files directly.

## Architecture

`internal/service` is the contract, organised as domain services on `Service` (`Notes`, `Refs`, `Actions`, `Shadow`): every operation is `func(ctx, Request) (Result, error)` with JSON-tagged structs, errors classified by `service.Kind`, and scope checks (`require`) inside each method. `internal/cli`, `internal/mcp` and `internal/api` only decode, call the service and encode; the domain services are interfaces (`service/contract.go`), implemented locally by the service and remotely by `internal/client`, so the CLI and MCP are identical in both modes; `.golangci.transport.yml` enforces that they never touch the store. A new transport (gRPC, a Chimera plugin, Tether tool) is a new package of that shape.

## Storage

go-apppaths layout (`caster paths`): blobs under `blobs/sha256/ab/cdef…` (write-once, mode 0400, digest verified on write and on read), SQLite index (`main.db`) for blob metadata, name revisions and FTS5. Dedup is by hash; a name is a pointer with revision history, never mutable bytes.

## Known limits (spike)

- Tesseract via the gateway needs the hub reachable; unreachable reports an error (exit 1), not not-found. `@rev` is unsupported (use `revision/<id>`). `materialize` calls `tesseract_get`, which reinforces the item's activation, as any read does.
- Workspace items have no revision id, so a materialised workspace item reports an empty revision.
- Actions: one so far (`save-handoff`). Binding context comes from flags/env/args; nothing reads it from a Tether session yet. Promotion of drafts stays a separate, reviewed step.
- No GC/pin/quota, no auth or namespace permissions (single local user), no remote put, no HTTP/MCP transport yet. Whole-file CAS, no chunking.
- Torque and Tesseract content is rendered as markdown by caster; the brief's `summary`/`preview` modes are not implemented.
