-- +goose Up
-- Additive only. Retain the immutable source and publication sequence spaces.
CREATE VIRTUAL TABLE revision_fts USING fts5(name, summary, body);
CREATE VIRTUAL TABLE publication_fts USING fts5(name, summary, body);
INSERT INTO revision_fts(rowid,name,summary,body)
 SELECT r.seq,r.name,COALESCE(json_extract(r.body,'$.summary'),''),COALESCE((SELECT group_concat(j.atom,' ') FROM json_tree(r.body,'$.payload') j WHERE j.type='text'),'') FROM revisions r;
INSERT INTO publication_fts(rowid,name,summary,body)
 SELECT r.seq,json_extract(r.body,'$.name'),COALESCE(json_extract(r.body,'$.summary'),''),COALESCE((SELECT group_concat(j.atom,' ') FROM json_tree(r.body,'$.payload') j WHERE j.type='text'),'') FROM publication_events r;
-- +goose StatementBegin
CREATE TRIGGER revision_fts_insert AFTER INSERT ON revisions BEGIN
 INSERT INTO revision_fts(rowid,name,summary,body) VALUES(new.seq,new.name,COALESCE(json_extract(new.body,'$.summary'),''),COALESCE((SELECT group_concat(j.atom,' ') FROM json_tree(new.body,'$.payload') j WHERE j.type='text'),''));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER revision_fts_update AFTER UPDATE OF body ON revisions BEGIN
 DELETE FROM revision_fts WHERE rowid=old.seq;
 INSERT INTO revision_fts(rowid,name,summary,body) VALUES(new.seq,new.name,COALESCE(json_extract(new.body,'$.summary'),''),COALESCE((SELECT group_concat(j.atom,' ') FROM json_tree(new.body,'$.payload') j WHERE j.type='text'),''));
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER revision_fts_delete AFTER DELETE ON revisions BEGIN
 DELETE FROM revision_fts WHERE rowid=old.seq;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER publication_fts_insert AFTER INSERT ON publication_events BEGIN
 INSERT INTO publication_fts(rowid,name,summary,body) VALUES(new.seq,json_extract(new.body,'$.name'),COALESCE(json_extract(new.body,'$.summary'),''),COALESCE((SELECT group_concat(j.atom,' ') FROM json_tree(new.body,'$.payload') j WHERE j.type='text'),''));
END;
-- +goose StatementEnd
ALTER TABLE receipts ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
ALTER TABLE publication_receipts ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
ALTER TABLE cursors ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
UPDATE receipts SET created_at=strftime('%Y-%m-%dT%H:%M:%fZ','now');
UPDATE publication_receipts SET created_at=strftime('%Y-%m-%dT%H:%M:%fZ','now');
UPDATE cursors SET created_at=strftime('%Y-%m-%dT%H:%M:%fZ','now');
-- +goose StatementBegin
CREATE TRIGGER receipts_timestamp AFTER INSERT ON receipts WHEN new.created_at='' BEGIN
 UPDATE receipts SET created_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE caller=new.caller AND authority=new.authority AND key=new.key;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER publication_receipts_timestamp AFTER INSERT ON publication_receipts WHEN new.created_at='' BEGIN
 UPDATE publication_receipts SET created_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE caller=new.caller AND authority=new.authority AND key=new.key;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER cursors_timestamp AFTER INSERT ON cursors WHEN new.created_at='' BEGIN
 UPDATE cursors SET created_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE token=new.token;
END;
-- +goose StatementEnd
CREATE TABLE kind_migrations (id TEXT PRIMARY KEY, record_id TEXT NOT NULL, from_schema TEXT NOT NULL, to_schema TEXT NOT NULL, from_revision TEXT NOT NULL, to_revision TEXT NOT NULL, actor TEXT NOT NULL, reason TEXT NOT NULL, migrated_at TEXT NOT NULL);
CREATE TABLE maintenance_log (id TEXT PRIMARY KEY, actor TEXT NOT NULL, operation TEXT NOT NULL, performed_at TEXT NOT NULL, body BLOB NOT NULL);
