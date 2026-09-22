-- +goose Up
-- 用户中心 P0 功能：修改密码 + 邮箱验证 + 个人资料 + 手机号绑定
-- 基于：用户决策 2026-09-22 + 方案 B（允许试用 1 次）
-- 防刷设计：验证码/ token 采「同键覆盖」语义（SaveSMSCode/SaveEmailToken 的
-- ON CONFLICT DO UPDATE），handler 级 60s 限流在 P1 落地；不用 EXCLUDE 约束
-- （now() 不可变 + 会卡住 5 分钟内的合法重发场景）。

-- ─────────────────────────────────────────────────────────
-- 1. users 表新增字段
-- ─────────────────────────────────────────────────────────
ALTER TABLE users ADD COLUMN password_changed_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN avatar_url TEXT;
ALTER TABLE users ADD COLUMN timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai';
ALTER TABLE users ADD COLUMN phone CITEXT;
ALTER TABLE users ADD COLUMN phone_verified_at TIMESTAMPTZ;

-- 方案 B：允许试用 1 次（邮箱验证前）
-- trial_analysis_used = 0 时允许创建分析，创建后置 1
ALTER TABLE users ADD COLUMN trial_analysis_used SMALLINT NOT NULL DEFAULT 0;

-- 通知偏好（JSONB，默认：任务完成/告警/账单开启，产品更新关闭）
ALTER TABLE users ADD COLUMN notification_prefs JSONB NOT NULL DEFAULT '{
  "task_completed": true,
  "alert_triggered": true,
  "billing_reminder": true,
  "product_updates": false
}'::jsonb;

-- 历史数据修正：已注册用户的密码修改时间回填创建时间
UPDATE users SET password_changed_at = created_at WHERE password_changed_at IS NULL;

-- phone 唯一约束（允许 NULL，多个用户可以都是 NULL）
CREATE UNIQUE INDEX idx_users_phone_unique ON users(phone) WHERE phone IS NOT NULL;

-- ─────────────────────────────────────────────────────────
-- 2. verification_tokens 表（验证 token 统一管理）
-- ─────────────────────────────────────────────────────────
CREATE TABLE verification_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT UNIQUE NOT NULL,
    type       TEXT NOT NULL, -- email_verify | password_reset
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_verification_tokens_token ON verification_tokens(token) WHERE used_at IS NULL;
CREATE INDEX idx_verification_tokens_user_type ON verification_tokens(user_id, type);
CREATE INDEX idx_verification_tokens_expires ON verification_tokens(expires_at) WHERE used_at IS NULL;

COMMENT ON TABLE verification_tokens IS '验证 token 表：邮箱验证、找回密码（token 一次性，消费后置 used_at）';

-- ─────────────────────────────────────────────────────────
-- 3. login_sessions 表（登录历史，滚动窗口 100 条）
-- ─────────────────────────────────────────────────────────
CREATE TABLE login_sessions (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip_address   TEXT NOT NULL,
    user_agent   TEXT NOT NULL,
    device       TEXT,     -- Windows 11 / macOS 14 / iPhone 15
    location     TEXT,     -- 北京市 / 香港 / etc（通过 IP 库解析，P1 功能）
    logged_in_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_login_sessions_user_id_logged_in ON login_sessions(user_id, logged_in_at DESC);

COMMENT ON TABLE login_sessions IS '登录历史表：滚动窗口保留最近 100 条/用户';

-- 滚动窗口触发器函数（每次插入后保留最近 100 条）
CREATE OR REPLACE FUNCTION cleanup_old_login_sessions()
RETURNS TRIGGER AS $$
BEGIN
    DELETE FROM login_sessions
    WHERE id IN (
        SELECT id FROM login_sessions
        WHERE user_id = NEW.user_id
        ORDER BY logged_in_at DESC
        OFFSET 100
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_cleanup_login_sessions
AFTER INSERT ON login_sessions
FOR EACH ROW EXECUTE FUNCTION cleanup_old_login_sessions();

COMMENT ON FUNCTION cleanup_old_login_sessions IS '滚动窗口触发器：每次插入后保留最近 100 条登录历史';

-- ─────────────────────────────────────────────────────────
-- 4. sms_verification_codes 表（短信验证码，5 分钟过期）
-- phone 唯一：SaveSMSCode 走 ON CONFLICT (phone) 覆盖旧码（= 天然防刷限流辅助）
-- ─────────────────────────────────────────────────────────
CREATE TABLE sms_verification_codes (
    id         TEXT PRIMARY KEY,
    phone      TEXT NOT NULL UNIQUE,
    code       TEXT NOT NULL,  -- 6 位数字
    purpose    TEXT NOT NULL,  -- bind_phone | login | reset_password
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sms_codes_expires ON sms_verification_codes(expires_at);

COMMENT ON TABLE sms_verification_codes IS '短信验证码表：5 分钟过期；同手机号覆盖旧码';

-- ─────────────────────────────────────────────────────────
-- 5. platform_settings 新增配置项（邮件/短信服务）
-- 表结构（0002）：key/value/updated_at 三列，无 description
-- 邮件服务配置（Resend / SMTP）
INSERT INTO platform_settings (key, value) VALUES
  ('email_provider', 'resend'),
  ('resend_api_key', ''),
  ('email_from_address', 'noreply@pangu-cloud.com'),
  ('email_from_name', '盘古舆情'),
  ('smtp_host', ''),
  ('smtp_port', '465'),
  ('smtp_username', ''),
  ('smtp_password', '')
ON CONFLICT (key) DO NOTHING;

-- 短信服务配置（阿里云 / 腾讯云）
INSERT INTO platform_settings (key, value) VALUES
  ('sms_provider', 'aliyun'),
  ('sms_access_key_id', ''),
  ('sms_access_key_secret', ''),
  ('sms_sign_name', '盘古舆情'),
  ('sms_template_code', '')
ON CONFLICT (key) DO NOTHING;

-- +goose Down
-- 回滚顺序：先删除依赖，再删除表

DROP TRIGGER IF EXISTS trigger_cleanup_login_sessions ON login_sessions;
DROP FUNCTION IF EXISTS cleanup_old_login_sessions();

DROP TABLE IF EXISTS sms_verification_codes;
DROP TABLE IF EXISTS login_sessions;
DROP TABLE IF EXISTS verification_tokens;

-- 删除 platform_settings 配置项
DELETE FROM platform_settings WHERE key IN (
  'email_provider', 'resend_api_key', 'email_from_address', 'email_from_name',
  'smtp_host', 'smtp_port', 'smtp_username', 'smtp_password',
  'sms_provider', 'sms_access_key_id', 'sms_access_key_secret', 'sms_sign_name', 'sms_template_code'
);

DROP INDEX IF EXISTS idx_users_phone_unique;

ALTER TABLE users DROP COLUMN IF EXISTS notification_prefs;
ALTER TABLE users DROP COLUMN IF EXISTS trial_analysis_used;
ALTER TABLE users DROP COLUMN IF EXISTS phone_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS phone;
ALTER TABLE users DROP COLUMN IF EXISTS timezone;
ALTER TABLE users DROP COLUMN IF EXISTS avatar_url;
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS password_changed_at;
