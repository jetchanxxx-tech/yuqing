package auth

// PGStore 的用户中心实现：UserStore（users 新列，迁移 0007）
// + VerificationStore（verification_tokens / sms_verification_codes 两张表）。

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yuqing/platform/internal/pkg/db"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

var _ UserStore = (*PGStore)(nil)
var _ VerificationStore = (*PGVerificationStore)(nil)

const userCenterColumns = `id, email, password_hash, name, phone, avatar_url, timezone,
	email_verified_at, phone_verified_at, password_changed_at,
	COALESCE(trial_analysis_used, 0), status, created_at, last_login_at, token_version, row_version`

func scanUserCenter(row pgx.Row) (*User, error) {
	var u User
	var phone, avatarURL, timezone *string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &phone, &avatarURL, &timezone,
		&u.EmailVerifiedAt, &u.PhoneVerifiedAt, &u.PasswordChangedAt, &u.TrialAnalysisUsed,
		&u.Status, &u.CreatedAt, &u.LastLoginAt, &u.TokenVersion, &u.RowVersion)
	if err != nil {
		return nil, err
	}
	if phone != nil {
		u.Phone = *phone
	}
	if avatarURL != nil {
		u.AvatarURL = *avatarURL
	}
	if timezone != nil {
		u.Timezone = *timezone
	}
	return &u, nil
}

// GetByID 按 ID 查用户（含用户中心字段）。
func (s *PGStore) GetByID(ctx context.Context, userID string) (*User, error) {
	const q = `SELECT ` + userCenterColumns + ` FROM users WHERE id = $1`
	u, err := scanUserCenter(s.pool.QueryRow(ctx, q, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, wrapDB(err, "get user by id")
	}
	return u, nil
}

// GetByPhone 按手机号查用户（部分唯一索引保证至多一行）。
func (s *PGStore) GetByPhone(ctx context.Context, phone string) (*User, error) {
	const q = `SELECT ` + userCenterColumns + ` FROM users WHERE phone = $1`
	u, err := scanUserCenter(s.pool.QueryRow(ctx, q, phone))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	if err != nil {
		return nil, wrapDB(err, "get user by phone")
	}
	return u, nil
}

// UpdatePassword commits only while the verified credential version is current
// and the account remains active. The predicate and version increments execute
// in the same UPDATE, including PostgreSQL's concurrent-row recheck.
func (s *PGStore) UpdatePassword(ctx context.Context, userID, newHash string, expectedVersion int64) error {
	const q = `UPDATE users SET password_hash = $2, password_changed_at = now(),
		token_version = token_version + 1, row_version = row_version + 1
		WHERE id = $1 AND status = 'active' AND token_version = $3`
	tag, err := s.pool.Exec(ctx, q, userID, newHash, expectedVersion)
	if err != nil {
		return wrapDB(err, "update user password")
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// The write already failed closed. This read only selects the error for
	// a missing/inactive account versus a changed credential version.
	u, err := s.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Status != "active" {
		return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "account unavailable")
	}
	return pkgerrors.Wrap(pkgerrors.ErrConflict, "credentials changed; sign in again")
}

// MarkEmailVerified 标记邮箱已验证。
func (s *PGStore) MarkEmailVerified(ctx context.Context, userID string) error {
	const q = `UPDATE users SET email_verified_at = now() WHERE id = $1`
	return s.execUserUpdate(ctx, q, userID)
}

// UpdateProfile 更新昵称/头像/时区（空串字段跳过）。
func (s *PGStore) UpdateProfile(ctx context.Context, userID, name, avatarURL, timezone string) error {
	const q = `UPDATE users SET
		name = CASE WHEN $2 = '' THEN name ELSE $2 END,
		avatar_url = CASE WHEN $3 = '' THEN avatar_url ELSE $3 END,
		timezone = CASE WHEN $4 = '' THEN timezone ELSE $4 END
		WHERE id = $1`
	return s.execUserUpdate(ctx, q, userID, name, avatarURL, timezone)
}

// SetPhone 绑定手机号。唯一索引冲突 → ErrConflict，用户不存在 → ErrNotFound。
// 注意唯一冲突判断必须在 wrapDB 之前：wrapDB 会吞掉原始 PgError（只留文本），
// 之后 IsUniqueViolation 永远 false（契约测试实测踩过）。
func (s *PGStore) SetPhone(ctx context.Context, userID, phone string) error {
	const q = `UPDATE users SET phone = $2, phone_verified_at = now() WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, userID, phone)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "phone already bound to another account")
		}
		return wrapDB(err, "update user phone")
	}
	if tag.RowsAffected() == 0 {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	return nil
}

// ClearPhone 解绑手机号。
func (s *PGStore) ClearPhone(ctx context.Context, userID string) error {
	const q = `UPDATE users SET phone = NULL, phone_verified_at = NULL WHERE id = $1`
	return s.execUserUpdate(ctx, q, userID)
}

// ConsumeTrialAnalysis 原子消耗试用额度（0→1），单条 UPDATE 竞态安全。
func (s *PGStore) ConsumeTrialAnalysis(ctx context.Context, userID string) (bool, error) {
	const q = `UPDATE users SET trial_analysis_used = 1
		WHERE id = $1 AND COALESCE(trial_analysis_used, 0) = 0`
	tag, err := s.pool.Exec(ctx, q, userID)
	if err != nil {
		return false, wrapDB(err, "consume trial analysis")
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PGStore) execUserUpdate(ctx context.Context, q string, args ...any) error {
	tag, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return wrapDB(err, "update user")
	}
	if tag.RowsAffected() == 0 {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
	}
	return nil
}

// PostgreSQL rechecks these predicates after waiting for a concurrent row
// update, so queued requests cannot adopt a revoked actor's newer version.
func (s *PGStore) UpdateOwnProfile(ctx context.Context, actor Principal, name, timezone string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET name=CASE WHEN $3='' THEN name ELSE $3 END,timezone=$4,row_version=row_version+1 WHERE id=$1 AND status='active' AND token_version=$2`, actor.UserID, actor.TokenVersion, name, timezone)
	if err != nil {
		return wrapDB(err, "update own profile")
	}
	if tag.RowsAffected() != 1 {
		return pkgerrors.ErrUnauthorized
	}
	return nil
}
func (s *PGStore) ReplaceOwnAvatar(ctx context.Context, actor Principal, previous, next string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET avatar_url=$4,row_version=row_version+1 WHERE id=$1 AND status='active' AND token_version=$2 AND COALESCE(avatar_url,'')=$3`, actor.UserID, actor.TokenVersion, previous, next)
	if err != nil {
		cause := wrapDB(err, "replace own avatar")
		var serverError *pgconn.PgError
		// Only confirmed integrity-constraint rejection proves this autocommit
		// statement rolled back. Transport, cancellation and uncertain SQLSTATEs
		// retain their staged object; wrapping INTERNAL alone proves nothing.
		if errors.As(err, &serverError) && (serverError.Severity == "ERROR" || serverError.SeverityUnlocalized == "ERROR") && len(serverError.Code) == 5 && serverError.Code[:2] == "23" {
			return &avatarWriteNotCommitted{cause: cause}
		}
		return cause
	}
	if tag.RowsAffected() == 0 {
		return &avatarWriteNotCommitted{cause: pkgerrors.ErrConflict}
	}
	if tag.RowsAffected() != 1 {
		return pkgerrors.ErrInternal
	}
	return nil
}
