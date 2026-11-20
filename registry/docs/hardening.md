# Catalog hardening and recovery

This change prepares maintenance commands and a disabled job definition. It does
not install a schedule, deploy a service, populate data, change consumers, or
modify gateway grants. The deployment owner resolves the job and configuration
against its supported Cerberus schedule contract before activation.

## Search and reference reports

Migration 004 adds two SQLite FTS5 indexes, one for immutable draft revisions and
one for immutable publication events. Names, summaries and complete authored
payload string values (including decoded multiline bodies) are indexed. Triggers populate new rows and backfill
existing data. Search selects the latest revision per ID at the cursor boundary,
then intersects the FTS row IDs. An old matching revision cannot make a newer,
nonmatching revision appear. The two sequence spaces are never interchanged.

`--query` is a case-insensitive token phrase using FTS5's default unicode61
tokenizer. Quotes and boolean operators are escaped as literal text. It does not
implement substring or prefix search. An empty/whitespace query lists records.

`registry references --publication draft --limit 100 --json` returns issues,
examined record count, boundary, as_of and continuation. Reuse all filters and
follow every page before claiming the report is complete. Published is the
default view. Local missing/wrong-kind targets are unresolved; archived/deleted
targets retain those states. External targets are unchecked, not claimed missing
or resolved. A report never grants permission or mutates a relationship.

## Retention policy

Server configuration, under the existing protected `registry` section:

```yaml
registry:
  maintenance:
    cursor_ttl: 168h
    receipt_ttl: 720h
    draft_history_ttl: 2160h
    backup_directory: /srv/data/backups/registry
```

These are example values, not enabled defaults. Each duration defaults to `0s`
(no expiration); negative/invalid durations fail configuration. The backup
directory has no default and must be an absolute operator-owned directory.
Only a principal already configured as an `owner` reviewer with `registry.review`
scope can run global maintenance. Request arguments cannot replace policy,
select a filesystem path or confer a role. No new credential is created here.

| Command | Effect |
|---|---|
| `registry retention --json` | Compute counts in one transaction, roll it back |
| `registry retention --apply --json` | Commit expiration and a maintenance audit entry |
| `registry backup --json` | Consistent online SQLite copy, integrity check, SHA-256 manifest |

Retention first expires cursors and both creation/publication replay receipts.
Existing rows get a fresh clock when migration 004 is installed; expiry never
uses the snapshot's `as_of` content timestamp. Expired cursors return a validation
error and require a fresh query. Expired replay keys no longer guarantee replay;
retain the receipt horizon longer than the longest client retry window.

Draft history pruning is conservative:

- All revisions of every identity ever published are retained forever, including
  its unpublished edits, archives and tombstones. Publication events, policies,
  decisions, approvals and verification evidence are never pruned.
- The latest revision and permanent identity claim always survive.
- Any unexpired cursor defers all draft history pruning, preserving snapshots.
- Creation responses, approval candidates, migration audit endpoints and pinned
  draft-edge target revisions are protected. Tool identities with authoritative
  classification decisions retain all revisions, preserving restrictive history.
  Any identity with historical relationship review stamps or a review hash also
  retains all revisions, including the original proposal and a later removal;
  removing an edge cannot erase its independent review evidence.

Only older, unprotected revisions of never-published identities are eligible.
The FTS delete trigger follows pruning in the same transaction. Maintenance logs
are retained indefinitely. Run a preview and verify a backup before enabling a
retention schedule; there is no automatic timer in the service.

## Consistent backup job (prepared, disabled)

[backup-job.json](backup-job.json) is an operator handoff definition, **not** an
installed Cerberus manifest. `enabled: false` is intentional. The existing
Cerberus owner converts the definition to its current schedule API, resolves the
absolute installed executable and protected existing token-file reference, and
confirms service/database/destination identity before scheduling. Proposed daily
cadence and non-overlap are requirements of that handoff; this repository neither
calls the scheduler nor supplies a new grant. Temporary connector schedules stay
with their separate connector/deployment owner; this backup job does not sync or
publish records.

