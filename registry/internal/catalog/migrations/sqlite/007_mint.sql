-- +goose Up
CREATE TABLE minted_identities (
 operation TEXT NOT NULL, authority TEXT NOT NULL, domain TEXT NOT NULL,
 logical_key TEXT NOT NULL, id TEXT NOT NULL UNIQUE, closed INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(operation,authority,domain,logical_key)
);
CREATE TABLE mint_receipts (
 operation TEXT NOT NULL, caller TEXT NOT NULL, authority TEXT NOT NULL, key TEXT NOT NULL,
 digest TEXT NOT NULL, body BLOB NOT NULL, expires_at TEXT NOT NULL,
 PRIMARY KEY(operation,caller,authority,key)
);
-- Closure is sticky even if an authored status is subsequently changed. The
-- mapping outlives receipts and revision retention; it is never a cascade child.
-- +goose StatementBegin
CREATE TRIGGER minted_workstream_closed AFTER UPDATE OF body ON revisions
WHEN json_extract(NEW.body,'$.kind')='workstream' AND
 (json_extract(NEW.body,'$.lifecycle') IN ('archived','deleted') OR
  json_extract(NEW.body,'$.payload.status')='closed')
BEGIN
 UPDATE minted_identities SET closed=1 WHERE id=NEW.id;
END;
-- +goose StatementEnd
