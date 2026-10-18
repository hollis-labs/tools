# Transport boundary

Run this from the tool's module directory, with golangci-lint v2.11.4:

```sh
GOWORK=off golangci-lint run --config .golangci.transport.yml ./...
```

The dedicated config rejects direct `store.Store` method calls and raw
`database/sql` operations in `internal/api`, `internal/mcp` and `internal/cli`.
Transports decode requests, call the service layer and encode results. The
composition root under `cmd/` opens the store and wires the service.

The [monorepo workflow](../../.github/workflows/tool-checks.yml) runs this
check alongside ordinary lint, vet, race tests and vulnerability scanning.
Use the Go version declared by this module's `go.mod` and `GOWORK=off` so a
local multi-project workspace cannot hide a missing dependency.

## Scope and limitations

- Type-aware matching is required (`forbidigo.analyze-types: true`). Paths are
  relative to the module (`run.relative-path-mode: gomod`).
- Tests are excluded. A newly added transport directory must be included in
  the config's path expression.
- Interface-mediated calls and promoted methods on embedded store handles
  may escape this matcher. Review those boundaries directly.
- Storage DTO types are allowed. This is not a transport-parity check:
  authorization and behavior still need service-level tests.

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the behavioral invariants and
existing tests to run when changing them.
