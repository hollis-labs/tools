# Cutover plan: the pre-Substrate service to the Substrate build

This is the plan the owner runs. Nothing in it has been applied: the live service, its descriptor,
its data and the proxy configuration were never touched while writing it. Every command below with a
data path was rehearsed end to end on a copy of the live data (record at the end).

## Read this first

- **The new store is created beside the old files, in the same directory.** The other ports use a new
  directory; Caster does not, because `CASTER_DB_PATH` and `CASTER_WORKSPACE` already place the store and Conduit
  and the hub child find it through them. So `<data>/workspaces/default/` ends up holding `main.db`, `main.db-wal`,
  `main.db-shm`, `blobs/` (the old files, never written again) and `caster.db`, `cas/` (new). The new binary
  never opens the old names, and refuses to start while `main.db` exists and `caster.db` does not.
- **Code is a clean break; data is not.** Every row, blob and the full-text index move. The old pairing records move
  as history (`legacy_grants`, `legacy_devices`) but are **not credentials**: the ten paired devices need new
  credentials (below).
- **The newest data is in the write-ahead log.** A copy of `main.db` alone silently loses it. The order below takes
  an online backup (SQLite's backup API, which includes the log), stops the service, takes a second one and
  proves it holds everything the stopped database holds, and migrates from that. `caster migrate` refuses an
  old database whose `main.db-wal` holds data, refuses to run while something listens on `httphost.addr`, and
  refuses a target that exists.
- **No Cerberus apply, deploy or reload of `caster-uat` until the descriptor is updated** (step 9). The
  current descriptor starts `./bin/caster serve -addr 127.0.0.1:8991`; the `-addr` flag no longer exists
  (the address is `httphost.addr`) and the health URL path changed from `/health` to `/healthz`. A
  Cerberus rebuild or restart before the edit would run the new binary with a flag it does not know.
  `deploy.yaml`'s unit drop-in (ExecStart and `SUBSTRATE_CONFIG`) stays.

## Step 0: nothing of the new store exists (no change made)

```sh
D=<data>/workspaces/default           # the directory `caster paths` prints as the db's directory
ls -la "$D"
```

Require: `main.db` (and normally `main.db-wal`, `main.db-shm`), `blobs/`. Require that **none** of these exist:
`caster.db`, `caster.db-wal`, `caster.db-shm`, `cas/`, `caster.db.migrating`, `cas.migrating`, `.migrate-scratch`,
`migrate-report.json`. If one does, stop: an earlier attempt or a different build has been here; the plan does
not guess. (`caster migrate` repeats this check and changes nothing when it fails.)

Also require that the descriptor has not been applied, `caster version` of the release binary prints the expected
build, and the machine has enough free space for: the backup (about the size of `main.db`) plus the blob tree twice
(the backup hard-links it, the migration copies it into `cas/`).

## Step 1: prepare (the old service keeps running)

1. Write the Substrate config file named by `SUBSTRATE_CONFIG` (path in `deploy.yaml`): the old environment
   variables become keys. Read the unit's current environment first (`systemctl --user show -p Environment <unit>`,
   names and the non-secret values) and map them:

   ```yaml
   httphost:
     addr: "127.0.0.1:8991"
   caster:
     torque_url: ""          # was CASTER_TORQUE_URL
     tesseract_mcp: ""       # was CASTER_TESSERACT_MCP
     tesseract_bin: ""       # was CASTER_TESSERACT_BIN
     allowed_hosts:          # every Host a proxy forwards: <public-host>, <tailnet-host>
       - <public-host>
       - <tailnet-host>
   identity:
     trusted_proxy: true     # only because the proxies below overwrite X-Forwarded-Proto
   ```

   `HOLLIS_AUTH` and `HOLLIS_AUTH_CASTER` are gone (`auth.mode` is `enforce` and is not to be changed).
2. Put the release binary in place **beside** the running one (not over it), and check it prints help:
   `/path/to/new/caster` with no arguments prints help and starts nothing.
3. Choose a backup directory on the same filesystem as the data directory: `B=<backup-dir>/caster-cutover`.

## Step 2: online backup while the service runs

```sh
mkdir -p "$B/b1"
sqlite3 -readonly "$D/main.db" ".backup $B/b1/main.db"     # SQLite online backup: includes the log
cp -al "$D/blobs" "$B/b1/blobs"                             # hard links: blobs are write-once, nothing is copied
```

This copy is the fallback if the stop in step 3 goes wrong. It is not the one that is migrated.

