-- +goose Up
CREATE TABLE import_plans (id TEXT PRIMARY KEY, caller TEXT NOT NULL, authority TEXT NOT NULL, body BLOB NOT NULL);
CREATE TABLE import_receipts (caller TEXT NOT NULL, authority TEXT NOT NULL, key TEXT NOT NULL, digest TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(caller,authority,key));
CREATE TABLE import_sources (authority TEXT NOT NULL, connector TEXT NOT NULL, source_key TEXT NOT NULL, id TEXT NOT NULL, source_revision TEXT NOT NULL, source_digest TEXT NOT NULL, registry_revision TEXT NOT NULL, PRIMARY KEY(authority,connector,source_key), UNIQUE(authority,connector,id));
CREATE TABLE import_checkpoints (authority TEXT NOT NULL, connector TEXT NOT NULL, checkpoint TEXT NOT NULL, PRIMARY KEY(authority,connector));
CREATE TABLE change_cursors (token TEXT PRIMARY KEY, caller TEXT NOT NULL, sequence INTEGER NOT NULL);
CREATE TABLE export_receipts (id TEXT PRIMARY KEY, caller TEXT NOT NULL, authority TEXT NOT NULL, body BLOB NOT NULL);
