package accountadmin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/db"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/payment"
)

type PGStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

var _ Store = (*PGStore)(nil)

// lockAdministrator shares the K2 administration boundary and holds the
// original actor version/role through the transaction's commit.
func lockAdministrator(ctx context.Context, tx pgx.Tx, actorID string, version int64) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auth.PlatformAdminLockID); err != nil {
		return internal(err)
	}
	var status string
	var current int64
	var admin bool
	err := tx.QueryRow(ctx, `SELECT u.status,u.token_version,EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=u.id AND role='platform_admin') FROM users u WHERE id=$1 FOR SHARE OF u`, actorID).Scan(&status, &current, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return conflict()
	}
	if err != nil {
		return internal(err)
	}
	if status != "active" || version != current || !admin {
		return conflict()
	}
	return nil
}
func (s *PGStore) CreatePending(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if err := validateCreation(&req); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, internal(err)
	}
	defer tx.Rollback(context.Background())
	if err = lockAdministrator(ctx, tx, req.ActorID, req.ActorTokenVersion); err != nil {
		return nil, err
	}
	uid, tid := id.New(), id.New()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,email,password_hash,name,status) VALUES($1,$2,'!pending-activation',$3,'pending_activation')`, []any{uid, req.Email, req.Name}},
		{`INSERT INTO tenants(id,name,slug,db_name,status,plan_code) VALUES($1,$2,$3,$4,'active','free')`, []any{tid, req.TenantName, "t-" + strings.ToLower(tid), "yuqing_" + strings.ToLower(tid)}},
		{`INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,$2,'tenant_admin')`, []any{tid, uid}},
		{`INSERT INTO report_credits(tenant_id,balance,plan_code,version) VALUES($1,$2,'free',1)`, []any{tid, credit.TrialCredits}},
		{`INSERT INTO credit_transactions(id,tenant_id,delta,reason,reason_detail,actor_id,idempotency_key,balance_after,version) VALUES($1,$2,$3,'trial','administrator account creation',$4,$5,$3,1)`, []any{id.New(), tid, credit.TrialCredits, req.ActorID, "account-create:" + uid}},
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			if db.IsUniqueViolation(err) {
				return nil, pkgerrors.ErrConflict
			}
			return nil, internal(err)
		}
	}
	details, _ := json.Marshal(map[string]any{"target_type": "user", "target_id": uid, "reason": "administrator account creation", "request_id": req.RequestID, "before": map[string]any{}, "after": map[string]any{"status": "pending_activation", "tenant_id": tid, "trial_credits": credit.TrialCredits}})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(tenant_id,actor_id,action,resource,details_json) VALUES($1,$2,'account.create','user',$3::jsonb)`, tid, req.ActorID, details); err != nil {
		return nil, internal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, internal(err)
	}
	return &CreateResult{UserID: uid, TenantID: tid, Status: "pending_activation"}, nil
}
func (s *PGStore) StartNotification(ctx context.Context, actorID, targetID, purpose string, version int64) (*NotificationAttempt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, internal(err)
	}
	defer tx.Rollback(context.Background())
	if err = lockAdministrator(ctx, tx, actorID, version); err != nil {
		return nil, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR SHARE`, targetID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return nil, missing()
	} else if err != nil {
		return nil, internal(err)
	}
	if (purpose == auth.SetPassword && status != "pending_activation") || (purpose == auth.PasswordReset && status != "active") {
		return nil, conflict()
	}
	n := &NotificationAttempt{ID: id.New(), Purpose: purpose, State: "pending", CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if _, err = tx.Exec(ctx, `INSERT INTO account_notification_attempts(id,user_id,actor_id,actor_version,purpose,state,created_at) VALUES($1,$2,$3,$4,$5,'pending',$6)`, n.ID, targetID, actorID, version, purpose, n.CreatedAt); err != nil {
		return nil, internal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, internal(err)
	}
	return n, nil
}
func (s *PGStore) FinishNotification(ctx context.Context, attemptID, actorID string, version int64, state, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return internal(err)
	}
	defer tx.Rollback(context.Background())
	if err = lockAdministrator(ctx, tx, actorID, version); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE account_notification_attempts SET state=$4,error_code=$5 WHERE id=$1 AND actor_id=$2 AND actor_version=$3 AND state='pending'`, attemptID, actorID, version, state, code)
	if err != nil {
		return internal(err)
	}
	if tag.RowsAffected() != 1 {
		return conflict()
	}
	if err = tx.Commit(ctx); err != nil {
		return internal(err)
	}
	return nil
}

