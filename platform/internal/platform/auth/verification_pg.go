package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/yuqing/platform/internal/pkg/notification"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/db"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

type PGVerificationStore struct{ pool *pgxpool.Pool }

func NewPGVerificationStore(pool *pgxpool.Pool) *PGVerificationStore {
	return &PGVerificationStore{pool: pool}
}

func verificationTable(purpose string) (table, hash, target string) {
	if verificationSMS(purpose) {
		return "sms_verification_codes", "code_hash", "phone"
	}
	return "verification_tokens", "token_hash", "target"
}
func verificationStorageError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return verificationInvalid()
	}
	if db.IsUniqueViolation(err) {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "identity target already belongs to another account")
	}
	return pkgerrors.Wrap(pkgerrors.ErrServiceUnavailable, "verification storage unavailable")
}
func lockVerificationUser(ctx context.Context, tx pgx.Tx, uid string) (*User, error) {
	u, err := scanUserCenter(tx.QueryRow(ctx, `SELECT `+userCenterColumns+` FROM users WHERE id=$1 FOR UPDATE`, uid))
	if err != nil {
		return nil, verificationStorageError(err)
	}
	return u, nil
}

func (s *PGVerificationStore) ReserveSend(ctx context.Context, purpose, target, ip string, limits VerificationLimits) error {
	if _, err := verificationTarget(purpose, target); err != nil {
		return err
	}
	limits = limits.defaults()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return verificationStorageError(err)
	}
	defer tx.Rollback(ctx)
	now := time.Now()
	keys := []string{"target:" + purpose + ":" + target}
	if ip != "" {
		keys = append(keys, "ip:"+ip)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 741916))`, key); err != nil {
			return verificationStorageError(err)
		}
		var start, last time.Time
		var sends int
		err = tx.QueryRow(ctx, `SELECT window_start,last_sent,sends FROM verification_send_gates WHERE gate_key=$1 FOR UPDATE`, key).Scan(&start, &last, &sends)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return verificationStorageError(err)
		}
		maximum := limits.TargetLimit
		isTarget := key == "target:"+purpose+":"+target
		if !isTarget {
			maximum = limits.IPLimit
		}
		if errors.Is(err, pgx.ErrNoRows) || !now.Before(start.Add(limits.Window)) {
			start = now
			sends = 0
		}
		if sends >= maximum || (isTarget && !last.IsZero() && now.Before(last.Add(limits.Interval))) {
			return pkgerrors.Wrap(pkgerrors.ErrQuotaExceeded, "please wait before requesting another credential")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO verification_send_gates(gate_key,window_start,last_sent,sends) VALUES($1,$2,$3,$4) ON CONFLICT(gate_key) DO UPDATE SET window_start=$2,last_sent=$3,sends=$4`, key, start, now, sends+1); err != nil {
			return verificationStorageError(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return verificationStorageError(err)
	}
	return nil
}

