package accountclosure

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/storage"
)

func (s *PGStore) SetAvatarStorage(port AvatarLifecycle) { s.avatars = port }

// The per-user session lock serializes resumable steps across CLI processes.
// The platform/user/tenant transaction boundary is released before filesystem
// work, so one user's slow disk cannot hold administration's global lock.
func (s *PGStore) Process(ctx context.Context, uid string, limit int) (*Status, error) {
	if uid == "" || limit < 1 || limit > 1000 {
		return nil, pkgerrors.ErrBadRequest
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,741916))`, uid).Scan(&locked); err != nil {
		return nil, err
	}
	if !locked {
		return s.Status(ctx, uid)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		if e := conn.QueryRow(c, `SELECT pg_advisory_unlock(hashtextextended($1,741916))`, uid).Scan(&released); e != nil || !released {
			_ = conn.Conn().Close(c)
		}
	}()
	r, cursor, err := s.claim(ctx, conn, uid)
	if err != nil {
		return nil, err
	}
	if r.State == "pending" || r.State == "cancelled" {
		return r, nil
	}
	if r.State == "completed" {
		return s.cleanupBusiness(ctx, r, limit)
	}
	if r.LastError != "" {
		return r, nil
	}
	if s.avatars == nil {
		return s.failStep(ctx, r, "AVATAR_STORAGE_UNAVAILABLE")
	}
	if err = s.avatars.Seal(ctx, uid); err != nil {
		return s.failStep(ctx, r, "AVATAR_CLEANUP_FAILED")
	}
	next, done, n, err := s.avatars.Reconcile(ctx, uid, cursor, limit)
	if err != nil {
		return s.failStep(ctx, r, "AVATAR_CLEANUP_FAILED")
	}
	_, err = s.pool.Exec(ctx, `UPDATE account_closures SET cleanup_cursor=$2,avatar_deleted=avatar_deleted+$3,last_error='',cleanup_status=$4 WHERE id=$1 AND state='finalizing'`, r.ID, next, n, map[bool]string{true: "anonymizing", false: "avatars"}[done])
	if err != nil {
		return nil, err
	}
	if !done {
		return s.Status(ctx, uid)
	}
	if err = s.anonymize(ctx, uid, r.ID); err != nil {
		return s.failStep(ctx, r, "ANONYMIZATION_FAILED")
	}
	r, err = s.Status(ctx, uid)
	if err != nil {
		return nil, err
	}
	return s.cleanupBusiness(ctx, r, limit)
}

func (s *PGStore) failStep(ctx context.Context, r *Status, code string) (*Status, error) {
	_, err := s.pool.Exec(ctx, `UPDATE account_closures SET last_error=$2 WHERE id=$1`, r.ID, code)
	if err != nil {
		return nil, err
	}
	r.LastError = code
	return r, nil
}

func (s *PGStore) claim(ctx context.Context, conn interface {
	Begin(context.Context) (pgx.Tx, error)
}, uid string) (*Status, int64, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(741914)`); err != nil {
		return nil, 0, err
	}
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&status); err != nil {
		return nil, 0, err
	}
	r, err := scan(tx.QueryRow(ctx, `SELECT `+projection+` FROM account_closures WHERE user_id=$1 ORDER BY requested_at DESC,id DESC LIMIT 1 FOR UPDATE`, uid))
	if err != nil {
		return nil, 0, err
	}
	var cursor int64
	if err = tx.QueryRow(ctx, `SELECT cleanup_cursor,avatar_deleted,attempts FROM account_closures WHERE id=$1`, r.ID).Scan(&cursor, &r.AvatarDeleted, &r.Attempts); err != nil {
		return nil, 0, err
	}
	if r.State == "completed" || r.State == "cancelled" {
		return r, cursor, nil
	}
	if status != "closure_pending" {
		return nil, 0, pkgerrors.ErrConflict
	}
	var due bool
	if err = tx.QueryRow(ctx, `SELECT now()>=$1`, r.WithdrawUntil).Scan(&due); err != nil {
		return nil, 0, err
	}
	if !due {
		return r, cursor, nil
	}
	p, err := preview(ctx, tx, uid, true)
	if err != nil {
		return nil, 0, err
	}
	code := ""
	if len(p.Blockers) > 0 {
		code = strings.Split(p.Blockers[0], ":")[0]
	}
	// Confirmed team scope must still match current members. A later membership
	// change cannot turn someone's shared assets into a sole-team deletion.
	var changed, unknown int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM account_closure_tenants c WHERE closure_id=$1 AND (sole_member IS DISTINCT FROM ((SELECT count(*) FROM tenant_members WHERE tenant_id=c.tenant_id)=1) OR NOT EXISTS(SELECT 1 FROM tenant_members WHERE tenant_id=c.tenant_id AND user_id=$2))`, r.ID, uid).Scan(&changed); err != nil {
		return nil, 0, err
	}
	if changed > 0 {
		code = "TEAM_IMPACT_CHANGED"
	}
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM account_notification_attempts WHERE (user_id=$1 OR actor_id=$1) AND state='pending')+(SELECT count(*) FROM verification_tokens WHERE (user_id=$1 OR issuer_user_id=$1) AND notice_state='processing')`, uid).Scan(&unknown); err != nil {
		return nil, 0, err
	}
	if unknown > 0 {
		code = "NOTIFICATION_DELIVERY_UNCONFIRMED"
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$1 AND sole_member) AND COALESCE(pdf_key,'')<>''`, r.ID).Scan(&unknown); err != nil {
		return nil, 0, err
	}
	if unknown > 0 {
		code = "FINANCIAL_ARTIFACT_REVIEW_REQUIRED"
	}
	if code != "" {
		if _, err = tx.Exec(ctx, `UPDATE account_closures SET last_error=$2,attempts=attempts+1,last_attempt_at=now() WHERE id=$1`, r.ID, code); err != nil {
			return nil, 0, err
		}
		r.LastError = code
		return r, cursor, tx.Commit(ctx)
	}
	var ref string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(avatar_url,'') FROM users WHERE id=$1`, uid).Scan(&ref); err != nil {
		return nil, 0, err
	}
	if ref != "" && !storage.OwnsAvatarReference(uid, ref) {
		if _, err = tx.Exec(ctx, `UPDATE account_closures SET last_error='AVATAR_REFERENCE_UNVERIFIED' WHERE id=$1`, r.ID); err != nil {
			return nil, 0, err
		}
		r.LastError = "AVATAR_REFERENCE_UNVERIFIED"
		return r, cursor, tx.Commit(ctx)
	}
	if r.State == "pending" {
		if _, err = tx.Exec(ctx, `UPDATE users SET token_version=token_version+1,row_version=row_version+1 WHERE id=$1`, uid); err != nil {
			return nil, 0, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE account_closures SET state='finalizing',cleanup_status='avatars',last_error='',attempts=attempts+1,last_attempt_at=now(),avatar_ref=$2 WHERE id=$1`, r.ID, ref); err != nil {
		return nil, 0, err
	}
	r.State = "finalizing"
	r.CleanupStatus = "avatars"
	r.LastError = ""
	return r, cursor, tx.Commit(ctx)
}

// Keep only typed financial facts in audit JSON. Free-form reasons, names,
// arbitrary strings and request metadata cannot carry personal identifiers.
func scrubAudit(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			switch x.(type) {
			case float64, bool, nil:
				out[k] = x
			case map[string]any, []any:
				out[k] = scrubAudit(x)
			}
		}
		out["anonymized"] = true
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, x := range v {
			switch x.(type) {
			case float64, bool, nil, map[string]any, []any:
				out = append(out, scrubAudit(x))
			}
		}
		return out
	case float64, bool, nil:
		return v
	}
	return nil
}

func (s *PGStore) anonymize(ctx context.Context, uid, cid string) error {
	tx, err := begin(ctx, s.pool)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, uid).Scan(&status); err != nil {
		return err
	}
	if status != "closure_pending" {
		return pkgerrors.ErrConflict
	}
	p, err := preview(ctx, tx, uid, true)
	if err != nil {
		return err
	}
	if len(p.Blockers) > 0 {
		return pkgerrors.ErrConflict
	}
	// Recheck dispatch finality while locking the exact credential rows. Once
	// cancelled, the notice claim predicate cannot reintroduce an old recipient.
	var inflight int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM verification_tokens WHERE (user_id=$1 OR issuer_user_id=$1) AND notice_state='processing'`, uid).Scan(&inflight); err != nil {
		return err
	}
	if inflight > 0 {
		return pkgerrors.ErrConflict
	}
	for _, q := range []string{
		`UPDATE verification_tokens SET used_at=COALESCE(used_at,now()),target='',token_hash=NULL,notice_target='',notice_state=CASE WHEN notice_state='accepted' THEN 'accepted' ELSE 'cancelled' END,notice_next_attempt=NULL WHERE user_id=$1 OR issuer_user_id=$1`,
		`UPDATE sms_verification_codes SET used_at=COALESCE(used_at,now()),phone='anonymized:'||id,code_hash=NULL WHERE user_id=$1 OR issuer_user_id=$1`,
		`DELETE FROM login_sessions WHERE user_id=$1`,
		`UPDATE api_keys SET name='已注销账号密钥',key_hash='anonymized:'||id,prefix='',revoked_at=COALESCE(revoked_at,now()) WHERE creator_user_id=$1`,
		`DELETE FROM platform_user_roles WHERE user_id=$1`,
	} {
		if _, err = tx.Exec(ctx, q, uid); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE credit_transactions SET reason_detail='已注销账号（原因脱敏）',idempotency_key=CASE WHEN idempotency_key IS NULL THEN NULL ELSE 'anonymized:'||id END WHERE run_id IS NULL AND (actor_id=$1 OR tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$2 AND sole_member))`, uid, cid); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id,details_json FROM audit_logs WHERE actor_id=$1 OR resource=$1 OR details_json->>'target_id'=$1 OR tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$2 AND sole_member) ORDER BY id`, uid, cid)
	if err != nil {
		return err
	}
	type auditRow struct {
		id   int64
		data []byte
	}
	var audits []auditRow
	for rows.Next() {
		var a auditRow
		if err = rows.Scan(&a.id, &a.data); err != nil {
			rows.Close()
			return err
		}
		audits = append(audits, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range audits {
		var v any
		if len(a.data) > 0 {
			if err = json.Unmarshal(a.data, &v); err != nil {
				return err
			}
		}
		data, _ := json.Marshal(scrubAudit(v))
		if _, err = tx.Exec(ctx, `UPDATE audit_logs SET details_json=$2::jsonb,resource='anonymized' WHERE id=$1`, a.id, data); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE tenants SET status='closed',name='已注销团队',slug='closed-'||id,settings_json='{}',row_version=row_version+1 WHERE id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$1 AND sole_member)`, cid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET qr_code_url=NULL WHERE tenant_id IN (SELECT tenant_id FROM account_closure_tenants WHERE closure_id=$1 AND sole_member)`, cid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM tenant_members WHERE user_id=$1`, uid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET email=$2,name='已注销用户',password_hash='',phone=NULL,avatar_url=NULL,email_verified_at=NULL,phone_verified_at=NULL,last_login_at=NULL,password_changed_at=NULL,timezone='Asia/Shanghai',notification_prefs='{}',status='closed',closed_at=now(),pii_anonymized_at=now(),token_version=token_version+1,row_version=row_version+1 WHERE id=$1`, uid, id.New()+".closed@invalid"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE account_closures SET state='completed',completed_at=now(),avatar_ref='',cleanup_status='retention_pending',last_error='' WHERE id=$1 AND state='finalizing'`, cid); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Business records are governed by their original creation time and the
// confirmed catalog retention snapshot. Analysis IDs survive as tombstones
// because immutable usage/run/credit facts reference them. Unsupported file
// adapters remain visible pending work and never cause arbitrary file deletion.
func (s *PGStore) cleanupBusiness(ctx context.Context, r *Status, limit int) (*Status, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT tenant_id,retention_days FROM account_closure_tenants WHERE closure_id=$1 AND sole_member ORDER BY tenant_id FOR UPDATE`, r.ID)
	if err != nil {
		return nil, err
	}
	type impact struct {
		id   string
		days *int
	}
	var impacts []impact
	for rows.Next() {
		var t impact
		if err = rows.Scan(&t.id, &t.days); err != nil {
			rows.Close()
			return nil, err
		}
		impacts = append(impacts, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	allDone := true
	lastError := ""
	for _, t := range impacts {
		state := "retention_pending"
		code := ""
		if t.days == nil || *t.days < 1 {
			code = "RETENTION_POLICY_UNVERIFIED"
		} else {
			cutoff := time.Now().UTC().Add(-time.Duration(*t.days) * 24 * time.Hour)
			var external int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM reports WHERE tenant_id=$1 AND (format<>'html' OR file_key NOT IN ('','reports/'||id||'.html'))`, t.id).Scan(&external); err != nil {
				return nil, err
			}
			if external > 0 {
				code = "EXTERNAL_ARTIFACT_REVIEW_REQUIRED"
			} else {
				if _, err = tx.Exec(ctx, `DELETE FROM reports WHERE tenant_id=$1 AND id IN (SELECT id FROM reports WHERE tenant_id=$1 AND created_at<=$2 ORDER BY created_at,id LIMIT $3)`, t.id, cutoff, limit); err != nil {
					return nil, err
				}
				if _, err = tx.Exec(ctx, `DELETE FROM raw_documents WHERE (tenant_id,analysis_id,id) IN (SELECT d.tenant_id,d.analysis_id,d.id FROM raw_documents d JOIN analyses a ON a.id=d.analysis_id AND a.tenant_id=d.tenant_id WHERE d.tenant_id=$1 AND a.created_at<=$2 ORDER BY a.created_at,d.analysis_id,d.id LIMIT $3)`, t.id, cutoff, limit); err != nil {
					return nil, err
				}
				if _, err = tx.Exec(ctx, `UPDATE analyses SET name='已回收分析',summary='',warning='',report_content='',keywords='[]',sources='[]',sentiments='[]',topics='[]',dimensions='[]',exclude_words='null',date_from='',date_to='',retrieval_coverage=NULL,report_id='',report_template_id='',closure_purged_at=now() WHERE tenant_id=$1 AND id IN (SELECT a.id FROM analyses a WHERE a.tenant_id=$1 AND a.created_at<=$2 AND a.closure_purged_at IS NULL AND NOT EXISTS(SELECT 1 FROM raw_documents d WHERE d.tenant_id=$1 AND d.analysis_id=a.id) AND NOT EXISTS(SELECT 1 FROM reports r WHERE r.tenant_id=$1 AND r.analysis_id=a.id) ORDER BY a.created_at,a.id LIMIT $3)`, t.id, cutoff, limit); err != nil {
					return nil, err
				}
				if _, err = tx.Exec(ctx, `DELETE FROM monitor_plans WHERE tenant_id=$1 AND id IN (SELECT id FROM monitor_plans WHERE tenant_id=$1 AND created_at<=$2 ORDER BY created_at,id LIMIT $3)`, t.id, cutoff, limit); err != nil {
					return nil, err
				}
				if _, err = tx.Exec(ctx, `DELETE FROM alerts WHERE tenant_id=$1 AND id IN (SELECT id FROM alerts WHERE tenant_id=$1 AND created_at<=$2 ORDER BY created_at,id LIMIT $3)`, t.id, cutoff, limit); err != nil {
					return nil, err
				}
				var remaining int
				if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM analyses WHERE tenant_id=$1 AND closure_purged_at IS NULL)+(SELECT count(*) FROM reports WHERE tenant_id=$1)+(SELECT count(*) FROM raw_documents WHERE tenant_id=$1)+(SELECT count(*) FROM monitor_plans WHERE tenant_id=$1)+(SELECT count(*) FROM alerts WHERE tenant_id=$1)`, t.id).Scan(&remaining); err != nil {
					return nil, err
				}
				if remaining == 0 {
					state = "complete"
				}
			}
		}
		if code != "" {
			state = "pending_cleanup"
			lastError = code
		}
		if state != "complete" {
			allDone = false
		}
		if _, err = tx.Exec(ctx, `UPDATE account_closure_tenants SET cleanup_status=$3,cleanup_due=CASE WHEN $4::integer IS NULL THEN NULL ELSE (SELECT requested_at FROM account_closures WHERE id=$1)+$4::integer*interval '24 hours' END WHERE closure_id=$1 AND tenant_id=$2`, r.ID, t.id, state, t.days); err != nil {
			return nil, err
		}
	}
	state := "retention_pending"
	if allDone {
		state = "complete"
	}
	if lastError != "" {
		state = "pending_cleanup"
	}
	if _, err = tx.Exec(ctx, `UPDATE account_closures SET cleanup_status=$2,last_error=$3 WHERE id=$1`, r.ID, state, lastError); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	r.CleanupStatus = state
	r.LastError = lastError
	return r, nil
}