const userProjection = `u.id,COALESCE(u.name,''),u.email,COALESCE(u.phone,''),u.status,
	u.email_verified_at IS NOT NULL,u.phone_verified_at IS NOT NULL,
	ARRAY(SELECT role FROM platform_user_roles WHERE user_id=u.id ORDER BY role),
	(SELECT COUNT(*) FROM tenant_members WHERE user_id=u.id),u.created_at,u.last_login_at,u.row_version`
const userFilter = `WHERE ($1='' OR strpos(lower(u.id),lower($1))>0 OR strpos(lower(COALESCE(u.name,'')),lower($1))>0 OR strpos(lower(u.email::text),lower($1))>0)
	AND ($2='' OR u.status=$2)
	AND ($3='' OR EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=u.id AND role=$3))
	AND ($4='' OR ($4='email' AND u.email_verified_at IS NOT NULL) OR ($4='phone' AND u.phone_verified_at IS NOT NULL)
	OR ($4='none' AND u.email_verified_at IS NULL AND u.phone_verified_at IS NULL))
 AND ($5::timestamptz IS NULL OR u.created_at >= $5)
 AND ($6::timestamptz IS NULL OR u.created_at < $6)`

func scanUser(row pgx.Row) (UserRow, error) {
	u := UserRow{}
	var phone string
	err := row.Scan(&u.ID, &u.Name, &u.Email, &phone, &u.Status, &u.EmailVerified, &u.PhoneVerified, &u.PlatformRoles, &u.TenantCount, &u.CreatedAt, &u.LastLoginAt, &u.RowVersion)
	u.PhoneMasked = MaskPhone(phone)
	if u.PlatformRoles == nil {
		u.PlatformRoles = []string{}
	}
	return u, err
}

func (s *PGStore) ListUsers(ctx context.Context, q Query) ([]UserRow, int, error) {
	args := []any{q.Q, q.Status, q.PlatformRole, q.Verified, q.CreatedFrom, q.CreatedTo}
	total := 0
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users u `+userFilter, args...).Scan(&total); err != nil {
		return nil, 0, internal(err)
	}
	start, _ := pageBounds(total, q)
	args = append(args, q.PageSize, start)
	rows, err := s.pool.Query(ctx, `SELECT `+userProjection+` FROM users u `+userFilter+` ORDER BY u.created_at,u.id LIMIT $7 OFFSET $8`, args...)
	if err != nil {
		return nil, 0, internal(err)
	}
	defer rows.Close()
	items := []UserRow{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, internal(err)
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, internal(err)
	}
	return items, total, nil
}

func (s *PGStore) User(ctx context.Context, id string) (*UserDetail, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userProjection+` FROM users u WHERE u.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, missing()
	}
	if err != nil {
		return nil, internal(err)
	}
	result := &UserDetail{UserRow: u, Memberships: []Membership{}, Notifications: []NotificationAttempt{}}
	rows, err := s.pool.Query(ctx, `SELECT t.id,t.name,t.status,m.role,m.row_version FROM tenant_members m JOIN tenants t ON t.id=m.tenant_id
		WHERE m.user_id=$1 ORDER BY t.created_at,t.id`, id)
	if err != nil {
		return nil, internal(err)
	}
	for rows.Next() {
		m := Membership{}
		if err := rows.Scan(&m.TenantID, &m.TenantName, &m.TenantStatus, &m.Role, &m.RowVersion); err != nil {
			rows.Close()
			return nil, internal(err)
		}
		result.Memberships = append(result.Memberships, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, internal(err)
	}
	notificationRows, notificationErr := s.pool.Query(ctx, `SELECT id,purpose,state,error_code,created_at FROM account_notification_attempts WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT 20`, id)
	if notificationErr != nil {
		return nil, internal(notificationErr)
	}
	for notificationRows.Next() {
		var n NotificationAttempt
		if err := notificationRows.Scan(&n.ID, &n.Purpose, &n.State, &n.ErrorCode, &n.CreatedAt); err != nil {
			notificationRows.Close()
			return nil, internal(err)
		}
		result.Notifications = append(result.Notifications, n)
	}
	err = notificationRows.Err()
	notificationRows.Close()
	if err != nil {
		return nil, internal(err)
	}
	result.AuditLogs, err = s.auditRows(ctx, "user", id, "")
	if err != nil {
		return nil, err
	}
	return result, nil
}