func (s *PGVerificationStore) Issue(ctx context.Context, c VerificationCredential) error {
	if err := validateVerificationCredential(c); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return verificationStorageError(err)
	}
	defer tx.Rollback(ctx)
	if c.IssuerUserID != c.UserID {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, PlatformAdminLockID); err != nil {
			return verificationStorageError(err)
		}
	}
	// All participating users are locked before any credential. This also makes
	// queued actor revocation visible instead of adopting a newly loaded version.
	ids := []string{c.UserID}
	if c.IssuerUserID != c.UserID {
		ids = append(ids, c.IssuerUserID)
	}
	sort.Strings(ids)
	users := map[string]*User{}
	for _, uid := range ids {
		u, err := lockVerificationUser(ctx, tx, uid)
		if err != nil {
			return err
		}
		users[uid] = u
	}
	u, issuer := users[c.UserID], users[c.IssuerUserID]
	if !verificationUserMatches(u, c) || issuer.TokenVersion != c.IssuerVersion {
		return verificationInvalid()
	}
	if c.IssuerUserID != c.UserID {
		if issuer.Status != "active" || (c.Purpose != SetPassword && c.Purpose != PasswordReset) {
			return verificationInvalid()
		}
		var admin bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform_user_roles WHERE user_id=$1 AND role='platform_admin')`, issuer.ID).Scan(&admin); err != nil {
			return verificationStorageError(err)
		}
		if !admin {
			return pkgerrors.ErrForbidden
		}
	}
	table, hash, target := verificationTable(c.Purpose)
	// Serializes same-target issuance by different users as well as resends.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,741917))`, c.Purpose+":"+c.Target); err != nil {
		return verificationStorageError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE `+table+` SET used_at=now() WHERE used_at IS NULL AND purpose=$1 AND (`+target+`=$2 OR user_id=$3)`, c.Purpose, c.Target, c.UserID); err != nil {
		return verificationStorageError(err)
	}
	columns := `id,user_id,issuer_user_id,purpose,` + target + `,` + hash + `,issued_version,issuer_version,expires_at,delivery_status`
	values := `$1,$2,$3,$4,$5,$6,$7,$8,$9,'pending'`
	if !verificationSMS(c.Purpose) {
		columns += `,type`
		values += `,$4`
	}
	if _, err = tx.Exec(ctx, `INSERT INTO `+table+` (`+columns+`) VALUES (`+values+`)`, c.ID, c.UserID, c.IssuerUserID, c.Purpose, c.Target, c.Hash, c.IssuedVersion, c.IssuerVersion, c.ExpiresAt); err != nil {
		return verificationStorageError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return verificationStorageError(err)
	}
	return nil
}
func (s *PGVerificationStore) RecordDelivery(ctx context.Context, id string, accepted bool) error {
	r := notification.Receipt{Provider: "custom", State: "rejected"}
	if accepted {
		r.State = "accepted"
		r.AcceptedAt = time.Now().UTC()
	}
	return s.RecordReceipt(ctx, id, r)
}
func (s *PGVerificationStore) RecordReceipt(ctx context.Context, id string, r notification.Receipt) error {
	r, err := notification.Normalize(r, "")
	if err != nil {
		return verificationInvalid()
	}
	data, err := json.Marshal(r)
	if err != nil {
		return verificationInvalid()
	}
	for _, table := range []string{"verification_tokens", "sms_verification_codes"} {
		tag, err := s.pool.Exec(ctx, `UPDATE `+table+` SET delivery_status=$2,delivery_receipt=$3::jsonb || jsonb_build_object('purpose',purpose),used_at=CASE WHEN $2='rejected' THEN now() ELSE used_at END WHERE id=$1 AND delivery_status='pending' AND used_at IS NULL`, id, r.State, data)
		if err != nil {
			return verificationStorageError(err)
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
	}
	return verificationInvalid()
}
func (s *PGVerificationStore) Consume(ctx context.Context, a VerificationAttempt) (*User, error) {
	// A preliminary lookup discovers only the immutable owner. The authoritative
	// credential is re-read under lock after locking that user.
	table, hash, target := verificationTable(a.Purpose)
	var uid string
	where := hash + `=$2`
	lookup := a.Hash
	if verificationSMS(a.Purpose) {
		where = target + `=$2`
		lookup = a.Target
	}
	query := `SELECT user_id FROM ` + table + ` WHERE purpose=$1 AND ` + where + ` AND used_at IS NULL`
	if err := s.pool.QueryRow(ctx, query, a.Purpose, lookup).Scan(&uid); err != nil {
		return nil, verificationStorageError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, verificationStorageError(err)
	}
	defer tx.Rollback(ctx)
	u, err := lockVerificationUser(ctx, tx, uid)
	if err != nil {
		return nil, err
	}
	c := VerificationCredential{Purpose: a.Purpose}
	err = tx.QueryRow(ctx, `SELECT id,user_id,issuer_user_id,issued_version,issuer_version,`+target+`,`+hash+`,expires_at,attempts,delivery_status='accepted',used_at FROM `+table+` WHERE purpose=$1 AND `+where+` AND used_at IS NULL FOR UPDATE`, a.Purpose, lookup).Scan(&c.ID, &c.UserID, &c.IssuerUserID, &c.IssuedVersion, &c.IssuerVersion, &c.Target, &c.Hash, &c.ExpiresAt, &c.Attempts, &c.Accepted, &c.UsedAt)
	if err != nil {
		return nil, verificationStorageError(err)
	}
	now := time.Now()
	// Cross-user/purpose submissions cannot spend someone else's attempts.
	if c.UserID != uid || !verificationUserMatches(u, c) || !c.Accepted || !now.Before(c.ExpiresAt) || c.Attempts >= maxCodeTries || (a.UserID != "" && a.UserID != uid) || (a.ExpectedVersion != nil && *a.ExpectedVersion != u.TokenVersion) {
		return nil, verificationInvalid()
	}
	if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(a.Hash)) != 1 {
		_, err = tx.Exec(ctx, `UPDATE `+table+` SET attempts=attempts+1,used_at=CASE WHEN attempts+1>=5 THEN now() ELSE used_at END WHERE id=$1`, c.ID)
		if err != nil {
			return nil, verificationStorageError(err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, verificationStorageError(err)
		}
		if c.Attempts+1 >= maxCodeTries {
			return nil, pkgerrors.ErrNotFound
		}
		return nil, verificationInvalid()
	}
	originalVersion := u.TokenVersion
	originalEmail := u.Email
	if err = applyVerification(u, c, a, now); err != nil {
		return nil, err
	}
	if c.Purpose != PhoneLogin {
		_, err = tx.Exec(ctx, `UPDATE users SET email=$2,phone=NULLIF($3,''),email_verified_at=$4,phone_verified_at=$5,password_hash=$6,password_changed_at=$7,status=$8,token_version=$9,row_version=$10 WHERE id=$1`, u.ID, u.Email, u.Phone, u.EmailVerifiedAt, u.PhoneVerifiedAt, u.PasswordHash, u.PasswordChangedAt, u.Status, u.TokenVersion, u.RowVersion)
		if err != nil {
			return nil, verificationStorageError(err)
		}
	}
	if c.Purpose == EmailChange {
		if _, err = tx.Exec(ctx, `UPDATE verification_tokens SET notice_target=$2,notice_state='pending',notice_next_attempt=now() WHERE id=$1`, c.ID, originalEmail); err != nil {
			return nil, verificationStorageError(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE `+table+` SET used_at=$2 WHERE id=$1`, c.ID, now); err != nil {
		return nil, verificationStorageError(err)
	}
	if u.TokenVersion != originalVersion {
		if err = invalidateVerificationUser(ctx, tx, u.ID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, verificationStorageError(err)
	}
	return u, nil
}
func invalidateVerificationUser(ctx context.Context, tx pgx.Tx, uid string) error {
	for _, table := range []string{"verification_tokens", "sms_verification_codes"} {
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET used_at=now() WHERE user_id=$1 AND used_at IS NULL`, uid); err != nil {
			return verificationStorageError(err)
		}
	}
	return nil
}
func (s *PGVerificationStore) UnbindPhone(ctx context.Context, uid string, version int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return verificationStorageError(err)
	}
	defer tx.Rollback(ctx)
	u, err := lockVerificationUser(ctx, tx, uid)
	if err != nil {
		return err
	}
	if u.Status != "active" || u.TokenVersion != version {
		return verificationInvalid()
	}
	if u.EmailVerifiedAt == nil {
		return pkgerrors.ErrConflict
	}
	if u.Phone == "" {
		return pkgerrors.ErrNotFound
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET phone=NULL,phone_verified_at=NULL,token_version=token_version+1,row_version=row_version+1 WHERE id=$1`, uid); err != nil {
		return verificationStorageError(err)
	}
	if err = invalidateVerificationUser(ctx, tx, uid); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return verificationStorageError(err)
	}
	return nil
}
