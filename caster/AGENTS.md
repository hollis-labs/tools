# caster

Local content-addressed store and ref resolver for scratchpads, drafts, notes, Torque tasks and Tesseract objects

It is not: a second knowledge store (Tesseract keeps durable knowledge; caster keeps bytes, names and provenance) or a sync product.

## Start Here

- `.golangci.transport.yml` — the transport-boundary gate; `docs/transport-boundary.md` says how to run and ratchet it.
- `cmd/caster/main.go` — the composition root: resolves paths (go-apppaths), gates telemetry (go-otel), opens the store and builds the service layer; the only place that opens the store.
- `internal/service/service.go` — the service layer: unexported store, `Store()` escape hatch, `New(store)`.
- Run `go mod tidy` once after generating: `go.sum` is not rendered.
- `.github/workflows/check.yml` — the full CI gate.

## Commands

```sh
gofmt -l .
go vet ./...
go test -race -count=1 ./...
```

CI (`.github/workflows/check.yml`) is the full gate.

## Boundaries

- Transports (the packages matched by `transport_dirs_regex` in `.golangci.transport.yml`) call the service layer, never `store.Store` methods or raw SQL; the gate is `.golangci.transport.yml`, its ceiling `.github/quality/transport-baseline.txt` only decreases.
- Blobs are immutable and addressed by sha256; mutable things (names, tasks, records) are pointers or snapshots, never blob contents. `TestPutDedupsAndVerifies`, `TestNameRevisions`.
- Detection never fetches; only refs a resolver validates are shown or expanded. Expanded content is labelled untrusted.
- Exit codes `0/2/3/1` are the helper protocol; do not repurpose them.
