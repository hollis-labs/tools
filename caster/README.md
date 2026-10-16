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

**Feeding it from a chat UI.** Ops Chat (`apps/ops-chat`) sends finished messages through an observe-only dev-server endpoint that keeps the bearer token in its Node process (see its README, `VITE_CASTER_SHADOW=1`). Any other browser client should do the same: the token must not reach the browser, and a generic pass-through proxy would let any web page drive the whole API.

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
TOKEN=$(cat "$(caster token)")                       # 0600 file, created on first use
curl -H "Authorization: Bearer $TOKEN" 'localhost:8991/v1/get?ref=CW-20261016-0015'
curl -H "Authorization: Bearer $TOKEN" -d '{"name":"api/x","content":"hi"}' localhost:8991/v1/put
```

Routes: `GET /v1/head|get|list|log|search`, `POST /v1/put|snap|scan|expand` (JSON bodies are the service request structs), `GET /health` (open). Auth is `libs/security/auth`: bearer API key (hash of the token file), scopes `caster.read` and `caster.write`, mode `enforce` by default (`HOLLIS_AUTH_CASTER` / `HOLLIS_AUTH` override; `ValidateBind` refuses `off`/`permissive` on non-local listeners), audit events as JSON on stderr. Unmapped routes fail closed. A missing ref is HTTP 404 with a Meta body; a bad ref is 400; an unreachable backend is 502.

## Architecture

`internal/service` is the contract, organised as domain services on `Service` (`Notes`, `Refs`, `Actions`, `Shadow`): every operation is `func(ctx, Request) (Result, error)` with JSON-tagged structs, errors classified by `service.Kind`, and the operation→scope table (`ScopeFor`). `internal/cli`, `internal/mcp` and `internal/api` only decode, call the service and encode; `.golangci.transport.yml` enforces that they never touch the store. A new transport (gRPC, a Chimera plugin, Tether tool) is a new package of that shape.

## Storage

go-apppaths layout (`caster paths`): blobs under `blobs/sha256/ab/cdef…` (write-once, mode 0400, digest verified on write and on read), SQLite index (`main.db`) for blob metadata, name revisions and FTS5. Dedup is by hash; a name is a pointer with revision history, never mutable bytes.

## Known limits (spike)

- Tesseract via the gateway needs the hub reachable; unreachable reports an error (exit 1), not not-found. `@rev` is unsupported (use `revision/<id>`). `materialize` calls `tesseract_get`, which reinforces the item's activation, as any read does.
- Workspace items have no revision id, so a materialised workspace item reports an empty revision.
- Actions: one so far (`save-handoff`). Binding context comes from flags/env/args; nothing reads it from a Tether session yet. Promotion of drafts stays a separate, reviewed step.
- No GC/pin/quota, no auth or namespace permissions (single local user), no remote put, no HTTP/MCP transport yet. Whole-file CAS, no chunking.
- Torque and Tesseract content is rendered as markdown by caster; the brief's `summary`/`preview` modes are not implemented.
