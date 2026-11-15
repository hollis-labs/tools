# Registry

Registry is a class 2 catalog service in the tools monorepo. It stores authored
operational definitions and relationships; it does not execute workflows,
launch agents, own runtime leases, or infer permission from a catalog record.

- `cmd/registry/substrate.yaml` owns module wiring; regenerate
  `substrate_wiring.go` with Substrate, never edit generated wiring.
- `internal/catalog` owns persistence/service behavior and common transports.
  Shared kind validation and tool hashes come from Substrate/kinds. Do not fork
  the schemas here or treat project parsing as class certification.
- Every mutation is transactional and conditional. Never replace immutable
  revision paging with high-water filtering of mutable current records.
- API/CLI/MCP preserve IDs verbatim; use query/JSON selectors, not path cleaning.
- Credential-to-authority/reviewer bindings are server configuration. Generic
  input never sets effective review, reviewer identity or verification stamps.
- CLI and stdio MCP are HTTP clients; only serve opens SQLite.
- Pins come from the selected Substrate BOM. Use `substrate upgrade` and
  `substrate pins sync --app .`; no hand-edited dependency versions.
- Run `go test -race ./...`, `go vet ./...`, build, pins/wiring checks and the
  repository-root layout/public scans. Root workflows carry Registry CI.
- Deployment follows docs/deployment.md, fleet-install ownership and Cerberus
  supervision. Driver approval of the concrete activation plan is separate
  from implementation. No population, migration or shared gateway reload.
