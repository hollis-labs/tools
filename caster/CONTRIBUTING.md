# Contributing to Caster

Content-addressed store and ref resolver for scratchpads, drafts, notes, Torque tasks and Tesseract objects. One store on a home node; CLI, browser UI, MCP and HTTP are thin wrappers over one service layer.

## Start Here

- `.golangci.transport.yml` — the transport-boundary gate; `docs/transport-boundary.md` says how to run it.
- `cmd/caster/main.go` — the composition root: resolves paths (go-apppaths), gates telemetry (go-otel), opens the store, builds the service layer, and chooses local vs remote mode (`remote.go`, `serve.go`); the only place that opens the store.
- `internal/service` — the contract and the logic. `contract.go` has the domain interfaces (`NotesAPI`, `RefsAPI`, `ActionsAPI`, `ShadowAPI`, `AccessAPI`) and `Compose`; `authz.go` the scopes; `access.go` pairing and devices; `actions.go`, `shadow.go`, `notes.go`, `refs.go`.
- `internal/ref` (parse and detect), `internal/resolver` (cas, caster, torque, tesseract), `internal/mcpclient` (stdio MCP client for the Tesseract gateway), `internal/store` (SQLite + blobs).
- `internal/cli`, `internal/mcp`, `internal/api` (+ `internal/api/ui`, the embedded browser UI) are the transports; `internal/client` is the remote implementation of the same service interfaces, used when this machine is a client of a home node.
- Deploy: `caster.cerberus.yaml` (Cerberus resource `caster-uat`, a systemd user service on the home node), `deploy/tailscale-serve.sh` (tailnet HTTPS), `deploy/tether/*` (draft catalog entries for the MCP server and a `caster` agent).
- `../.github/workflows/tool-checks.yml` — the full CI gate.

## Commands

```sh
gofmt -l .
go vet ./...
go test -race -count=1 ./...
GOWORK=off golangci-lint run --allow-parallel-runners --config .golangci.transport.yml ./...
make dist        # client binaries in bin/dist/ for scp
```

CI (`../.github/workflows/tool-checks.yml`) is the full gate. If you run a Cerberus-managed home node, deploy its resource after changing runtime source; a plain `go install` does not update that service.

## Boundaries

- Transports (the packages matched by `transport_dirs_regex` in `.golangci.transport.yml`) call the service layer, never `store.Store` methods or raw SQL; the gate is `.golangci.transport.yml`, CI rejects transport-boundary findings. A new operation goes in the service contract first, then in each transport: CLI, MCP, HTTP, and `internal/client` for remote mode.
- Authorization lives in the service layer, not in transports. Requests without an identity are refused; a transport only stamps who is calling (`AsLocalOperator` for the CLI and stdio MCP, the verified principal for HTTP) (`TestFailClosedWithoutIdentity`, `TestScopeMatrix`). Scopes: `caster.read`, `.resolve`, `.write`, `.act`, `.observe`, `.admin`; `resolve` is separate so a read-only holder cannot read everything the home node can reach.
- Blobs are immutable and addressed by sha256; mutable things (names, tasks, records) are pointers or snapshots, never blob contents (`TestPutDedupsAndVerifies`, `TestNameRevisions`). There is no delete; do not add one without deciding what happens to blobs other names still reference.
- Detection never fetches; only refs a resolver validates are shown or expanded. `scan` and `expand` treat a remote ref the caller lacks `resolve` for as unresolved, never as forbidden, so they cannot be used to probe (`TestDetectIsQuietAboutProse`, `TestScanAndExpandDoNotLeakRemoteRefsWithoutResolve`). Expanded content is labeled untrusted.
- Tesseract resolution reads nothing until materialized, and materializing is a read that reinforces the item (`TestGatewayResolveReadsNothingMaterializeReads`); an absent gateway is `unavailable`, not `not_found` (`TestGatewayAbsentIsUnavailable`). Caster holds no Tesseract credentials: it launches the server from the Tether catalog entry.
- Pairing: the code is the credential. Only SHA-256 of codes and device tokens is stored; every failed exchange answers identically; the route is rate-limited and size-limited; grants can be created only at the home node's own terminal, never over HTTP; scopes can only be narrowed at exchange and renewal (`TestPairingLifecycle`, `TestOperatorKeyCannotMintGrantsOverHTTP`, `TestExchangeIsRateLimited`, `TestRevokedDeviceStopsAtOnce`). Exchange attempts are logged by shape (length, prefix, stray whitespace), never the code (`TestExchangeAttemptsAreLoggedWithoutTheCode`). Do not log a code or token anywhere.
- Identity sources are tried in order: device tokens, Tether JWTs (`aud=caster`, only when `CASTER_TETHER_URL` is set), the local operator key (`TestTetherIssuedTokens`). The Tether path is tested against a synthetic issuer only.
- The remote client refuses plain http except to loopback or a tailnet address, never follows redirects, and keeps the token out of its profile: the profile holds a URL and a secrets reference (`TestURLPolicy`, `TestRedirectsAreNeverFollowed`, `TestProfileHoldsNoSecretAndEnvWins`, `TestBadTokenSaysWhatToDo`). A not-found is an outcome, not an error (`TestOutcomesAreNotErrorsButFailuresAre`).
- The browser UI is a static shell that carries no data and is served without a token; everything it shows comes from authenticated `/v1` calls (`TestUIShellIsOpenButLocked`). Note text is untrusted: render it only with `textContent` and `createElement` in `internal/api/ui/app.js`, never `innerHTML`, and keep the CSP in `internal/api/gui.go` (no inline script or style, no third-party origins, no framing). Links are limited to `http(s)` and `caster://`. The device token lives in `sessionStorage`, not `localStorage`. A pairing code in a URL travels in the fragment and is removed on load.
- Actions with remote effects: `save-handoff` snapshots to CAS first, writes a Tesseract workspace draft (never promoted by caster), is idempotent by content and session, refuses `user/*` namespaces, and writes nothing when the source does not resolve (`TestSaveHandoffWritesOnceAndReplays`, `TestSaveHandoffUnresolvedWritesNothing`, `TestSaveHandoffValidatesContext`, `TestSaveHandoffDryRunStoresAndWritesNothing`).
- Shadow mode stores no message text, only ref, extractor, state and verdict; it validates candidates, so it needs `caster.observe` (`TestShadowPolicyDryRun`, `TestShadowRejectsHugeText`). Bump `ref.Generation` when an extractor changes.
- The HTTP service binds loopback; non-loopback `-addr` is allowed only with auth enforced. TLS is Tailscale Serve's job (`deploy/tailscale-serve.sh`), tailnet only, never Funnel.
- Exit codes `0/2/3/1` are the helper protocol; do not repurpose them. A missing ref exits 2 and is HTTP 404 with a body.
- Shared libraries come from `github.com/hollis-labs/libs` (`security/auth`, `security/secrets`); fill gaps here and say so, and move them to libs when they prove general.
