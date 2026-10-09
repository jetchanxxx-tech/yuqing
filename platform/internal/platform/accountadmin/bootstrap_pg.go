package accountadmin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/platform/auth"
)

// BootstrapPlatformAdmin is a privileged operator-only initialization operation.
// It is deliberately absent from Store and Service, so HTTP management keeps
// using Change and its authenticated actor guard. The caller must supply the
// verified immutable users.id, never an email or token claim. The returned bool
// is true only when this transaction grants the initial role.
func (s *PGStore) BootstrapPlatformAdmin(ctx context.Context, userID string) (bool, error) {
	if strings.TrimSpace(userID) == "" || userID != strings.TrimSpace(userID) {
		return false, missing()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, internal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auth.PlatformAdminLockID); err != nil {
		return false, internal(err)
	}
	var status string
	var rowVersion, tokenVersion int64
	err = tx.QueryRow(ctx, `SELECT status,row_version,token_version FROM users WHERE id=$1 FOR UPDATE`, userID).
		Scan(&status, &rowVersion, &tokenVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, missing()
	}
	if err != nil {
		return false, internal(err)
	}
	if status != "active" {
		return false, conflict()
	}
	var roleCount int
	var sameAdmin bool
	err = tx.QueryRow(ctx, `SELECT COUNT(*),COALESCE(bool_and(user_id=$1 AND role='platform_admin'),false) FROM platform_user_roles`, userID).
		Scan(&roleCount, &sameAdmin)
	if err != nil {
		return false, internal(err)
	}
	if roleCount != 0 {
		if roleCount != 1 || !sameAdmin {
			return false, conflict()
		}
		// A replay must not invalidate the account's current credentials or
		// produce a second audit. Commit the read-only transaction unchanged.
		if err = tx.Commit(ctx); err != nil {
			return false, internal(err)
		}
		return false, nil
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, userID); err != nil {
		return false, internal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET row_version=row_version+1,token_version=token_version+1 WHERE id=$1`, userID); err != nil {
		return false, internal(err)
	}
	details, err := json.Marshal(map[string]any{
		"target_type": "user", "target_id": userID, "source": "cli",
		"reason": "Initial platform administrator provisioned by verified immutable user ID",
		"request_id": id.New(),
		"before": map[string]any{"platform_roles": []string{}, "row_version": rowVersion, "token_version": tokenVersion},
		"after": map[string]any{"platform_roles": []string{"platform_admin"}, "row_version": rowVersion + 1, "token_version": tokenVersion + 1},
	})
	if err != nil {
		return false, internal(err)
	}
	// The CLI operator has no authenticated user identity. Do not attribute
	// the privileged action to the target account; record the CLI source.
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,tenant_id,action,resource,details_json)
		VALUES(NULL,NULL,'user.bootstrap_admin',$1,$2::jsonb)`, userID, details); err != nil {
		return false, internal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return false, internal(err)
	}
	return true, nil
}
