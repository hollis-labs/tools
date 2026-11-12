-- The Conduit schema as one migration (clean break from the pre-Substrate layout). SQLite only.
--
-- It is the final shape of the old runs, attempts and panics tables. Not here, on purpose: accounts, API keys and
-- sessions (the identity module's identity_* tables). The old tokens table is kept as retired_tokens: no auth path
-- reads it; `conduit migrate-legacy` fills it for the audit trail.
--
-- Until the first release on Substrate this is the ONLY migration: a schema change edits this file.

-- +goose Up

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    session TEXT NOT NULL DEFAULT '',
    parent TEXT NOT NULL DEFAULT '',
    principal TEXT NOT NULL DEFAULT '',
    host TEXT NOT NULL DEFAULT '',
    cwd TEXT NOT NULL DEFAULT '',
    command TEXT NOT NULL,
    parsed TEXT NOT NULL,
    effect TEXT NOT NULL,
    policy_verdict TEXT NOT NULL,
    policy_reasons TEXT NOT NULL,
    policy_mode TEXT NOT NULL,
    approval TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    ended_at TEXT NOT NULL DEFAULT '',
    sandbox TEXT NOT NULL DEFAULT '',
    request TEXT NOT NULL DEFAULT '',
    approved_by TEXT NOT NULL DEFAULT '',
    approved_at TEXT NOT NULL DEFAULT '',
    heartbeat_at TEXT NOT NULL DEFAULT '',
    owner_pid INTEGER NOT NULL DEFAULT 0,
    owner_start INTEGER NOT NULL DEFAULT 0,
    pgid INTEGER NOT NULL DEFAULT 0,
    pgid_start INTEGER NOT NULL DEFAULT 0,
    failure_class TEXT NOT NULL DEFAULT '',
    failure_message TEXT NOT NULL DEFAULT '',
    panic_ref TEXT NOT NULL DEFAULT ''
);
CREATE INDEX runs_session ON runs(session, created_at);
CREATE INDEX runs_created ON runs(created_at);

CREATE TABLE attempts (
    run_id TEXT NOT NULL REFERENCES runs(id),
    n INTEGER NOT NULL,
    retry_of INTEGER NOT NULL DEFAULT 0,
    started TEXT NOT NULL,
    ended TEXT NOT NULL,
    exit_code INTEGER NOT NULL,
    signal TEXT NOT NULL DEFAULT '',
    class TEXT NOT NULL,
    start_err TEXT NOT NULL DEFAULT '',
    stdout_ref TEXT NOT NULL DEFAULT '',
    stderr_ref TEXT NOT NULL DEFAULT '',
    stdout_bytes INTEGER NOT NULL DEFAULT 0,
    stderr_bytes INTEGER NOT NULL DEFAULT 0,
    truncated INTEGER NOT NULL DEFAULT 0,
    redactions INTEGER NOT NULL DEFAULT 0,
    scrubbed TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (run_id, n)
);

CREATE TABLE panics (
    ref TEXT PRIMARY KEY,
    at TEXT NOT NULL,
    where_ TEXT NOT NULL,
    value TEXT NOT NULL,
    stack TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX panics_at ON panics(at);

-- The pre-Substrate API tokens, copied whole (hash included) by migrate-legacy and never read by any auth path.
CREATE TABLE retired_tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    hash TEXT NOT NULL,
    scopes TEXT NOT NULL,
    created_at TEXT NOT NULL,
    revoked_at TEXT NOT NULL DEFAULT '',
    last_used TEXT NOT NULL DEFAULT '',
    retired_at TEXT NOT NULL
);
