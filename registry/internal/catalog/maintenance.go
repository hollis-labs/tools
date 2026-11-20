package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
)

// Zero durations disable expiration. Policy is protected server configuration,
// never supplied by API callers. No background goroutine or job is installed.
type MaintenanceConfig struct {
	CursorTTL       time.Duration
	ReceiptTTL      time.Duration
	DraftHistoryTTL time.Duration
	BackupDirectory string
}
type RetentionResult struct {
	Applied             bool              `json:"applied"`
	Cursors             int64             `json:"cursors"`
	Receipts            int64             `json:"receipts"`
	PublicationReceipts int64             `json:"publication_receipts"`
	DraftRevisions      int64             `json:"draft_revisions"`
	HistoryDeferred     bool              `json:"history_deferred"`
	Policy              map[string]string `json:"policy"`
}

func (s *Service) maintenanceCall(ctx context.Context, p libauth.Principal, op string, a Args) (any, error) {
	if s.reviewers[p.ID] != "owner" {
		return nil, failure("forbidden", "reviewer", "maintenance requires configured owner review authority")
	}
	switch op {
	case "backup_create":
		dir := s.Maintenance.BackupDirectory
		if !filepath.IsAbs(dir) {
			return nil, failure("validation_error", "registry.maintenance.backup_directory", "an absolute operator-owned backup directory is required")
		}
		// The caller cannot choose a path or overwrite an existing file.
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, failure("unavailable", "backup", "backup directory unavailable")
		}
		dest := filepath.Join(dir, "registry-"+time.Now().UTC().Format("20060102T150405.000000000Z")+"-"+newID()+".db")
		result, err := s.DB.Backup(ctx, dest, store.BackupOptions{})
		if err != nil {
			return nil, failure("unavailable", "backup", "consistent verified backup failed")
		}
		// Record the result after the snapshot; the backup manifest is its durable
		// evidence even if this audit write fails. Never claim a rolled-back copy.
		raw, _ := json.Marshal(result)
		_, err = s.DB.ExecContext(ctx, `INSERT INTO maintenance_log(id,actor,operation,performed_at,body) VALUES(?,?,?,?,?)`, newID(), p.ID, op, time.Now().UTC().Format(time.RFC3339Nano), raw)
		return result, err
	default:
		return s.retain(ctx, p.ID, a.Apply)
	}
}

var previewRollback = errors.New("retention preview rollback")

func (s *Service) retain(ctx context.Context, actor string, apply bool) (RetentionResult, error) {
	cfg := s.Maintenance
	out := RetentionResult{Applied: apply, Policy: map[string]string{"cursor_ttl": cfg.CursorTTL.String(), "receipt_ttl": cfg.ReceiptTTL.String(), "draft_history_ttl": cfg.DraftHistoryTTL.String()}}
	if cfg.CursorTTL < 0 || cfg.ReceiptTTL < 0 || cfg.DraftHistoryTTL < 0 {
		return out, failure("validation_error", "retention", "negative retention durations refused")
	}
	now := time.Now().UTC()
	err := s.DB.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		prune := func(table string, ttl time.Duration, count *int64) error {
			if ttl == 0 {
				return nil
			}
			result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE julianday(created_at)<julianday(?)`, now.Add(-ttl).Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
			*count, err = result.RowsAffected()
			return err
		}
		if err := prune("cursors", cfg.CursorTTL, &out.Cursors); err != nil {
			return err
		}
		if err := prune("receipts", cfg.ReceiptTTL, &out.Receipts); err != nil {
			return err
		}
		if err := prune("publication_receipts", cfg.ReceiptTTL, &out.PublicationReceipts); err != nil {
			return err
		}
		var cursors int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cursors`).Scan(&cursors); err != nil {
			return err
		}
		out.HistoryDeferred = cfg.DraftHistoryTTL > 0 && cursors > 0
		if cfg.DraftHistoryTTL > 0 && cursors == 0 {
			result, err := tx.ExecContext(ctx, `DELETE FROM revisions WHERE
    julianday(json_extract(body,'$.updated_at'))<julianday(?)
    AND seq NOT IN (SELECT seq FROM records)
    AND id NOT IN (SELECT id FROM publication_events)
    AND id NOT IN (SELECT id FROM revisions WHERE COALESCE(json_extract(body,'$.review.reviewed_by'),'')!='')
    AND id NOT IN (SELECT r.id FROM revisions r,json_each(r.body,'$.edges') e
      WHERE COALESCE(json_extract(e.value,'$.reviewed_by'),'')!=''
         OR COALESCE(json_extract(e.value,'$.review_hash'),'')!='')
    AND token NOT IN (SELECT json_extract(body,'$.revision_id') FROM receipts)
    AND token NOT IN (SELECT json_extract(c.value,'$.revision') FROM publication_approvals a,json_each(a.body,'$.candidates') c)
    AND token NOT IN (SELECT revision FROM source_verifications)
    AND token NOT IN (SELECT from_revision FROM kind_migrations UNION SELECT to_revision FROM kind_migrations)
    AND token NOT IN (SELECT json_extract(e.value,'$.target_revision') FROM revisions r,json_each(r.body,'$.edges') e WHERE COALESCE(json_extract(e.value,'$.target_revision'),'')!='')`, now.Add(-cfg.DraftHistoryTTL).Format(time.RFC3339Nano))
			if err != nil {
				return err
			}
			out.DraftRevisions, err = result.RowsAffected()
			if err != nil {
				return err
			}
		}
		if !apply {
			return previewRollback
		}
		raw, _ := json.Marshal(out)
		_, err := tx.ExecContext(ctx, `INSERT INTO maintenance_log(id,actor,operation,performed_at,body) VALUES(?,?,?,?,?)`, newID(), actor, "retention_run", now.Format(time.RFC3339Nano), raw)
		return err
	})
	if errors.Is(err, previewRollback) {
		err = nil
	}
	return out, err
}
