-- +goose Up
ALTER TABLE import_plans ADD COLUMN expires_at TEXT NOT NULL DEFAULT '';
ALTER TABLE import_plans ADD COLUMN committed INTEGER NOT NULL DEFAULT 0;
CREATE TABLE import_prepare_keys (caller TEXT NOT NULL, authority TEXT NOT NULL, key TEXT NOT NULL, digest TEXT NOT NULL, plan_id TEXT NOT NULL REFERENCES import_plans(id), PRIMARY KEY(caller,authority,key));
ALTER TABLE import_sources ADD COLUMN source_authority TEXT NOT NULL DEFAULT '';
ALTER TABLE import_sources ADD COLUMN mapping_version TEXT NOT NULL DEFAULT '';
ALTER TABLE import_sources ADD COLUMN edge_set_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE import_sources ADD COLUMN import_receipt_id TEXT NOT NULL DEFAULT '';
CREATE TABLE import_failures (id TEXT PRIMARY KEY, plan_id TEXT NOT NULL, attempted_at TEXT NOT NULL, body BLOB NOT NULL);
CREATE TABLE export_attempts (receipt_id TEXT PRIMARY KEY REFERENCES export_receipts(id), started_at TEXT NOT NULL, state TEXT NOT NULL, body BLOB NOT NULL);
CREATE TABLE export_authorization_keys (caller TEXT NOT NULL, authority TEXT NOT NULL, key TEXT NOT NULL, digest TEXT NOT NULL, receipt_id TEXT NOT NULL REFERENCES export_receipts(id), PRIMARY KEY(caller,authority,key));
