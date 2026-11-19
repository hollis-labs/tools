-- +goose Up
-- An authorization is single-start. A process crash leaves started evidence;
-- inspecting that evidence cannot authorize another filesystem attempt.
CREATE TABLE export_attempts (receipt_id TEXT PRIMARY KEY REFERENCES export_receipts(id), body BLOB NOT NULL);
