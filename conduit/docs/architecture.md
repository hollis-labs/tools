# Architecture

Conduit is an application on the Substrate framework: the manifest (`cmd/conduit/substrate.yaml`) names the modules, the
generated `cmd/conduit/substrate_wiring.go` starts them, and `cmd/conduit` is the composition root. What follows is the
shape that is Conduit's own. How the port was done and what changed: `docs/conversion-notes.md`.

## Layers

```
cmd/conduit            composition root: command plan, module start, service build, serve, mcp, migrate-legacy
internal/cli           command-line transport (and the approve/reject HTTP client in remote.go)
internal/api           HTTP transport: /api routes, per-principal rate limit, JSON errors
internal/mcp           MCP tools (module-registered, scope-checked); stdio under local trust
internal/service       the one place that decides: Submit / Approve / Reject / Execute / Start / Cancel, redaction, views
internal/store         SQLite over the framework's core/store (one write connection, a reader pool, WriteTx)
internal/persistence   embedded goose migration, data-layout check, old-layout refusal
internal/legacy        one-way migration from the pre-Substrate database, with proof
internal/policy, cmdparse, cmdrun, runbook, proc, blob, fuzzy, hostguard, appdirs, settings
```

Transports decode, call the service and encode; `.golangci.transport.yml` fails the build when `internal/api`, `internal/mcp`
or `internal/cli` touch the store or `database/sql` (`docs/transport-boundary.md`).

## The command plan

A command line decides which modules start and what may be created (`cmd/conduit/plan.go`). Help, version, an unknown word
and `--help` start no module that opens a file. Read-only commands need an existing database; writers may create it;
only `serve`, `migrate-legacy` and the two offline identity commands start the whole set. See the conversion notes for the
table and the tests.

## Identity and approval

The identity module is the identity provider: the owner is created offline (`conduit identity setup`), agents and services
get API keys (`conduit identity key create`), and people sign in with a session. There is no open setup route.

The auth guard runs in `enforce` mode: every `/api` route and every MCP tool maps to a scope (`conduit.read`,
`conduit.read.all`, `conduit.run`, `conduit.approve`, `conduit.admin`), and a route with no mapping is refused. Approving or
rejecting a run is stricter than a scope: the HTTP layer marks the request context as a person only for a verified principal
of kind `user`, and the service refuses `Approve` and `Reject` on any other context, on a retired (legacy) run, and when the
approver is the submitter. An API key with `conduit.approve` therefore still cannot approve.

## A run

`Submit` parses the command, applies the policy (allow, ask, deny) and records the decision. Deny is final. Allow is started
by the transport that asked. Ask is recorded `pending`; a person approves it through `POST /api/runs/{id}/approve` (or
`conduit approve --server ...`), the policy is decided again at that moment, the approval expires, and the serving process
starts it exactly once. Output is redacted before it is stored, kept as a content-addressed blob (local directory, or caster)
and returned labelled untrusted.

## Data

`<data>/conduit.db` (one goose migration: `runs`, `attempts`, `panics`, `retired_tokens`), `<data>/blobs/`, the identity
signing key and the framework's own tables. The data directory is `CONDUIT_DATA_DIR` or the XDG data home; it is the only
environment variable the application reads.

Rows copied from the pre-Substrate database carry `runs.legacy = 1`. Every state-changing statement filters on
`legacy = 0`, and the service answers `ErrLegacy` for approve, reject, cancel, start and execute on such a row, so history
can be read and recalled but never acts.

## Modes

| Mode | Module set | Identity | Starts approved runs |
|---|---|---|---|
| `conduit serve` | full | keys and sessions | for `key:` and `user:` principals |
| `conduit mcp` (stdio, launched by an agent) | full, no listener | local trust, read and run only | for `local:operator` |
| local commands (`run`, `ls`, `show`, ...) | config, secrets, database | the invoking user; no approval | no |
