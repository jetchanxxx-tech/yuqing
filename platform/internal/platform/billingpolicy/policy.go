// Package billingpolicy binds the sole billing exemption to an immutable user ID.
package billingpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/platform/billing"
)

const PolicyKey = "fixed_admin_v1"
const CatalogRevision = "report-credit-plan-b-2026-09-15"

// This lock is shared with auth/accountadmin's exclusive administration lock.
const administrationLockID int64 = 741914

type Actor struct {
	UserID   string
	APIKeyID string
	// Non-nil only for a request authenticated by JWT. Rechecked under row lock.
	TokenVersion *int64
	Permission   string
}
type Decision struct {
	Exempt          bool
	PolicyKey       string
	PolicyVersion   int64
	PlanCode        string
	CatalogRevision string
}
type Policy interface {
	ResolveTx(context.Context, pgx.Tx, string, Actor) (Decision, error)
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// CheckActorTx serializes against account and membership administration before
// taking tenant, actor and membership shared locks. Callers next lock analysis,
// then credits. Shared global locking prevents cross-target admin deadlocks.
func CheckActorTx(ctx context.Context, tx pgx.Tx, tenantID string, actor Actor) error {
	if actor.UserID == "" {
		return pkgerrors.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, administrationLockID); err != nil {
		return err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1 FOR SHARE`, tenantID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pkgerrors.ErrForbidden
		}
		return err
	}
	if status != "active" {
		return pkgerrors.ErrTenantSuspended
	}
	var version int64
	if err := tx.QueryRow(ctx, `SELECT status,token_version FROM users WHERE id=$1 FOR SHARE`, actor.UserID).Scan(&status, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pkgerrors.ErrForbidden
		}
		return err
	}
	if status != "active" || actor.TokenVersion != nil && *actor.TokenVersion != version {
		return pkgerrors.ErrForbidden
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM tenant_members WHERE tenant_id=$1 AND user_id=$2 FOR SHARE`, tenantID, actor.UserID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pkgerrors.ErrForbidden
		}
		return err
	}
	if actor.APIKeyID != "" {
		var owner string
		var scopes []byte
		var revoked bool
		err := tx.QueryRow(ctx, `SELECT COALESCE(creator_user_id,''),COALESCE(scopes,'[]'),revoked_at IS NOT NULL FROM api_keys WHERE id=$1 AND tenant_id=$2 FOR SHARE`, actor.APIKeyID, tenantID).Scan(&owner, &scopes, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return pkgerrors.ErrForbidden
		}
		if err != nil {
			return err
		}
		if owner != actor.UserID || revoked {
			return pkgerrors.ErrForbidden
		}
		var allowed []string
		if err := json.Unmarshal(scopes, &allowed); err != nil {
			return err
		}
		if actor.Permission != "" && len(allowed) > 0 {
			found := false
			for _, p := range allowed {
				if p == actor.Permission {
					found = true
				}
			}
			if !found {
				return pkgerrors.ErrForbidden
			}
		}
	} else if actor.Permission != "" {
		var admin bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=$1 AND role='platform_admin')`, actor.UserID).Scan(&admin); err != nil {
			return err
		}
		if !admin && role != "tenant_admin" && (role != "analyst" || actor.Permission != "analyses:create") {
			return pkgerrors.ErrForbidden
		}
	}
	return nil
}
func (s *Service) ResolveTx(ctx context.Context, tx pgx.Tx, tenantID string, actor Actor) (Decision, error) {
	if err := CheckActorTx(ctx, tx, tenantID, actor); err != nil {
		return Decision{}, err
	}
	d := Decision{PlanCode: "free", CatalogRevision: CatalogRevision}
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT NULLIF(plan_code,'') FROM report_credits WHERE tenant_id=$1),'free')`, tenantID).Scan(&d.PlanCode); err != nil {
		return d, err
	}
	if billing.DefaultPlans()[d.PlanCode] == nil {
		return d, pkgerrors.ErrForbidden
	}
	err := tx.QueryRow(ctx, `SELECT policy_key,policy_version FROM billing_exempt_principals WHERE user_id=$1`, actor.UserID).Scan(&d.PolicyKey, &d.PolicyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	d.Exempt = true
	return d, nil
}
func (s *Service) IsExempt(ctx context.Context, userID string) (bool, error) {
	var exempt bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM billing_exempt_principals WHERE user_id=$1)`, userID).Scan(&exempt)
	return exempt, err
}

// Bind verifies the initial account directly in PostgreSQL. An existing same-ID
// binding is idempotent even after email changes; email never transfers a policy.
func (s *Service) Bind(ctx context.Context, expected string, apply bool) (bool, error) {
	if expected == "" || strings.TrimSpace(expected) != expected {
		return false, pkgerrors.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, administrationLockID); err != nil {
		return false, err
	}
	var bound string
	err = tx.QueryRow(ctx, `SELECT user_id FROM billing_exempt_principals WHERE policy_key=$1`, PolicyKey).Scan(&bound)
	if err == nil {
		if bound != expected {
			return false, pkgerrors.ErrConflict
		}
		return false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	var id, status string
	var admin bool
	err = tx.QueryRow(ctx, `SELECT u.id,u.status,EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=u.id AND role='platform_admin') FROM users u WHERE email='admin@pangu.com' FOR SHARE OF u`).Scan(&id, &status, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, pkgerrors.ErrForbidden
	}
	if err != nil {
		return false, err
	}
	if id != expected || status != "active" || !admin {
		return false, pkgerrors.ErrForbidden
	}
	if !apply {
		return false, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO billing_exempt_principals(policy_key,user_id,bound_by) VALUES($1,$2,'verified-cli')`, PolicyKey, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,resource,details_json) VALUES($1,'billing.exempt.bind',$2,jsonb_build_object('user_id',$1::text,'policy_version',1,'source','verified-cli'))`, id, PolicyKey); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
