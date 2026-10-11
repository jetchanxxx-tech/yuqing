package accountclosure

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/platform/billing"
)

type PGStore struct {
	pool    *pgxpool.Pool
	avatars AvatarLifecycle
}

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }
func begin(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(741914)`); err != nil {
		tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}
func lockUser(ctx context.Context, tx pgx.Tx, actor Actor, status string) error {
	var currentStatus, hash string
	var version int64
	err := tx.QueryRow(ctx, `SELECT status,password_hash,token_version FROM users WHERE id=$1 FOR UPDATE`, actor.UserID).Scan(&currentStatus, &hash, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return pkgerrors.ErrNotFound
	}
	if err != nil {
		return err
	}
	if currentStatus != status || version != actor.Version || actor.PasswordHash != "" && hash != actor.PasswordHash {
		return pkgerrors.ErrConflict
	}
	return nil
}
func preview(ctx context.Context, tx pgx.Tx, uid string, allowPending bool) (*Preview, error) {
	p := &Preview{WithdrawalDays: 7, Tenants: []TenantImpact{}, Blockers: []string{}}
	var platform bool
	var activeAdmins int
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=$1 AND role='platform_admin'),(SELECT count(*) FROM platform_user_roles r JOIN users u ON u.id=r.user_id WHERE r.role='platform_admin' AND u.status='active')`, uid).Scan(&platform, &activeAdmins); err != nil {
		return nil, err
	}
	if platform && activeAdmins < 2 && !allowPending {
		p.Blockers = append(p.Blockers, "LAST_PLATFORM_ADMIN")
	}
	rows, err := tx.Query(ctx, `SELECT t.id,t.name,t.status,tm.role,(SELECT count(*) FROM tenant_members WHERE tenant_id=t.id),CASE WHEN rc.tenant_id IS NULL THEN 'free' ELSE rc.plan_code END FROM tenant_members tm JOIN tenants t ON t.id=tm.tenant_id LEFT JOIN report_credits rc ON rc.tenant_id=t.id WHERE tm.user_id=$1 ORDER BY t.id`, uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t TenantImpact
		if err = rows.Scan(&t.ID, &t.Name, &t.Status, &t.Role, &t.Members, &t.PlanCode); err != nil {
			rows.Close()
			return nil, err
		}
		if plan := billing.DefaultPlans()[t.PlanCode]; plan != nil {
			days := plan.RetentionDays
			t.RetentionDays = &days
		}
		p.Tenants = append(p.Tenants, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, t := range p.Tenants {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,741915))`, t.ID); err != nil {
			return nil, err
		}
		if t.Members > 1 && t.Role == "tenant_admin" {
			var admins int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM tenant_members tm JOIN users u ON u.id=tm.user_id WHERE tm.tenant_id=$1 AND tm.role='tenant_admin' AND tm.user_id<>$2 AND u.status='active'`, t.ID, uid).Scan(&admins); err != nil {
				return nil, err
			}
			if admins == 0 {
				p.Blockers = append(p.Blockers, "TEAM_ADMIN_HANDOFF:"+t.ID)
			}
		}
		if t.Members == 1 {
			var orders, runs, invoices int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM orders WHERE tenant_id=$1 AND (state IN ('pending','refund_needed','refunding','disputed') OR state='paid' AND NOT granted)`, t.ID).Scan(&orders); err != nil {
				return nil, err
			}
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM analysis_runs r WHERE tenant_id=$1 AND (state NOT IN ('completed','failed','canceled') OR EXISTS(SELECT 1 FROM llm_call_authorizations c WHERE c.run_id=r.id AND NOT EXISTS(SELECT 1 FROM usage_events u WHERE u.call_id=c.call_id)))`, t.ID).Scan(&runs); err != nil {
				return nil, err
			}
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE tenant_id=$1 AND status IN ('unpaid','disputed','refund_pending')`, t.ID).Scan(&invoices); err != nil {
				return nil, err
			}
			if orders > 0 {
				p.Blockers = append(p.Blockers, "UNSETTLED_ORDERS:"+t.ID)
			}
			if runs > 0 {
				p.Blockers = append(p.Blockers, "UNSETTLED_TASKS:"+t.ID)
			}
			if invoices > 0 {
				p.Blockers = append(p.Blockers, "UNSETTLED_INVOICES:"+t.ID)
			}
		}
	}
	return p, nil
}
func (s *PGStore) Preview(ctx context.Context, uid string, version int64) (*Preview, error) {
	tx, err := begin(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if err = lockUser(ctx, tx, Actor{UserID: uid, Version: version}, "active"); err != nil {
		return nil, err
	}
	return preview(ctx, tx, uid, false)
}

const projection = `id,state,requested_at,withdraw_until,completed_at,cleanup_status,last_error,avatar_deleted,attempts`

func scan(row pgx.Row) (*Status, error) {
	r := &Status{}
	err := row.Scan(&r.ID, &r.State, &r.RequestedAt, &r.WithdrawUntil, &r.CompletedAt, &r.CleanupStatus, &r.LastError, &r.AvatarDeleted, &r.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.ErrNotFound
	}
	return r, err
}
func (s *PGStore) Status(ctx context.Context, uid string) (*Status, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+projection+` FROM account_closures WHERE user_id=$1 ORDER BY requested_at DESC,id DESC LIMIT 1`, uid))
}
func audit(ctx context.Context, tx pgx.Tx, actor Actor, action string) error {
	data, _ := json.Marshal(map[string]any{"target_type": "user", "target_id": actor.UserID, "reason": action, "request_id": actor.RequestID})
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,resource,details_json) VALUES($1,$2,$1,$3::jsonb)`, actor.UserID, action, data)
	return err
}
func (s *PGStore) Request(ctx context.Context, actor Actor, confirmed []string) (*Status, error) {
	tx, err := begin(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if err = lockUser(ctx, tx, actor, "active"); err != nil {
		return nil, err
	}
	p, err := preview(ctx, tx, actor.UserID, false)
	if err != nil {
		return nil, err
	}
	if len(p.Blockers) > 0 {
		return nil, pkgerrors.WithDetails(pkgerrors.Wrap(pkgerrors.ErrConflict, "account closure is blocked"), map[string]any{"blockers": p.Blockers})
	}
	sole := []string{}
	for _, t := range p.Tenants {
		if t.Members == 1 {
			sole = append(sole, t.ID)
		}
	}
	sort.Strings(confirmed)
	if len(sole) != len(confirmed) {
		return nil, pkgerrors.ErrConflict
	}
	for i := range sole {
		if sole[i] != confirmed[i] {
			return nil, pkgerrors.ErrConflict
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	r := &Status{ID: id.New(), State: "pending", RequestedAt: now, WithdrawUntil: now.Add(168 * time.Hour), CleanupStatus: "pending"}
	if _, err = tx.Exec(ctx, `INSERT INTO account_closures(id,user_id,state,requested_at,withdraw_until,actor_version,avatar_ref) SELECT $1,id,'pending',$3,$4,token_version+1,COALESCE(avatar_url,'') FROM users WHERE id=$2`, r.ID, actor.UserID, now, r.WithdrawUntil); err != nil {
		return nil, err
	}
	for _, t := range p.Tenants {
		if _, err = tx.Exec(ctx, `INSERT INTO account_closure_tenants(closure_id,tenant_id,sole_member,original_status,plan_code,retention_days) VALUES($1,$2,$3,$4,$5,$6)`, r.ID, t.ID, t.Members == 1, t.Status, t.PlanCode, t.RetentionDays); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET status='closure_pending',token_version=token_version+1,row_version=row_version+1 WHERE id=$1`, actor.UserID); err != nil {
		return nil, err
	}
	for _, q := range []string{`UPDATE verification_tokens SET used_at=COALESCE(used_at,now()) WHERE user_id=$1 OR issuer_user_id=$1`, `UPDATE sms_verification_codes SET used_at=COALESCE(used_at,now()) WHERE user_id=$1 OR issuer_user_id=$1`} {
		if _, err = tx.Exec(ctx, q, actor.UserID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE verification_tokens SET notice_state='cancelled',notice_target='',notice_next_attempt=NULL WHERE (user_id=$1 OR issuer_user_id=$1) AND notice_state IN ('pending','failed')`, actor.UserID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE api_keys SET revoked_at=COALESCE(revoked_at,now()) WHERE creator_user_id=$1 OR tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$2 AND sole_member)`, actor.UserID, r.ID); err != nil {
		return nil, err
	}
	if err = audit(ctx, tx, actor, "account.closure.request"); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r, nil
}
func (s *PGStore) Cancel(ctx context.Context, actor Actor) (*Status, error) {
	tx, err := begin(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if err = lockUser(ctx, tx, actor, "closure_pending"); err != nil {
		return nil, err
	}
	r, err := scan(tx.QueryRow(ctx, `SELECT `+projection+` FROM account_closures WHERE user_id=$1 AND state='pending' FOR UPDATE`, actor.UserID))
	if err != nil {
		return nil, err
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT now()<$1`, r.WithdrawUntil).Scan(&allowed); err != nil {
		return nil, err
	}
	if !allowed {
		return nil, pkgerrors.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET status='active',token_version=token_version+1,row_version=row_version+1 WHERE id=$1`, actor.UserID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_closures SET state='cancelled',cleanup_status='cancelled' WHERE id=$1`, r.ID); err != nil {
		return nil, err
	}
	if err = audit(ctx, tx, actor, "account.closure.cancel"); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	r.State = "cancelled"
	r.CleanupStatus = "cancelled"
	return r, nil
}
