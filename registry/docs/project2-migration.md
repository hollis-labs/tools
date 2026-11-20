# Project draft migration

`registry-project2-migrate` is an offline, one-time JSON draft rewriter. It
validates with the agency-owned `registry/contracts` module and its `project/2`
contract. Registry's
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

All files are transformed and validated against the contracts schema before any
output is staged. Input/argument envelopes also pass Registry's strict catalog
decoder; unknown fields, wrong field types and empty keys are refused. Existing
record migration envelopes require an id, version and reason together.

The complete output is written into a private sibling staging directory, then
published atomically with Linux `RENAME_NOREPLACE`. Existing destinations,
including ones created during staging, are never replaced. Failures clean the
staging directory; sources remain unchanged. Files use mode 0600 and the output
directory mode 0700. Publication is supported on Linux; other platforms fail
closed. Retrying the unchanged source batch with a fresh output path is safe;
rerunning against an existing output path is refused.

The rewriter:

1. Requires `project/1` in the source descriptor and authored Input, if present.
2. Promotes metadata.disposition and metadata.type from the descriptor or Input
   into the descriptor's first-class fields, deleting both metadata entries.
   Conflicting values fail the batch. Unrelated metadata is preserved.
3. Removes class `"lib"`, preserving the explicit authored type from the payload
   or metadata. Missing type is refused even when the legacy class is `"lib"`;
   no type is inferred from a class. The contracts destination schema validates
   type enums and requires a numeric tier where applicable. Both metadata
   locations must agree with each other and any first-class value.
4. Requires an authored disposition and type. It supplies no defaults and does
   not map lifecycle to disposition. It updates both schema fields to `project/2`.
5. Validates every destination payload with registry/contracts and refuses
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

Rehearsal drafts are archived rather than migrated. Tests generate independent
legacy fixtures, take a verified SQLite backup, and migrate only the disposable
copy. These fixtures are not evidence of live population or the authored cohort.
