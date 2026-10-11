package accountnotify

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/notification"
	"github.com/yuqing/platform/internal/platform/auth"
)

type PGStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

const optedIn = `COALESCE(jsonb_typeof(notification_prefs->'task_completed')='boolean' AND notification_prefs->>'task_completed'='true',false)`

func storageError(error) error { return pkgerrors.ErrServiceUnavailable }

func (s *PGStore) Preferences(ctx context.Context, actor auth.Principal) (Preferences, error) {
	var prefs Preferences
	err := s.pool.QueryRow(ctx, `SELECT `+optedIn+` FROM users WHERE id=$1 AND status='active' AND token_version=$2`, actor.UserID, actor.TokenVersion).Scan(&prefs.TaskCompleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return prefs, pkgerrors.ErrConflict
	}
	if err != nil {
		return prefs, storageError(err)
	}
	return prefs, nil
}
func (s *PGStore) SetPreferences(ctx context.Context, actor auth.Principal, prefs Preferences) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET notification_prefs=jsonb_set(notification_prefs,'{task_completed}',to_jsonb($3::bool),true),row_version=row_version+1 WHERE id=$1 AND status='active' AND token_version=$2`, actor.UserID, actor.TokenVersion, prefs.TaskCompleted)
	if err != nil {
		return storageError(err)
	}
	if tag.RowsAffected() != 1 {
		return pkgerrors.ErrConflict
	}
	return nil
}

func (s *PGStore) Claim(ctx context.Context) (*Intent, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, storageError(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, auth.PlatformAdminLockID); err != nil {
		return nil, storageError(err)
	}
	var eid, uid string
	// Unfinished inactive/invalidated attempts require reconciliation; an expired
	// lease is not evidence their copied recipient is no longer in flight.
	err = tx.QueryRow(ctx, `SELECT n.id,n.user_id FROM account_notification_events n JOIN users u ON u.id=n.user_id
 WHERE n.next_attempt_at<=now() AND (n.state IN ('pending','failed') OR n.state='processing' AND u.status='active' AND u.email_verified_at IS NOT NULL AND n.issued_epoch=u.notification_epoch AND COALESCE(jsonb_typeof(u.notification_prefs->'task_completed')='boolean' AND u.notification_prefs->>'task_completed'='true',false)
 AND EXISTS(SELECT 1 FROM tenants t JOIN tenant_members m ON m.tenant_id=t.id WHERE t.id=n.tenant_id AND t.status='active' AND m.user_id=n.user_id))
 ORDER BY n.next_attempt_at,n.id LIMIT 1`).Scan(&eid, &uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, storageError(err)
	}
	var status, email string
	var verified, pref bool
	var epoch int64
	if err = tx.QueryRow(ctx, `SELECT status,email,email_verified_at IS NOT NULL,`+optedIn+`,notification_epoch FROM users WHERE id=$1 FOR SHARE`, uid).Scan(&status, &email, &verified, &pref, &epoch); err != nil {
		return nil, storageError(err)
	}
	var n Intent
	var state, tid string
	var issued int64
	var due bool
	err = tx.QueryRow(ctx, `SELECT id,state,tenant_id,issued_epoch,next_attempt_at<=now() FROM account_notification_events WHERE id=$1 FOR UPDATE SKIP LOCKED`, eid).Scan(&n.ID, &state, &tid, &issued, &due)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, storageError(err)
	}
	if !due || (state != "pending" && state != "failed" && state != "processing") {
		return nil, nil
	}
	var tenantStatus string
	var member bool
	if err = tx.QueryRow(ctx, `SELECT status,EXISTS(SELECT 1 FROM tenant_members WHERE tenant_id=$1 AND user_id=$2) FROM tenants WHERE id=$1 FOR SHARE`, tid, uid).Scan(&tenantStatus, &member); err != nil {
		return nil, storageError(err)
	}
	if status != "active" || !verified || !pref || issued != epoch || tenantStatus != "active" || !member {
		if state == "processing" {
			return nil, nil
		}
		if _, err = tx.Exec(ctx, `UPDATE account_notification_events SET state='cancelled',next_attempt_at=NULL WHERE id=$1 AND state IN ('pending','failed')`, eid); err != nil {
			return nil, storageError(err)
		}
		n.Cancelled = true
	} else {
		if err = tx.QueryRow(ctx, `UPDATE account_notification_events SET state='processing',attempts=attempts+1,next_attempt_at=now()+interval '2 minutes' WHERE id=$1 RETURNING attempts`, eid).Scan(&n.Attempt); err != nil {
			return nil, storageError(err)
		}
		n.Recipient = email
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, storageError(err)
	}
	return &n, nil
}

func (s *PGStore) Complete(ctx context.Context, n Intent, receipt notification.Receipt) error {
	r, err := notification.Normalize(receipt, "task_completed")
	if err != nil {
		return pkgerrors.ErrBadRequest
	}
	data, err := json.Marshal(r)
	if err != nil {
		return pkgerrors.ErrBadRequest
	}
	tag, err := s.pool.Exec(ctx, `UPDATE account_notification_events SET state=CASE WHEN $3 THEN 'accepted' ELSE 'failed' END,receipt=$4,next_attempt_at=CASE WHEN $3 THEN NULL ELSE now()+interval '1 minute' END WHERE id=$1 AND state='processing' AND attempts=$2`, n.ID, n.Attempt, r.State == "accepted", data)
	if err != nil {
		return storageError(err)
	}
	if tag.RowsAffected() != 1 {
		return pkgerrors.ErrConflict
	}
	return nil
}
