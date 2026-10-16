# caster

A content-addressed store and ref resolver for scratchpads, drafts and notes (mostly markdown), that can also pull Torque tasks and Tesseract records by id or key. One store on a **home node**; the CLI on your laptop, a small browser UI, an MCP server for agents and an HTTP API all reach it through the same service layer.

Status: spike (pre-0.1, nothing tagged). Design brief: `infra/compute-hosting/reference-store-brief.md`. It is not a second knowledge store (Tesseract keeps durable knowledge; caster keeps bytes, names and provenance) and not a sync product.

## Install

```sh
make build          # bin/caster
make dist           # bin/dist/caster-{darwin,linux}-{arm64,amd64}, pure Go, for scp to other machines
caster paths        # where the store lives (go-apppaths layout)
```

Go 1.26; no cgo. On a machine that is not the home node, `caster login` (below) turns the same binary into a client.

## Quick start

```sh
echo "draft" | caster put -n spike/notes -t "draft"   # -> caster://spike/notes@1
caster put -n spike/notes notes.md                    # same name, new content: @2
caster get spike/notes            # floating: latest;  caster get spike/notes@1 is pinned
caster log spike/notes            # revision history
caster ls [-ns spike] [-t draft]
caster search walrus              # full-text over stored text
caster get CW-20261016-0015       # a Torque task as markdown   (torque://task/<id>)
caster get tesseract://adr/ADR-022
caster snap -n snaps/t15 CW-20261016-0015   # pin any ref's bytes into CAS, with provenance
caster scan  < message.txt        # which refs in this text actually resolve
caster expand < message.txt       # inline their contents (labeled untrusted)
```

Exit codes follow the secrets helper protocol: `0` ok, `2` not found, `3` denied, `1` error, so `caster get <ref>` works as a `helper`-style resolver.

## Refs

| Ref | Meaning |
|---|---|
| `cas://sha256/<hex>` | immutable blob, always pinned |
| `caster://<ns>/<name>[@rev]` | named pointer; floating, or pinned with `@rev`. A bare `name` uses ns `scratch` |
| `torque://task/CW-YYYYMMDD-NNNN` | Torque task, via `CASTER_TORQUE_URL` (default `http://127.0.0.1:8990`) |
| `tesseract://item/<id>` | current Tesseract item (floating); content via `tesseract_get` |
| `tesseract://revision/<id>` | exact immutable revision (pinned) |
| `tesseract://<domain>/<namespace>/<key>` | `memory`, `knowledge`, `event`, `workspace` or `context` by key |
| `tesseract://<ns>/<key>` | legacy: local `tesseract` CLI context store (`CASTER_TESSERACT_BIN`) |

Bare `ns/name`, a task id or a 64-hex hash is accepted wherever a ref is.

## Ways in

| | |
|---|---|
| **CLI** | the commands above, plus `shadow`, `actions`/`act`, `pair`, `devices`, `revoke`, `login`, `whoami`, `remote`, `renew`, `logout` |
| **Browser UI** | `caster serve` serves it at `/`: list, search, rendered markdown, edit, history. Pair a tab with `caster pair -link <url>`. [docs/browser-ui.md](docs/browser-ui.md) |
| **MCP** | `caster mcp`: `caster_put`, `caster_get`, `caster_head`, `caster_snap`, `caster_ls`, `caster_log`, `caster_search`, `caster_scan`, `caster_expand`, actions and shadow tools. [docs/mcp.md](docs/mcp.md) |
| **HTTP** | `caster serve`, JSON under `/v1`, bearer-token auth. [docs/http-api.md](docs/http-api.md) |

All four are thin: they decode a request, call the service layer and encode the result. Authorization is decided in the service, so they cannot disagree.

## Using it from other machines

One machine is the home node: it owns the store, resolves Torque and Tesseract, and runs `caster serve` on loopback, behind tailnet HTTPS (Tailscale Serve). Everything else is a client.

```sh
# on the home node
caster pair -scope read,write,resolve -label laptop        # one-use code, 5 minutes

# on the client
caster login https://<home>.<tailnet>.ts.net:8443 <code>   # token goes to the OS keychain
caster put -n notes/today < today.md                       # now stored on the home node
caster whoami                                              # who the home node takes you for, scopes, expiry
```

Local only: loopback, or your tailnet. Nothing here is meant for the public internet. [docs/home-node.md](docs/home-node.md) covers running the node (Cerberus), HTTPS, and client setup; [docs/access.md](docs/access.md) covers scopes, pairing and the identity sources.

## More

- [docs/shadow-mode.md](docs/shadow-mode.md): measuring ref-detection noise before showing any card in a chat UI.
- [docs/actions.md](docs/actions.md): registered operations on a ref, such as `save-handoff` to a Tesseract workspace draft.
- [docs/transport-boundary.md](docs/transport-boundary.md): the lint gate that keeps transports off the store.
- [CHANGELOG.md](CHANGELOG.md), [AGENTS.md](AGENTS.md) (invariants and the tests that guard them).

## Architecture

`internal/service` is the contract, organized as domain services on `Service` (`Notes`, `Refs`, `Actions`, `Shadow`, `Access`): every operation is `func(ctx, Request) (Result, error)` with JSON-tagged structs, errors classified by `service.Kind`, and scope checks inside each method. `internal/cli`, `internal/mcp` and `internal/api` only decode, call the service and encode. The domain services are interfaces (`service/contract.go`) implemented locally by the service and remotely by `internal/client`, so the CLI and MCP behave the same against a local store or a home node. A new transport (gRPC, a plugin, a Tether tool) is a new package of that shape.

## Storage

go-apppaths layout (`caster paths`): blobs under `blobs/sha256/ab/cdef…` (write-once, mode 0400, digest verified on write and read), a SQLite index (`main.db`) for blob metadata, name revisions, devices and FTS5. Dedup is by hash; a name is a pointer with revision history, never mutable bytes.

## Development

```sh
make test                                   # go test -race -count=1 ./...
go vet ./...
GOWORK=off golangci-lint run --config .golangci.transport.yml ./...   # transport boundary
```

CI (`.github/workflows/check.yml`) is the full gate.

## Known limits

- One shared owner. No per-namespace permissions inside caster, and `caster.resolve` reads Torque and Tesseract with the home node's own access, so grant it deliberately. Same-uid processes can read the store and token files directly.
- **No delete.** Names only gain revisions; blobs are never collected (no GC, pin or quota). Whole-file CAS, no chunking.
- Tesseract through the gateway needs the hub reachable; unreachable is an error, not not-found. `@rev` is unsupported for Tesseract (use `revision/<id>`).
- Browser device tokens live in `sessionStorage`: pair again per tab session.
- Tether cannot yet mint `caster.*` scopes for paired devices or launch grants (task `CW-20261016-0178`), so caster has its own pairing; revocation of a Tether-issued JWT relies on its short lifetime.
- Actions: one so far (`save-handoff`); binding context comes from flags and env, not from a Tether session.

## License

See [LICENSE](LICENSE).