const tenantProjection = `t.id,t.name,t.slug,t.plan_code,t.status,t.created_at,t.row_version,
	(SELECT COUNT(*) FROM tenant_members WHERE tenant_id=t.id),COALESCE(rc.plan_code,''),
	CASE WHEN rc.tenant_id IS NULL THEN 'unknown' ELSE 'report_credits' END`
const tenantJoin = ` FROM tenants t LEFT JOIN report_credits rc ON rc.tenant_id=t.id `
const tenantFilter = `WHERE ($1='' OR strpos(lower(t.id),lower($1))>0 OR strpos(lower(t.name),lower($1))>0)
	AND ($2='' OR t.status=$2) AND ($3='' OR rc.plan_code=$3)`

func scanTenant(row pgx.Row) (TenantRow, error) {
	t := TenantRow{}
	err := row.Scan(&t.ID, &t.Name, &t.Slug, &t.PlanCode, &t.Status, &t.CreatedAt, &t.RowVersion, &t.UserCount, &t.EffectivePlanCode, &t.PlanSource)
	return t, err
}

func (s *PGStore) ListTenants(ctx context.Context, q Query) ([]TenantRow, int, error) {
	args := []any{q.Q, q.Status, q.PlanCode}
	total := 0
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)`+tenantJoin+tenantFilter, args...).Scan(&total); err != nil {
		return nil, 0, internal(err)
	}
	start, _ := pageBounds(total, q)
	args = append(args, q.PageSize, start)
	rows, err := s.pool.Query(ctx, `SELECT `+tenantProjection+tenantJoin+tenantFilter+` ORDER BY t.created_at,t.id LIMIT $4 OFFSET $5`, args...)
	if err != nil {
		return nil, 0, internal(err)
	}
	defer rows.Close()
	items := []TenantRow{}
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, 0, internal(err)
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, internal(err)
	}
	return items, total, nil
}

func (s *PGStore) Tenant(ctx context.Context, id string) (*TenantDetail, error) {
	t, err := scanTenant(s.pool.QueryRow(ctx, `SELECT `+tenantProjection+tenantJoin+` WHERE t.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, missing()
	}
	if err != nil {
		return nil, internal(err)
	}
	result := &TenantDetail{TenantRow: t, Members: []TenantMember{}, Orders: []*payment.Order{}}
	rows, err := s.pool.Query(ctx, `SELECT u.id,COALESCE(u.name,''),u.email,m.role,m.row_version
		FROM tenant_members m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=$1 ORDER BY u.created_at,u.id`, id)
	if err != nil {
		return nil, internal(err)
	}
	for rows.Next() {
		m := TenantMember{}
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &m.RowVersion); err != nil {
			rows.Close()
			return nil, internal(err)
		}
		result.Members = append(result.Members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, internal(err)
	}
	result.Credit, err = credit.NewPGStore(s.pool).Snapshot(ctx, id)
	if err != nil {
		return nil, internal(err)
	}
	orders, err := payment.NewPGStore(s.pool).List(ctx, id, int(^uint(0)>>1))
	if err != nil {
		return nil, internal(err)
	}
	if orders != nil {
		result.Orders = orders
	}
	result.CreditTransactions, err = credit.NewPGStore(s.pool).Transactions(ctx, id, 50)
	if err != nil {
		return nil, internal(err)
	}
	if result.CreditTransactions == nil {
		result.CreditTransactions = []credit.Transaction{}
	}
	result.AuditLogs, err = s.auditRows(ctx, "tenant", id, id)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *PGStore) auditRows(ctx context.Context, targetType, targetID, tenantID string) ([]Audit, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,COALESCE(actor_id,''),action,COALESCE(tenant_id,''),details_json,created_at FROM audit_logs
		WHERE (details_json->>'target_type'=$1 AND details_json->>'target_id'=$2) OR ($3<>'' AND tenant_id=$3)
		ORDER BY created_at,id`, targetType, targetID, tenantID)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	result := []Audit{}
	for rows.Next() {
		a := Audit{}
		var details []byte
		if err := rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.TenantID, &details, &a.CreatedAt); err != nil {
			return nil, internal(err)
		}
		var d struct {
			TargetType string         `json:"target_type"`
			TargetID   string         `json:"target_id"`
			Reason     string         `json:"reason"`
			RequestID  string         `json:"request_id"`
			Before     map[string]any `json:"before"`
			After      map[string]any `json:"after"`
		}
		if err := json.Unmarshal(details, &d); err != nil {
			return nil, internal(err)
		}
		a.TargetType = d.TargetType
		a.TargetID = d.TargetID
		a.Reason = d.Reason
		a.RequestID = d.RequestID
		a.Before = d.Before
		a.After = d.After
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, internal(err)
	}
	return result, nil
}

// Change serializes all privileged mutations with the K1 provisioning lock.
// The order is platform advisory, optional tenant advisory, actor FOR SHARE,
// then target rows. The actor lock protects credentials through commit while
// remaining compatible with billing admission's shared actor reads. Global
// serialization avoids cross-actor lock upgrades/deadlocks at current scale.
// CAS, credentials, state and durable audit commit once.
func (s *PGStore) Change(ctx context.Context, m Mutation) (*Result, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, internal(err)
	}
	defer tx.Rollback(context.Background())
	platform := m.Action == "user.disable" || m.Action == "user.enable" || m.Action == "user.platform_role" || m.Action == "user.nickname"
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auth.PlatformAdminLockID); err != nil {
		return nil, internal(err)
	}
	if !platform {
		id := m.TenantID
		if m.Action != "member.role" {
			id = m.TargetID
		}
		_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,741915))`, id)
	}
	if err != nil {
		return nil, internal(err)
	}
	var actorStatus string
	var actorVersion int64
	var actorAdmin bool
	err = tx.QueryRow(ctx, `SELECT u.status,u.token_version,EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=u.id AND role='platform_admin')
		FROM users u WHERE u.id=$1 FOR SHARE OF u`, m.ActorID).Scan(&actorStatus, &actorVersion, &actorAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, conflict()
	}
	if err != nil {
		return nil, internal(err)
	}
	// Validate the request's original identity before any self-target version
	// changes. A request already queued when its actor is revoked conflicts;
	// a new request with the old JWT is rejected by authentication with 401.
	if actorStatus != "active" || actorVersion != m.ActorTokenVersion || !actorAdmin {
		return nil, conflict()
	}
	result := &Result{}
	before := map[string]any{}
	after := map[string]any{}
	targetType, tenantID := "user", m.TenantID
	switch m.Action {
	case "user.nickname":
		var name, status string
		var version int64
		if err = tx.QueryRow(ctx, `SELECT name,status,row_version FROM users WHERE id=$1 FOR UPDATE`, m.TargetID).Scan(&name, &status, &version); errors.Is(err, pgx.ErrNoRows) {
			return nil, missing()
		} else if err != nil {
			return nil, internal(err)
		}
		if version != m.ExpectedVersion || strings.TrimSpace(m.Name) == "" {
			return nil, conflict()
		}
		before = map[string]any{"name": name, "row_version": version}
		name = strings.TrimSpace(m.Name)
		after = map[string]any{"name": name, "row_version": version + 1}
		if _, err = tx.Exec(ctx, `UPDATE users SET name=$2,row_version=row_version+1 WHERE id=$1`, m.TargetID, name); err != nil {
			return nil, internal(err)
		}
		result.ID = m.TargetID
		result.Status = status
		result.RowVersion = version + 1
	case "user.disable", "user.enable", "user.platform_role":
		var status string
		var version int64
		var roles []string
		err = tx.QueryRow(ctx, `SELECT u.status,u.row_version,ARRAY(SELECT role FROM platform_user_roles WHERE user_id=u.id ORDER BY role)
			FROM users u WHERE u.id=$1 FOR UPDATE OF u`, m.TargetID).Scan(&status, &version, &roles)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, missing()
		}
		if err != nil {
			return nil, internal(err)
		}
		if version != m.ExpectedVersion {
			return nil, conflict()
		}
		result.ID = m.TargetID
		result.RowVersion = version + 1
		if m.Action == "user.platform_role" {
			if hasAdmin(roles) == m.PlatformAdmin {
				return nil, conflict()
			}
			before = map[string]any{"platform_roles": append([]string{}, roles...), "row_version": version}
			if m.PlatformAdmin {
				_, err = tx.Exec(ctx, `INSERT INTO platform_user_roles(user_id,role,granted_by) VALUES($1,'platform_admin',$2)`, m.TargetID, m.ActorID)
				roles = []string{"platform_admin"}
			} else {
				_, err = tx.Exec(ctx, `DELETE FROM platform_user_roles WHERE user_id=$1 AND role='platform_admin'`, m.TargetID)
				roles = []string{}
			}
			if err != nil {
				return nil, internal(err)
			}
			result.PlatformRoles = &roles
			after = map[string]any{"platform_roles": roles, "row_version": version + 1}
		} else {
			from, to := "active", "disabled"
			if m.Action == "user.enable" {
				from, to = "disabled", "active"
			}
			if status != from {
				return nil, conflict()
			}
			before = map[string]any{"status": status, "row_version": version}
			status = to
			after = map[string]any{"status": status, "row_version": version + 1}
			result.Status = status
		}
		_, err = tx.Exec(ctx, `UPDATE users SET status=$2,row_version=row_version+1,token_version=token_version+1 WHERE id=$1`, m.TargetID, status)
		if err != nil {
			return nil, internal(err)
		}
		var admins int
		if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM users u JOIN platform_user_roles r ON r.user_id=u.id WHERE u.status='active' AND r.role='platform_admin'`).Scan(&admins); err != nil {
			return nil, internal(err)
		}
		if admins < 1 {
			return nil, conflict()
		}
	case "member.role":
		var role string
		var version int64
		err = tx.QueryRow(ctx, `SELECT role,row_version FROM tenant_members WHERE tenant_id=$1 AND user_id=$2 FOR UPDATE`, m.TenantID, m.TargetID).Scan(&role, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, missing()
		}
		if err != nil {
			return nil, internal(err)
		}
		if version != m.ExpectedVersion || role == m.Role {
			return nil, conflict()
		}
		before = map[string]any{"role": role, "row_version": version}
		_, err = tx.Exec(ctx, `UPDATE tenant_members SET role=$3,row_version=row_version+1 WHERE tenant_id=$1 AND user_id=$2`, m.TenantID, m.TargetID, m.Role)
		if err != nil {
			return nil, internal(err)
		}
		var admins int
		if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM tenant_members WHERE tenant_id=$1 AND role='tenant_admin'`, m.TenantID).Scan(&admins); err != nil {
			return nil, internal(err)
		}
		if admins < 1 {
			return nil, conflict()
		}
		_, err = tx.Exec(ctx, `UPDATE users SET row_version=row_version+1,token_version=token_version+1 WHERE id=$1`, m.TargetID)
		if err != nil {
			return nil, internal(err)
		}
		after = map[string]any{"role": m.Role, "row_version": version + 1}
		result.TenantID = m.TenantID
		result.UserID = m.TargetID
		result.Role = m.Role
		result.RowVersion = version + 1
	case "tenant.suspend", "tenant.resume":
		targetType = "tenant"
		tenantID = m.TargetID
		var status string
		var version int64
		err = tx.QueryRow(ctx, `SELECT status,row_version FROM tenants WHERE id=$1 FOR UPDATE`, m.TargetID).Scan(&status, &version)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, missing()
		}
		if err != nil {
			return nil, internal(err)
		}
		from, to := "active", "suspended"
		if m.Action == "tenant.resume" {
			from, to = "suspended", "active"
		}
		if version != m.ExpectedVersion || status != from {
			return nil, conflict()
		}
		before = map[string]any{"status": status, "row_version": version}
		_, err = tx.Exec(ctx, `UPDATE tenants SET status=$2,row_version=row_version+1 WHERE id=$1`, m.TargetID, to)
		if err != nil {
			return nil, internal(err)
		}
		after = map[string]any{"status": to, "row_version": version + 1}
		result.ID = m.TargetID
		result.Status = to
		result.RowVersion = version + 1
	default:
		return nil, conflict()
	}
	details, err := json.Marshal(map[string]any{"target_type": targetType, "target_id": m.TargetID, "reason": m.Reason, "request_id": m.RequestID, "before": before, "after": after})
	if err != nil {
		return nil, internal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,tenant_id,action,resource,details_json) VALUES($1,NULLIF($2,''),$3,$4,$5::jsonb)`, m.ActorID, tenantID, m.Action, m.TargetID, details)
	if err != nil {
		return nil, internal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, internal(err)
	}
	return result, nil
}
