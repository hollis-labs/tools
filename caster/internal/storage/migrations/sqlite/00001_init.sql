-- The Caster schema, squashed to one migration (clean break from the pre-Substrate inline DDL).
-- Caster is SQLite only: search is FTS5 and queries use SQLite syntax.
--
-- Times are RFC 3339 text, as the pre-Substrate database stored them, so `caster migrate` copies
-- rows without converting anything.
--
-- Not here, on purpose: accounts, API keys, sessions and pairing are the identity module's
-- identity_* tables. The two legacy_* tables hold the old pairing records as history only: the
-- hashes are no credential any code accepts, and nothing reads them but `caster migrate`.
--
-- Until the cutover (the first migrated instance) this is the ONLY migration: a schema change edits this
-- file. After the cutover the file is frozen and every change is a new migration.

-- +goose Up

CREATE TABLE blobs (
	hash TEXT PRIMARY KEY,
	size INTEGER NOT NULL,
	mime TEXT NOT NULL,
	source_ref TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE TABLE name_revs (
	ns TEXT NOT NULL,
	name TEXT NOT NULL,
	rev INTEGER NOT NULL,
	hash TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (ns, name, rev)
);

CREATE TABLE action_log (
	idem_key TEXT PRIMARY KEY,
	action TEXT NOT NULL,
	source_ref TEXT NOT NULL,
	outcome_ref TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE observations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	text_hash TEXT NOT NULL,
	generation INTEGER NOT NULL,
	first_source TEXT NOT NULL DEFAULT '',
	first_at TEXT NOT NULL,
	last_at TEXT NOT NULL,
	seen INTEGER NOT NULL DEFAULT 1,
	candidates INTEGER NOT NULL,
	resolved INTEGER NOT NULL,
	would_show INTEGER NOT NULL,
	UNIQUE (text_hash, generation)
);

CREATE TABLE detections (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	observation_id INTEGER NOT NULL REFERENCES observations (id),
	extractor TEXT NOT NULL,
	confidence TEXT NOT NULL,
	ref TEXT NOT NULL,
	scheme TEXT NOT NULL,
	state TEXT NOT NULL,
	verdict TEXT NOT NULL,
	would_show INTEGER NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	label TEXT NOT NULL DEFAULT ''
);
CREATE INDEX detections_ref ON detections (ref);

-- Full-text index over text blobs. Derived from the blobs: `caster migrate` rebuilds it.
CREATE VIRTUAL TABLE blob_fts USING fts5 (hash UNINDEXED, body);

-- Old pairing grants and paired devices, carried across unchanged as history.
CREATE TABLE legacy_grants (
	id TEXT PRIMARY KEY,
	code_hash TEXT NOT NULL UNIQUE,
	scopes TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	consumed_at TEXT,
	revoked_at TEXT
);

CREATE TABLE legacy_devices (
	id TEXT PRIMARY KEY,
	token_hash TEXT NOT NULL UNIQUE,
	label TEXT NOT NULL DEFAULT '',
	scopes TEXT NOT NULL,
	grant_id TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	revoked_at TEXT,
	last_used_at TEXT
);
