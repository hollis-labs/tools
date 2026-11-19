-- +goose Up
-- Existing definitions remain drafts. Migration never manufactures approvals.
CREATE TABLE publication_events (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL, revision TEXT NOT NULL, body BLOB NOT NULL);
CREATE INDEX publication_events_identity ON publication_events(id,seq DESC);
CREATE TABLE published (id TEXT PRIMARY KEY, seq INTEGER NOT NULL REFERENCES publication_events(seq));
CREATE TABLE publication_policy (singleton INTEGER PRIMARY KEY CHECK(singleton=1), revision TEXT NOT NULL, body BLOB NOT NULL);
CREATE TABLE publication_policies (revision TEXT PRIMARY KEY, body BLOB NOT NULL);
CREATE TABLE publication_approvals (id TEXT PRIMARY KEY, revision TEXT NOT NULL, body BLOB NOT NULL);
CREATE TABLE publication_decisions (revision TEXT PRIMARY KEY, approval_id TEXT NOT NULL, body BLOB NOT NULL);
CREATE TABLE publication_receipts (caller TEXT NOT NULL, authority TEXT NOT NULL, key TEXT NOT NULL, digest TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(caller,authority,key));
CREATE TABLE source_verifications (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL, revision TEXT NOT NULL, verified_by TEXT NOT NULL, verified_at TEXT NOT NULL);
CREATE INDEX source_verifications_revision ON source_verifications(id,revision,seq DESC);
