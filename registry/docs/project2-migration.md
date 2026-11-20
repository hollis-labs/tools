# Project draft migration

`registry-project2-migrate` is an offline, one-time JSON draft rewriter. It
requires a Substrate version whose shared kinds support `project/2`. The
command refuses to produce output if that schema is unavailable. Registry's
normal request path never rewrites old schemas or reads metadata aliases.

The destination contract has first-class `disposition` and `type`:

| Field | Values |
|---|---|
| disposition | continue, tbd, experiment, decompose, dissolve, retire, parked, harvested |
| type | framework, monorepo, lib, service, app, tool |
| class | integer 1 (core), 2 (service), 3 (app) |

Class may be absent for framework, monorepo and lib. Other types require a
tier. Type and tier describe separate decisions; the migration never infers
one from the other. Lifecycle remains a separate field.

## Rewrite an authored copy

Keep the source drafts and their checksums. Work on a copy, with a new output
directory. The command accepts one JSON object per file: a project descriptor,
a Registry authored Input (`schema`, `key`, `payload`, optional metadata), or
a `record` argument envelope. It does not accept database rows or paged API
responses. It never opens SQLite or submits API mutations.

```sh
go run ./cmd/registry-project2-migrate --output ./project2-copy ./drafts/*.json
```

All files are transformed and validated against the shared schema before the
output directory is created. An existing output directory is refused. Input
files remain unchanged; output drafts use private file permissions. A filesystem
write failure can leave an incomplete output directory; discard that directory
before retrying.

The rewriter:

1. Requires `project/1` in the source descriptor and authored Input, if present.
2. Promotes metadata.disposition and metadata.type from the descriptor or Input
   into the descriptor's first-class fields, deleting both metadata entries.
   Conflicting values fail the batch. Unrelated metadata is preserved.
3. Removes class `"lib"`, preserving an explicit framework, monorepo or lib
   type. If no type is authored, class `"lib"` supplies type `"lib"`. Other
   explicit types conflict and need an authored tier correction.
4. Requires an authored disposition and type. It supplies no defaults and does
   not map lifecycle to disposition. It updates both schema fields to `project/2`.
5. Validates every destination payload with Substrate/kinds and refuses
   secret-like values. A second rewrite of the destination is refused.

For example, an authored draft fragment:

```json
{"schema":"project/1","class":"lib","metadata":{"disposition":"continue","type":"lib","note":"preserve"}}
```

becomes the following fragment (complete drafts must also carry the other
required project fields):

```json
{"schema":"project/2","type":"lib","disposition":"continue","metadata":{"note":"preserve"}}
```

Inspect the copy before admitting it. Confirm source checksums are unchanged,
every destination schema is `project/2`, no class is `"lib"`, and neither
descriptor nor envelope metadata retains `type` or `disposition`.

For an existing Registry draft, preserve its canonical identity and current
version token in a `record_migrate` envelope with an explicit reason, and use
the existing conditional `registry migrate --file ...` operation only during
a separately authorized activation. Do not re-create identities, edit immutable
history, or migrate published revisions in place. This offline utility does
not apply the migration or grant deployment approval.
