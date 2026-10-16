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
| `tesseract://<ns>/<key>`, `tesseract://item/<record_id>` | Tesseract `context` record, via the `tesseract` CLI (`CASTER_TESSERACT_BIN`) |

Bare `ns/name`, a task id or a 64-hex hash is accepted wherever a ref is.

## Storage

go-apppaths layout (`caster paths`): blobs under `blobs/sha256/ab/cdef…` (write-once, mode 0400, digest verified on write and on read), SQLite index (`main.db`) for blob metadata, name revisions and FTS5. Dedup is by hash; a name is a pointer with revision history, never mutable bytes.

## Known limits (spike)

- Tesseract: the CLI only reaches `context` records. Memory/knowledge/event items (the ULID ones the MCP tools return) need a Tether gateway client; `item/<id>` lookup scans only the first 500 records; `@rev` is unsupported.
- No GC/pin/quota, no auth or namespace permissions (single local user), no remote put, no HTTP/MCP transport yet. Whole-file CAS, no chunking.
- Torque and Tesseract content is rendered as markdown by caster; the brief's `summary`/`preview` modes are not implemented.