The command is an HTTP client. It uses the running service's configured database
and backup directory, not a checkout-relative database. The service uses
`store.Backup`'s verified online SQLite backup API, including committed WAL data.
It chooses a unique timestamp/random filename, refuses overwrite through the
backup API, and writes the default adjacent `.manifest.json` sidecar. The server
records completion after the snapshot; that final audit entry is consequently
not inside its own backup. If the audit write fails, retain the manifest and
inspect the destination before retrying. Do not copy a live database file alone.
Backup-file rotation is intentionally not implemented; the owner chooses a
storage policy separately and must keep a verified pre-migration copy.

## Isolated restore verification

Use a scratch directory outside the canonical database and backup directory.
Do not replace production or start a second service on the production listener.

1. Record the backup path, manifest SHA-256, size, timestamp and exact running
   binary revision. Preserve both files. Verify the file hash against the
   manifest before use and perform SQLite `PRAGMA integrity_check` on a read-only
   connection (or `store.VerifyBackup` through the existing operator tooling).
2. Copy the consistent backup into the isolated directory with mode 0600 and
   owner-only directory access. Keep the original backup read-only. Use a copy,
   not a hard link. Verify its hash before opening it writable.
3. Use the same binary and an isolated protected config: absolute scratch
   `database.sqlite.path`, scratch secret/state paths and a different loopback
   listener. Existing verifier bindings remain required. Do not connect a
   gateway, schedule, sync connector or consumer. Starting a newer binary can
   apply additive migrations to the scratch copy; record before/after versions.
4. Verify published and draft reads, immutable history, FTS name/summary/body
   results, reference report pages, publication policies/decisions, aliases and
   replay receipts. Compare IDs and exact revision/hash values to the source
   evidence at backup time; later live writes must be absent. A health response
   alone is insufficient.
5. Stop the isolated service, preserve the verification report and remove only
   the scratch files created by the restore exercise. Do not delete the backup.

`TestConsistentBackupAndIsolatedRestore` exercises this path against a live WAL
store in temporary directories: verified backup and manifest, independent copy,
restored service/FTS/history/approval replay, and absence of post-backup writes.
It does not claim a production restore or a scheduled-job run.

Production recovery is a separate coordinated action: stop only Registry,
preserve its database plus WAL/SHM and intervening-write evidence, verify the
chosen backup, and assess write loss before substituting a restored file. Binary
rollback never reverses database migrations. Never overwrite a newer live store
or remove its sidecars while a process is using it.

## Explicit schema evolution

SQLite schema changes are numbered embedded goose migrations. Version 003 is
reserved for the independently developed sync/import migration. Files 001 and 002
are unchanged; 004 is additive, backfills search and operational clocks, and
creates kind-migration/maintenance logs. It never rewrites authored payloads or
publishes retained drafts. Apply only through the normal service migration path,
with a verified backup and the deployment owner's activation process. No
automatic down/destructive migration is supplied. Before activation, validate the
combined 003 sync/import and 004 hardening sequence against fresh main. Do not
apply isolated 004 to a live database and then introduce the reserved 003 later.

Kind schemas come exclusively from the pinned agency-owned Registry contracts
module (`registry/contracts`), with independent validation per version.
`registry kinds` lists exact supported versions; `registry schema --schema KIND/N`
retrieves one. Adding `kind/2` requires a new version in that module and an explicit
Registry dependency update. Existing versions are unchanged. Unknown versions fail
closed. Ordinary `update` cannot change
a schema; `migrate` takes the full replacement input, current source token and a
nonempty `reason`. Only the owning publication authority can migrate an identity;
its kind and canonical ID cannot change. The transaction validates the target,
creates a draft and logs actor, reason, timestamp, old/new schemas and revisions.
Publication remains a separate reviewed transition. Query the log with
`registry migrations ID --json` (limit/cursor supported).

Authored tool examples extend the Registry envelope, not shared tool/1. Admission
now identifies `registry.authored-envelope/2`; publication content digests include
examples. Outstanding approval subjects prepared under envelope/1 must be
prepared/reviewed again. Existing publications and immutable receipts survive.
No new shared kind or elevated publication is authorized by this change.