(Rehearsed on a database with 500 rows that existed only in an open writer's log: the backup held all 500, a plain
`cp` of `main.db` held none, and the `.dump` hashes of the live database and the backup were equal.)

## Step 3: stop the old service

Stop the unit (`systemctl --user stop com.hollis-labs.cerberus.caster.caster-uat.service`, or the Cerberus stop;
**not** an apply). From here the old data does not change. Check it is stopped: nothing listens on 8991
(`ss -ltn 'sport = :8991'` prints no listener) and `pgrep -x caster` finds nothing for the old binary.

## Step 4: second backup of the stopped database, and the proof it is complete

```sh
mkdir -p "$B/b2"
sqlite3 -readonly "$D/main.db" ".backup $B/b2/main.db"
cp -al "$D/blobs" "$B/b2/blobs"

# 1. the stopped database and the backup hold the same data, row for row
sqlite3 -readonly "$D/main.db" .dump | sha256sum
sqlite3 -readonly "$B/b2/main.db" .dump | sha256sum              # the two hashes must be equal
# 2. the backup is a sound database
sqlite3 "$B/b2/main.db" 'pragma integrity_check'                  # ok
# 3. the same blob files
diff <(cd "$D/blobs" && find . -type f -not -path './tmp/*' | sort) \
     <(cd "$B/b2/blobs" && find . -type f -not -path './tmp/*' | sort)    # no output
# 4. anything written between the two backups is in the second one (it is expected to differ from b1 only by that)
sqlite3 -readonly "$B/b1/main.db" .dump | sha256sum
```

`.dump` reads the stopped `main.db` together with its `-wal`, so equal hashes prove the backup holds the log's
content too. If the first two hashes differ, stop: do not migrate; start the old service again (step 10 rollback
row 1) and investigate. Blob contents are checked again by the migration, which re-hashes every file.

## Step 5: migrate from the second backup

```sh
caster migrate --from "$B/b2" --report "$B/migrate-report.json"
```

`caster migrate` reads `main.db` and `blobs/` from `--from` and builds `caster.db` and `cas/` in `$D` (the new
binary resolves the same data directory the old one used; `caster paths` shows it). It builds them under staging
names and renames them into place only when the proof passes: every table's row count and digest, every blob
re-hashed against its name and size, the rebuilt full-text index compared row by row with the old one. It prints
`PROVED EQUAL` or exits 1 with nothing left behind. Take the live counts yourself before the migration
(`sqlite3 -readonly "$B/b2/main.db" 'select count(*) from blobs'`, and the same for `name_revs`, `grants`, `devices`) and
compare them with the report; the rehearsal record below shows what the copy gave.

Then, with the service still stopped:

```sh
caster migrate --verify-only --from "$B/b2"        # compares caster.db and cas/ with the backup again; writes nothing
sha256sum "$D/main.db" "$B/b2/main.db"             # the old file is unchanged and equals the backup (when its log was empty)
```

## Step 6: bootstrap identity, offline, with the new binary

```sh
caster identity setup --username <owner>            # the password on stdin, never in the shell history
caster identity key create --name hub    --scope <the scopes the owner chose, below> --out <path>/hub.key
caster identity key create --name agents --scope caster.read,caster.write,caster.resolve --out <path>/agents.key
caster identity key create --name <each other caller in callers.md> ...
```

Key files are `0600`, outside every repository. Create the owner and the keys before the first start; the service
refuses to create a signing key itself when the database holds identity rows, and `GET /api/setup` answers
`setup_required: false` once the owner exists.

**Scopes for the hub's key (the owner chooses).** Today the hub's paired device holds `caster.read caster.write
caster.resolve` only, so `caster_act`, `caster_observe`, `caster_shadow_report` and `caster_shadow_label` are listed
to every agent on the mesh but are refused when called. Options: **(a) same as today**: read, write, resolve (plus
`caster.mcp`, which the MCP endpoint needs and the old device did not); **(b) with `caster.act`** so agents can
run `save-handoff`; **(c) with `caster.admin`** for the shadow report and labels. Recommended: (a). The tools
agents lose compared with listing everything are those four, and they have not worked for agents so far. A
tool refused by scope is reported as `denied`.

## Step 7: install, then start

1. The owner installs the binary and the unit drop-in with `fleet-install deploy caster` (the descriptor is
   `deploy.yaml`; its preflight backs up the migrated `caster.db`). The drop-in sets
   `ExecStart=%h/.local/bin/caster serve` and `SUBSTRATE_CONFIG`.
2. Start the unit by systemd (not a Cerberus apply). Check `GET /healthz` is `{"status":"ok",...}` and
   `GET /api/setup` is `{"setup_required":false,...}` on loopback.
