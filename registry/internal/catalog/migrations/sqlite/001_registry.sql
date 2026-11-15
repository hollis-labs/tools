-- +goose Up
CREATE TABLE revisions (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL,
 token TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL,
 authority TEXT NOT NULL,
 lifecycle TEXT NOT NULL,
 name TEXT NOT NULL,
 search_text TEXT NOT NULL,
 body BLOB NOT NULL
);
CREATE INDEX revisions_identity ON revisions(id, seq DESC);
CREATE TABLE records (id TEXT PRIMARY KEY, seq INTEGER NOT NULL REFERENCES revisions(seq));
CREATE TABLE aliases (system TEXT NOT NULL, alias TEXT NOT NULL, id TEXT NOT NULL REFERENCES records(id), PRIMARY KEY(system, alias));
CREATE TABLE receipts (caller TEXT NOT NULL, authority TEXT NOT NULL, key TEXT NOT NULL, digest TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(caller, authority, key));
CREATE TABLE cursors (token TEXT PRIMARY KEY, query_hash TEXT NOT NULL, caller TEXT NOT NULL, boundary INTEGER NOT NULL, after_id TEXT NOT NULL, as_of TEXT NOT NULL);
