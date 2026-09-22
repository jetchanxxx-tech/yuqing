package auth

// PGStore 的用户中心实现：UserStore（users 新列，迁移 0007）
// + VerificationStore（verification_tokens / sms_verification_codes 两张表）。

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuqing/platform/internal/pkg/db"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

var _ UserStore = (*PGStore)(nil)
var _ VerificationStore = (*PGVerificationStore)(nil)

const userCenterColumns = `id, email, password_hash, name, phone, avatar_url, timezone,
	email_verified_at, phone_verified_at, password_changed_at,
	COALESCE(trial_analysis_used, 0)`

func scanUserCenter(row pgx.Row) (*User, error) {
	var u User
	var phone, avatarURL, timezone *string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &phone, &avatarURL, &timezone,
		&u.EmailVerifiedAt, &u.PhoneVerifiedAt, &u.PasswordChangedAt, &u.TrialAnalysisUsed)
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

// UpdatePassword 更新密码哈希并记录 password_changed_at。
func (s *PGStore) UpdatePassword(ctx context.Context, userID, newHash string) error {
	const q = `UPDATE users SET password_hash = $2, password_changed_at = now() WHERE id = $1`
	return s.execUserUpdate(ctx, q, userID, newHash)
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
func (s *PGStore) SetPhone(ctx context.Context, userID, phone string) error {
	const q = `UPDATE users SET phone = $2, phone_verified_at = now() WHERE id = $1`
	if err := s.execUserUpdate(ctx, q, userID, phone); err != nil {
		if db.IsUniqueViolation(err) {
			return pkgerrors.Wrap(pkgerrors.ErrConflict, "phone already bound to another account")
		}
		return err
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

// ─── VerificationStore（PG 实现） ─────────────────────────────

// PGVerificationStore 验证凭据存储。
// 邮箱 token → verification_tokens 表（type='email_verify'，
// user_id 列存目标用户）；
// 短信验证码 → sms_verification_codes 表（phone 主键，同手机号覆盖旧码）。
type PGVerificationStore struct {
	pool *pgxpool.Pool
}

// NewPGVerificationStore 创建 PG 验证凭据存储。
func NewPGVerificationStore(pool *pgxpool.Pool) *PGVerificationStore {
	return &PGVerificationStore{pool: pool}
}

// SaveEmailToken 保存邮箱验证 token（同 token 覆盖延长）。
func (s *PGVerificationStore) SaveEmailToken(ctx context.Context, token, userID string, ttl time.Duration) error {
	const q = `INSERT INTO verification_tokens (id, user_id, type, token, expires_at)
		VALUES (md5(random()::text), $2, 'email_verify', $1, $3)
		ON CONFLICT (token) DO UPDATE SET expires_at = EXCLUDED.expires_at, used_at = NULL`
	if _, err := s.pool.Exec(ctx, q, token, userID, time.Now().Add(ttl)); err != nil {
		return wrapDB(err, "save email token")
	}
	return nil
}

// LoadEmailToken 读取未消费未过期的 token 对应的 userID。
func (s *PGVerificationStore) LoadEmailToken(ctx context.Context, token string) (string, error) {
	const q = `SELECT user_id FROM verification_tokens
		WHERE token = $1 AND type = 'email_verify' AND used_at IS NULL AND expires_at > now()`
	var userID string
	err := s.pool.QueryRow(ctx, q, token).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", pkgerrors.Wrap(pkgerrors.ErrNotFound, "verification expired")
	}
	if err != nil {
		return "", wrapDB(err, "load email token")
	}
	return userID, nil
}

// ConsumeEmailToken 标记 token 已使用（保留行作审计）。
func (s *PGVerificationStore) ConsumeEmailToken(ctx context.Context, token string) error {
	const q = `UPDATE verification_tokens SET used_at = now() WHERE token = $1`
	if _, err := s.pool.Exec(ctx, q, token); err != nil {
		return wrapDB(err, "consume email token")
	}
	return nil
}

// SaveSMSCode 保存验证码（phone 唯一，同手机号覆盖旧码 = 天然防刷限流辅助）。
func (s *PGVerificationStore) SaveSMSCode(ctx context.Context, phone, purpose, code string, ttl time.Duration) error {
	const q = `INSERT INTO sms_verification_codes (id, phone, code, purpose, expires_at)
		VALUES (md5(random()::text), $1, $2, $3, $4)
		ON CONFLICT (phone) DO UPDATE SET code = EXCLUDED.code,
			purpose = EXCLUDED.purpose, expires_at = EXCLUDED.expires_at, created_at = now()`
	if _, err := s.pool.Exec(ctx, q, phone, code, purpose, time.Now().Add(ttl)); err != nil {
		return wrapDB(err, "save sms code")
	}
	return nil
}

// LoadSMSCode 读取验证码。
func (s *PGVerificationStore) LoadSMSCode(ctx context.Context, phone, purpose string) (string, error) {
	const q = `SELECT code FROM sms_verification_codes WHERE phone = $1 AND purpose = $2 AND expires_at > now()`
	var code string
	err := s.pool.QueryRow(ctx, q, phone, purpose).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", pkgerrors.Wrap(pkgerrors.ErrNotFound, "verification expired")
	}
	if err != nil {
		return "", wrapDB(err, "load sms code")
	}
	return code, nil
}

// ConsumeSMSCode 删除验证码。
func (s *PGVerificationStore) ConsumeSMSCode(ctx context.Context, phone, purpose string) error {
	const q = `DELETE FROM sms_verification_codes WHERE phone = $1 AND purpose = $2`
	if _, err := s.pool.Exec(ctx, q, phone, purpose); err != nil {
		return wrapDB(err, "consume sms code")
	}
	return nil
}
