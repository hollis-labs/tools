---
name: use-registry
description: Read or author operational definitions in the Registry catalog through its CLI, HTTP API, or registry MCP tools. Use for definition lookup, reference reports, and reviewed draft publication, not runtime execution or consumer cutover.
---

# Use Registry

Registry is a definition catalog. A record, relationship, weight or approval does
not grant runtime permission or launch anything. CLI and stdio MCP call the same
authenticated service; they never open a local database.

Discover exact versioned contracts with `registry kinds --json` and
`registry schema --schema KIND/VERSION --json`. Preserve canonical IDs verbatim,
including slashes and dot segments. HTTP IDs belong in JSON/query values.

Read published definitions by default. Use `--publication draft` to inspect
unpublished changes, `--full` for bodies, `--readable` for human tables and `--json`
for automation. Search uses FTS5 token phrases; it is not substring matching.
Follow every cursor with the same filters and authenticated caller. A reference
report covers one page; follow `cursor` while `has_more` is true. External
`unchecked` references have not been validated by Registry.

For authoring, use an operation envelope in `--file` (or stdin with `--file -`).
Writes require current `version_token`; reread after a conflict before deciding
whether to retry. Keep credentials in the configured token file and never embed
secret values in definitions, examples or reports. Missing local targets are
rejected unless unresolved references were explicitly authorized. Tool examples
are documentation and are never executed.

Editing published content creates a draft and leaves the published revision
current. Publication requires prepare → independent decision → publish, bound to
the exact candidate set, current published pointers and receipt manifest hash.
A changed candidate or stale receipt needs fresh review. Elevated agent/config
publication remains reserved to the existing owner process; do not bypass a
fail-closed decision. Relationship review independently binds the source version,
edge ID and returned edge `hash`; it does not publish the source.

Use `registry migrate` for an explicit transition to a supported shared schema,
with a rationale; ordinary update cannot change the schema. Migration creates a
draft and an audit entry, with no automatic aliases or publication. Do not invent
new kinds locally. Maintenance uses protected server policy; retention defaults
to a rollback preview. Applying retention, scheduling a job, restoring production
or changing consumers needs the authorization for that actual action.

For exact operation tables and example envelopes read [README](../../README.md).
For migrations, retention and recovery read [hardening](../../docs/hardening.md).
