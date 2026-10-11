package settings

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/auth"
)

func settingsFailure() error {
	return pkgerrors.Wrap(pkgerrors.ErrInternal, "platform configuration unavailable")
}
func (s *PGStore) adminTransaction(ctx context.Context, id string, version int64, f func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return settingsFailure()
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auth.PlatformAdminLockID); err != nil {
		return settingsFailure()
	}
	var status string
	var current int64
	var admin bool
	err = tx.QueryRow(ctx, `SELECT u.status,u.token_version,EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=u.id AND role='platform_admin') FROM users u WHERE id=$1 FOR SHARE OF u`, id).Scan(&status, &current, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return pkgerrors.ErrConflict
	}
	if err != nil {
		return settingsFailure()
	}
	if status != "active" || current != version || !admin {
		return pkgerrors.ErrConflict
	}
	if err = f(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return settingsFailure()
	}
	return nil
}
func (s *PGStore) AdminAll(ctx context.Context, id string, version int64) (map[string]string, error) {
	out := map[string]string{}
	err := s.adminTransaction(ctx, id, version, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT key,value FROM platform_settings`)
		if err != nil {
			return settingsFailure()
		}
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) != nil {
				return settingsFailure()
			}
			out[k] = redactValue(k, v)
		}
		if rows.Err() != nil {
			return settingsFailure()
		}
		return nil
	})
	return out, err
}
func (s *PGStore) AdminPatch(ctx context.Context, id string, version int64, values map[string]string) error {
	return s.adminTransaction(ctx, id, version, func(tx pgx.Tx) error {
		for k, v := range values {
			var old string
			err := tx.QueryRow(ctx, `SELECT value FROM platform_settings WHERE key=$1`, k).Scan(&old)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return settingsFailure()
			}
			if _, err = tx.Exec(ctx, `INSERT INTO platform_settings(key,value,updated_at) VALUES($1,$2,now()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, k, preserveSecret(k, v, old)); err != nil {
				return settingsFailure()
			}
		}
		// Values and arbitrary caller-controlled key names cannot enter audit metadata.
		_, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,resource,details_json) VALUES($1,'settings.update','platform_settings',jsonb_build_object('changed_count',$2::int))`, id, len(values))
		if err != nil {
			return settingsFailure()
		}
		return nil
	})
}
