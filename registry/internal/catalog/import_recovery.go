package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// ImportPreparationState recovers a caller's durable staging identity without
// resampling source or Registry baselines. Committed is observation, not part of
// the immutable preparation digest.
type ImportPreparationState struct {
	Plan      ImportPlan `json:"plan"`
	Committed bool       `json:"committed"`
}

func (s *Service) importPreparation(ctx context.Context, p libauth.Principal, a Args) (ImportPreparationState, error) {
	var state ImportPreparationState
	authority := s.authorities[p.ID]
	if authority == "" {
		return state, failure("forbidden", "authority", "configured owning authority required")
	}
	if strings.TrimSpace(a.IdempotencyKey) == "" {
		return state, failure("validation_error", "idempotency_key", "stable preparation key required")
	}
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `SELECT p.body,p.committed FROM import_prepare_keys k JOIN import_plans p ON p.id=k.plan_id WHERE k.caller=? AND k.authority=? AND k.key=?`, p.ID, authority, a.IdempotencyKey).Scan(&raw, &state.Committed)
	if errors.Is(err, sql.ErrNoRows) {
		return state, failure("not_found", "idempotency_key", "caller-owned preparation not found")
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(raw, &state.Plan)
	return state, err
}