3. Check each way in, with the `Host` the caller presents (see callers.md): loopback, `<tailnet-host>` through
   Tailscale Serve, `<public-host>` through the reverse proxy. A name missing from `caster.allowed_hosts`, or a
   proxy that does not send exactly one `X-Forwarded-Proto: https`, answers `403 Invalid host`; fix the
   config or the proxy, not the guard. Tailscale Serve and the reverse proxy both reach the service as a loopback
   peer.
4. Sign in to the browser page with the owner's password; list and open a note; search a word known to be in a
   stored text blob; fetch one blob by `cas://`.

## Step 8: hand out credentials and repoint callers

See callers.md for every caller. In short: the ten paired devices (labels in the owner's note) each need
`caster login <url> <pairing-code>` with a code minted by an administrator (`POST /api/identity/pairing-codes`,
which only exists over HTTP), or an offline `identity key create` file for a machine caller; the Tether catalog
entry for the hub's `caster` child moves to the identity key and to the loopback address (plan text in
callers.md, not applied here); Conduit needs nothing but the new binary on its `PATH`.

## Step 9: Cerberus descriptor (the owner's edit, then and only then an apply)

In the `caster-uat` resource of the project descriptor:

```yaml
    command:
    - ./bin/caster
    - serve
    # removed: -addr and 127.0.0.1:8991 (httphost.addr in the SUBSTRATE_CONFIG file)
    url: https://<public-host>/healthz     # was .../health
```

The unit also needs `Environment=SUBSTRATE_CONFIG=%h/.config/caster/caster.yaml`; `deploy.yaml`'s drop-in already
sets it, and the descriptor must not undo the drop-in. Until this edit is made, nobody runs a Cerberus apply,
deploy or reload for `caster-uat`.

## Step 10: rollback

| When | Do | Data |
|---|---|---|
| The stop (step 3) went wrong, or step 4's proof failed | start the old unit; nothing new exists yet | nothing lost |
| Migration failed or did not prove equal | nothing to undo: no `caster.db`, no `cas/`; start the old unit | nothing lost |
| After the new service ran | stop it, remove `caster.db*`, `cas/` and `migrate-report.json` from `$D` (new files only), start the old unit with the old binary and unit | everything written **since the new service started** exists only in `caster.db`; export what matters first (`caster ls`, `caster get`) or accept the loss. After the owner has accepted the new service, roll forward instead |

The old `main.db` and `blobs/` are never written by any step above, so they are the rollback. Keep them, and the
backups, until the owner decides otherwise.

## What changes for the owner (summary)

- Pairing codes and `cst_` tokens are gone: `caster pair`, `devices`, `revoke`, `renew`, `token` do not exist.
  Credentials are identity keys and sessions; machine credentials by offline `identity key create --out`, a
  pairing code only by an administrator over HTTP.
- `/v1` is removed; the API is `/api` with `caster.*` scopes and flat `{code, message}` errors.
- Bare `caster` prints help; only `caster serve` serves. The `-addr` flag is `httphost.addr`.
- `CASTER_DB_PATH` is read as the old database file's location and its parent is the store directory.
- The local CLI and `caster mcp` hold read, resolve, write, observe; `act` and the shadow report need a key.

## Left for later

- The browser page stays the small hand-written one for this cutover. A kit-based UI is a later task for the GUI
  track; the page is not to grow.
- Ports of the Cerberus descriptor to the new schema, a Caddy/Tailscale Serve review, and the removal of the old
  `main.db` and `blobs/` after the owner is satisfied.
- `internal/hostguard` is an app-local copy of Loom's package; a framework request for a shared one is held.

## Rehearsal record

Rehearsed in a scratch directory on the copy of the live data made for this purpose (never the live files), on a
port reserved for the migration agent, with temporary XDG directories: online backup (`.backup`), stop (simulated),
second backup, `.dump` hash equality, integrity check, blob list equality, `caster migrate --from` the second
backup (302 blobs and 29,163,525 bytes re-hashed, 313 name revisions, 0 action-log, observation and detection
rows, 15 grants and 10 devices in the legacy tables, 46 of 46 full-text rows equal, `PROVED EQUAL`),
`--verify-only` (`PROVED EQUAL`), `main.db` sha256 equal to before, offline identity bootstrap, start, `/healthz`,
`/api/setup`, 163 names listed with a read/write/resolve key, `/api/report` refused for that key (403), a
`cst_`-style token refused (401), an unknown Host refused (403), `caster whoami` as a remote client with the key,
and SIGTERM stopping the service cleanly.
