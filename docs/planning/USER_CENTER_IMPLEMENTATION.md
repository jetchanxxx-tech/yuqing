# 用户中心功能 - 技术实施方案

> **状态**：待用户审阅  
> **版本**：v1.0  
> **日期**：2026-09-23  
> **基于**：产品方案 + 技术分析 + 用户决策（2026-09-22 确认）

---

## § 1. 架构设计

### 1.1 服务层划分

```
platform/internal/platform/
├── auth/              ← 认证服务（已有）
│   service.go         Register/Login/Authenticate/Refresh
│   + ChangePassword   ← 新增方法
│   + SendVerificationEmail / VerifyEmail
│   + ResetPassword (P1)
│
├── user/              ← 用户服务（全新创建）
│   service.go         UpdateProfile / GetProfile
│   + BindPhone / UnbindPhone / SendPhoneCode
│   + UpdateAvatar / UpdateTimezone
│   + GetLoginSessions (P1) / GetNotificationPrefs (P1)
│   store.go           interface + memory/postgres 实现
│
├── billing/           ← 计费服务（已有，复用）
│   billing.go         DefaultPlans / AddonSkus / ResolveSKU
│
├── credit/            ← 额度服务（已有，复用）
│   credit.go          GetCredits / GetTransactions / GetBalance
│
└── apikey/            ← API Key 服务（已有，需暴露 UI）
    service.go         CreateKey / ListKeys / RevokeKey

pkg/                   ← 基础设施包
├── email/             ← 邮件发送（全新）
│   provider.go        interface + Resend 实现
│   templates.go       验证邮件/找回密码模板
│
├── sms/               ← 短信发送（全新）
│   provider.go        interface
│   aliyun.go          阿里云实现
│   tencent.go         腾讯云实现
│
└── storage/           ← 文件存储（全新）
    local.go           本地存储实现（MVP）
    oss.go             阿里云 OSS（P1）
```

**关键设计决策**：
- ✅ **新建 `user` 包**：个人资料/手机号/头像等领域聚合在独立包，不与认证服务耦合
- ✅ **Provider 接口**：SMTP/SMS 可插拔，环境变量切换供应商
- ✅ **复用 billing/credit**：套餐余额页纯前端，后端 API 已完整

### 1.2 API 路由设计

```
/api/v1/
├── auth/                           ← 认证相关（已有 /login /register /refresh）
│   PUT    /password                # 修改密码（新增）
│   POST   /send-verification-email # 发送验证邮件（新增）
│   GET    /verify-email?token=xxx  # 验证邮箱（新增，免鉴权）
│   POST   /reset-password-request  # 找回密码（P1）
│   POST   /reset-password          # 重置密码（P1）
│
├── user/                           ← 用户资料（全新路由组）
│   GET    /profile                 # 获取当前用户资料
│   PUT    /profile                 # 更新资料（昵称/时区）
│   POST   /avatar                  # 上传头像
│   POST   /phone/send-code         # 发送手机验证码
│   POST   /phone/bind              # 绑定手机号
│   POST   /phone/unbind            # 解绑手机号
│   GET    /login-sessions          # 登录历史（P1）
│   GET    /notification-prefs      # 通知偏好（P1）
│   PUT    /notification-prefs      # 更新通知偏好（P1）
│
├── billing/                        ← 计费相关（已有 /credits /plans）
│   # 套餐余额页复用现有接口，无需新增
│
└── api-keys/                       ← API Key 管理（新增路由组）
    POST   /                        # 创建 Key（仅 tenant_admin）
    GET    /                        # 列出 Keys（仅 tenant_admin）
    DELETE /:id                     # 撤销 Key（仅 tenant_admin）
```

**权限设计**：
- 所有 `/user/*` 端点：需登录（JWT middleware）
- `/api-keys/*`：需 `tenant_admin` 角色（RBAC middleware）
- `/auth/verify-email`：免鉴权（公开链接点击）

### 1.3 前端路由设计

```
/settings                          ← 用户中心根路由
├── /profile                       # 个人资料
├── /security                      # 账户安全（修改密码 + 登录历史）
├── /connections                   # 绑定与集成（手机号 + 第三方账号）
├── /notifications                 # 通知设置（P1）
├── /api-keys                      # API Key 管理（仅 tenant_admin 可见）
└── /billing                       # 套餐与余额

/verify-email?token=xxx            ← 邮箱验证独立页
```

**Layout 结构**：
```tsx
SettingsLayout
├── Sidebar（左侧导航）
│   - 个人资料
│   - 账户安全
│   - 绑定与集成
│   - 通知设置（P1）
│   - API 管理（条件渲染：isTenantAdmin）
│   - 套餐与订阅
└── Content Area（右侧内容区，<Outlet />）
```

---

## § 2. 数据库设计

### 2.1 迁移 SQL（0007_user_center_p0.sql）

```sql
-- +goose Up
-- 用户中心 P0 功能：修改密码 + 邮箱验证 + 个人资料 + 手机号绑定
-- 基于：用户决策 2026-09-22 + 方案 B（允许试用 1 次）

-- ─────────────────────────────────────────────────────────
-- 1. users 表新增字段
-- ─────────────────────────────────────────────────────────
ALTER TABLE users ADD COLUMN password_changed_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN avatar_url TEXT;
ALTER TABLE users ADD COLUMN timezone TEXT DEFAULT 'Asia/Shanghai';
ALTER TABLE users ADD COLUMN phone CITEXT UNIQUE;
ALTER TABLE users ADD COLUMN phone_verified_at TIMESTAMPTZ;

-- 方案 B：允许试用 1 次（邮箱验证前）
-- trial_analysis_used = 0 时允许创建分析，创建后置 1
ALTER TABLE users ADD COLUMN trial_analysis_used SMALLINT NOT NULL DEFAULT 0;

-- 历史数据修正：已注册用户的密码修改时间回填创建时间
UPDATE users SET password_changed_at = created_at WHERE password_changed_at IS NULL;

-- ─────────────────────────────────────────────────────────
-- 2. verification_tokens 表（验证 token 统一管理）
-- ─────────────────────────────────────────────────────────
CREATE TABLE verification_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT UNIQUE NOT NULL,
    type       TEXT NOT NULL, -- email_verification | password_reset
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    
    -- 防刷：同类型 token 5 分钟内只能发 1 次
    CONSTRAINT unique_user_type_recent 
        EXCLUDE USING btree (user_id WITH =, type WITH =) 
        WHERE (created_at > now() - interval '5 minutes' AND used_at IS NULL)
);

CREATE INDEX idx_verification_tokens_token ON verification_tokens(token) WHERE used_at IS NULL;
CREATE INDEX idx_verification_tokens_user_type ON verification_tokens(user_id, type);
CREATE INDEX idx_verification_tokens_expires ON verification_tokens(expires_at) WHERE used_at IS NULL;

-- ─────────────────────────────────────────────────────────
-- 3. login_sessions 表（登录历史，滚动窗口 100 条）
-- ─────────────────────────────────────────────────────────
CREATE TABLE login_sessions (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip_address   TEXT NOT NULL,
    user_agent   TEXT NOT NULL,
    device       TEXT,     -- Windows 11 / macOS 14 / iPhone
    location     TEXT,     -- 北京市 / 香港 / Unknown
    logged_in_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_login_sessions_user_logged ON login_sessions(user_id, logged_in_at DESC);

-- 滚动窗口触发器：每次插入后，保留用户最近 100 条，删除更旧的
CREATE OR REPLACE FUNCTION prune_login_sessions()
RETURNS TRIGGER AS $$
BEGIN
    DELETE FROM login_sessions
    WHERE user_id = NEW.user_id
      AND id NOT IN (
          SELECT id FROM login_sessions
          WHERE user_id = NEW.user_id
          ORDER BY logged_in_at DESC
          LIMIT 100
      );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prune_login_sessions
AFTER INSERT ON login_sessions
FOR EACH ROW
EXECUTE FUNCTION prune_login_sessions();

-- ─────────────────────────────────────────────────────────
-- 4. notification_prefs 表（P1，预留结构）
-- ─────────────────────────────────────────────────────────
CREATE TABLE notification_prefs (
    user_id              TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    task_complete        BOOLEAN NOT NULL DEFAULT true,
    alert_negative       BOOLEAN NOT NULL DEFAULT true,
    billing_reminder     BOOLEAN NOT NULL DEFAULT true,
    product_updates      BOOLEAN NOT NULL DEFAULT false,
    alert_frequency      TEXT NOT NULL DEFAULT 'immediate', -- immediate | hourly | daily
    task_frequency       TEXT NOT NULL DEFAULT 'immediate',
    quiet_hours_enabled  BOOLEAN NOT NULL DEFAULT false,
    quiet_hours_start    TIME,
    quiet_hours_end      TIME,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ─────────────────────────────────────────────────────────
-- 5. oauth_connections 表（第三方账号绑定，P1）
-- ─────────────────────────────────────────────────────────
CREATE TABLE oauth_connections (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider         TEXT NOT NULL, -- wechat | github | wework
    provider_user_id TEXT NOT NULL,
    provider_email   TEXT,
    provider_name    TEXT,
    avatar_url       TEXT,
    access_token     TEXT,      -- 加密存储（TODO: 接入密钥管理）
    refresh_token    TEXT,
    expires_at       TIMESTAMPTZ,
    connected_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at     TIMESTAMPTZ,
    
    UNIQUE (provider, provider_user_id),
    UNIQUE (user_id, provider)  -- 每个用户每种第三方只能绑定 1 个账号
);

CREATE INDEX idx_oauth_connections_user ON oauth_connections(user_id);

-- +goose Down
DROP TABLE IF EXISTS oauth_connections;
DROP TABLE IF EXISTS notification_prefs;
DROP TRIGGER IF EXISTS trg_prune_login_sessions ON login_sessions;
DROP FUNCTION IF EXISTS prune_login_sessions;
DROP TABLE IF EXISTS login_sessions;
DROP TABLE IF EXISTS verification_tokens;

ALTER TABLE users DROP COLUMN IF EXISTS trial_analysis_used;
ALTER TABLE users DROP COLUMN IF EXISTS phone_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS phone;
ALTER TABLE users DROP COLUMN IF EXISTS timezone;
ALTER TABLE users DROP COLUMN IF EXISTS avatar_url;
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS password_changed_at;
```

### 2.2 表结构说明

#### users 表新增字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `password_changed_at` | TIMESTAMPTZ | 密码最后修改时间，用于"密码强度"展示 |
| `email_verified_at` | TIMESTAMPTZ | 邮箱验证时间戳，NULL = 未验证 |
| `avatar_url` | TEXT | 头像 URL，NULL 时前端显示 Gravatar 或首字母 |
| `timezone` | TEXT | 时区，默认 Asia/Shanghai |
| `phone` | CITEXT | 手机号，UNIQUE 约束（跨租户唯一） |
| `phone_verified_at` | TIMESTAMPTZ | 手机验证时间戳 |
| `trial_analysis_used` | SMALLINT | 方案 B：0=未用试用，1=已用（邮箱验证前只能用 1 次） |

#### verification_tokens 表

**用途**：邮箱验证、找回密码的 token 统一管理  
**防刷机制**：EXCLUDE 约束 + 5 分钟窗口，同类型 token 不可频繁发送  
**过期清理**：后台 cron（每日 1 次）删除 `expires_at < now() - interval '7 days'`

#### login_sessions 表

**滚动窗口**：触发器自动保留最近 100 条，删除更旧记录  
**性能考虑**：INSERT 触发 DELETE，对高频登录用户可能有轻微延迟（<10ms）  
**地理位置**：`location` 字段可选接入 IP2Location / GeoIP2（P1）

### 2.3 索引设计

```sql
-- verification_tokens：查询活跃 token
idx_verification_tokens_token (token) WHERE used_at IS NULL

-- login_sessions：按用户倒序分页
idx_login_sessions_user_logged (user_id, logged_in_at DESC)

-- oauth_connections：用户绑定查询
idx_oauth_connections_user (user_id)
```

**覆盖索引优化**（可选）：
```sql
CREATE INDEX idx_verification_tokens_lookup 
ON verification_tokens(token, user_id, type, expires_at) 
WHERE used_at IS NULL;
```

---

## § 3. 外部服务集成

### 3.1 Resend.com（邮件发送）

#### 为什么选择 Resend？

✅ **用户决策明确**：阿里云邮件推送 → Resend.com（2026-09-22 确认）  
✅ **开发体验**：RESTful API + 官方 Go SDK（`github.com/resendlabs/resend-go`）  
✅ **国内可达性**：实测从国内服务器调用 Resend API 延迟 200-300ms（可接受）  
✅ **费用**：免费 3000 封/月，超出 $1/千封

#### Go SDK 集成

```go
// pkg/email/provider.go
package email

import (
    "context"
    "fmt"
    "github.com/resendlabs/resend-go"
)

type Provider interface {
    SendVerificationEmail(ctx context.Context, to, name, verifyURL string) error
    SendPasswordResetEmail(ctx context.Context, to, name, resetURL string) error
    SendTaskCompleteEmail(ctx context.Context, to, taskName, reportURL string) error
}

type ResendProvider struct {
    client *resend.Client
    from   string
}

func NewResendProvider(apiKey, from string) *ResendProvider {
    return &ResendProvider{
        client: resend.NewClient(apiKey),
        from:   from,
    }
}

func (p *ResendProvider) SendVerificationEmail(ctx context.Context, to, name, verifyURL string) error {
    params := &resend.SendEmailRequest{
        From:    p.from,
        To:      []string{to},
        Subject: "验证您的盘古舆情账号",
        Html:    renderVerificationTemplate(name, verifyURL),
    }
    
    _, err := p.client.Emails.Send(params)
    if err != nil {
        return fmt.Errorf("resend: send failed: %w", err)
    }
    return nil
}

func renderVerificationTemplate(name, verifyURL string) string {
    return fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body style="font-family: sans-serif; max-width: 600px; margin: 0 auto;">
    <h2>Hi %s,</h2>
    <p>欢迎注册盘古舆情！请点击下方按钮验证您的邮箱：</p>
    <p style="text-align: center; margin: 30px 0;">
        <a href="%s" style="background: #1890ff; color: white; padding: 12px 24px; 
           text-decoration: none; border-radius: 4px; display: inline-block;">
            验证邮箱
        </a>
    </p>
    <p style="color: #666; font-size: 14px;">
        链接 24 小时内有效。如果按钮无法点击，请复制以下链接到浏览器：<br>
        <code>%s</code>
    </p>
    <hr style="margin: 40px 0; border: none; border-top: 1px solid #eee;">
    <p style="color: #999; font-size: 12px;">
        此邮件由盘古舆情自动发送，请勿回复。
    </p>
</body>
</html>
`, name, verifyURL, verifyURL)
}
```

#### 配置项

```bash
# platform/config/server.env
EMAIL_PROVIDER=resend
RESEND_API_KEY=re_***（从 resend.com/api-keys 获取）
EMAIL_FROM=盘古舆情 <noreply@pangu-cloud.com>

# 发件域名配置（resend.com 控制台）
# 1. 添加域名：pangu-cloud.com
# 2. 配置 DNS 记录：
#    TXT  @ "resend-verification=xxxx"
#    MX   @ mx.resend.com (priority 10)
```

#### 费用估算

- 免费额度：3000 封/月
- 预估月发送量：
  - 注册验证：100 用户 × 1.5 次（重发） = 150 封
  - 找回密码：10 次/月 × 1.2 次 = 12 封
  - 任务完成通知：100 用户 × 5 任务 = 500 封
  - **总计**：约 700 封/月（免费额度内）

#### 错误处理与重试

```go
func (p *ResendProvider) SendVerificationEmail(ctx context.Context, to, name, verifyURL string) error {
    var lastErr error
    for attempt := 0; attempt < 3; attempt++ {
        _, err := p.client.Emails.Send(params)
        if err == nil {
            return nil
        }
        
        // 速率限制 / 临时故障重试
        if isRetryable(err) {
            time.Sleep(time.Duration(attempt+1) * time.Second)
            lastErr = err
            continue
        }
        
        // 永久失败立即返回（如邮箱格式错误）
        return fmt.Errorf("resend: %w", err)
    }
    return fmt.Errorf("resend: max retries exceeded: %w", lastErr)
}
```

### 3.2 SMS 服务（可选服务商）

#### 为什么设计为可选？

✅ **用户决策明确**：阿里云/腾讯云，环境变量切换（2026-09-22 确认）  
✅ **防厂商锁定**：接口抽象 + 双实现，便于切换或多活  
✅ **成本优化**：不同场景选最优费率（阿里云验证码 0.045 元/条，腾讯云 0.04 元/条）

#### Interface 设计

```go
// pkg/sms/provider.go
package sms

import "context"

type Provider interface {
    SendVerificationCode(ctx context.Context, phone, code string) error
}

// NewProvider 根据环境变量创建 provider
func NewProvider(providerName string) (Provider, error) {
    switch providerName {
    case "aliyun":
        return NewAliyunProvider(
            os.Getenv("SMS_ALIYUN_ACCESS_KEY_ID"),
            os.Getenv("SMS_ALIYUN_ACCESS_KEY_SECRET"),
            os.Getenv("SMS_ALIYUN_SIGN_NAME"),
            os.Getenv("SMS_ALIYUN_TEMPLATE_CODE"),
        )
    case "tencent":
        return NewTencentProvider(
            os.Getenv("SMS_TENCENT_SECRET_ID"),
            os.Getenv("SMS_TENCENT_SECRET_KEY"),
            os.Getenv("SMS_TENCENT_SDK_APP_ID"),
            os.Getenv("SMS_TENCENT_SIGN_NAME"),
            os.Getenv("SMS_TENCENT_TEMPLATE_ID"),
        )
    default:
        return nil, fmt.Errorf("unknown sms provider: %s", providerName)
    }
}
```

#### 阿里云实现

```go
// pkg/sms/aliyun.go
package sms

import (
    "context"
    "fmt"
    dysmsapi "github.com/aliyun/alibaba-cloud-sdk-go/services/dysmsapi"
)

type AliyunProvider struct {
    client       *dysmsapi.Client
    signName     string
    templateCode string
}

func NewAliyunProvider(accessKeyID, accessKeySecret, signName, templateCode string) (*AliyunProvider, error) {
    client, err := dysmsapi.NewClientWithAccessKey("cn-hangzhou", accessKeyID, accessKeySecret)
    if err != nil {
        return nil, fmt.Errorf("aliyun sms: init failed: %w", err)
    }
    return &AliyunProvider{
        client:       client,
        signName:     signName,
        templateCode: templateCode,
    }, nil
}

func (p *AliyunProvider) SendVerificationCode(ctx context.Context, phone, code string) error {
    request := dysmsapi.CreateSendSmsRequest()
    request.Scheme = "https"
    request.PhoneNumbers = phone
    request.SignName = p.signName
    request.TemplateCode = p.templateCode
    request.TemplateParam = fmt.Sprintf(`{"code":"%s"}`, code)
    
    response, err := p.client.SendSms(request)
    if err != nil {
        return fmt.Errorf("aliyun sms: %w", err)
    }
    
    if response.Code != "OK" {
        return fmt.Errorf("aliyun sms: code=%s, message=%s", response.Code, response.Message)
    }
    
    return nil
}
```

#### 腾讯云实现

```go
// pkg/sms/tencent.go
package sms

import (
    "context"
    "fmt"
    "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
    "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
    sms "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"
)

type TencentProvider struct {
    client     *sms.Client
    sdkAppID   string
    signName   string
    templateID string
}

func NewTencentProvider(secretID, secretKey, sdkAppID, signName, templateID string) (*TencentProvider, error) {
    credential := common.NewCredential(secretID, secretKey)
    cpf := profile.NewClientProfile()
    client, err := sms.NewClient(credential, "ap-guangzhou", cpf)
    if err != nil {
        return nil, fmt.Errorf("tencent sms: init failed: %w", err)
    }
    return &TencentProvider{
        client:     client,
        sdkAppID:   sdkAppID,
        signName:   signName,
        templateID: templateID,
    }, nil
}

func (p *TencentProvider) SendVerificationCode(ctx context.Context, phone, code string) error {
    request := sms.NewSendSmsRequest()
    request.SmsSdkAppId = common.StringPtr(p.sdkAppID)
    request.SignName = common.StringPtr(p.signName)
    request.TemplateId = common.StringPtr(p.templateID)
    request.PhoneNumberSet = common.StringPtrs([]string{phone})
    request.TemplateParamSet = common.StringPtrs([]string{code, "5"}) // code, 有效期（分钟）
    
    response, err := p.client.SendSms(request)
    if err != nil {
        return fmt.Errorf("tencent sms: %w", err)
    }
    
    if len(response.Response.SendStatusSet) == 0 {
        return fmt.Errorf("tencent sms: empty response")
    }
    
    status := response.Response.SendStatusSet[0]
    if *status.Code != "Ok" {
        return fmt.Errorf("tencent sms: code=%s, message=%s", *status.Code, *status.Message)
    }
    
    return nil
}
```

#### 配置示例

```bash
# 阿里云
SMS_PROVIDER=aliyun
SMS_ALIYUN_ACCESS_KEY_ID=LTAI***
SMS_ALIYUN_ACCESS_KEY_SECRET=***
SMS_ALIYUN_SIGN_NAME=盘古舆情
SMS_ALIYUN_TEMPLATE_CODE=SMS_123456789  # 验证码模板：您的验证码为${code},有效期5分钟

# 腾讯云
SMS_PROVIDER=tencent
SMS_TENCENT_SECRET_ID=AKI***
SMS_TENCENT_SECRET_KEY=***
SMS_TENCENT_SDK_APP_ID=1400***
SMS_TENCENT_SIGN_NAME=盘古舆情
SMS_TENCENT_TEMPLATE_ID=123456  # 验证码模板：您的验证码为{1},有效期{2}分钟
```

#### 申请流程对比

| 项目 | 阿里云 | 腾讯云 |
|------|--------|--------|
| **签名审核** | 1-2 工作日 | 1-2 工作日 |
| **模板审核** | 2 小时 | 2 小时 |
| **所需材料** | 营业执照 + 授权书 | 营业执照 + 授权书 |
| **单价（验证码）** | 0.045 元/条 | 0.04 元/条 |
| **免费额度** | 无 | 新用户 100 条 |
| **到达率** | >99% | >99% |

**建议**：优先申请腾讯云（便宜 + 有免费额度），阿里云作为备用。

### 3.3 本地头像存储（MVP）

#### 为什么不用 OSS？

✅ **用户决策明确**：MVP 本地存储，P1 再上 OSS（2026-09-22 确认）  
✅ **简化部署**：无需配置 OSS 凭据，无外部依赖  
✅ **成本优化**：预估 1000 用户 × 500KB = 500MB（SSD 成本可忽略）

#### 存储设计

```go
// pkg/storage/local.go
package storage

import (
    "fmt"
    "io"
    "os"
    "path/filepath"
)

type LocalStorage struct {
    basePath string
    baseURL  string
}

func NewLocalStorage(basePath, baseURL string) *LocalStorage {
    return &LocalStorage{
        basePath: basePath,
        baseURL:  baseURL,
    }
}

func (s *LocalStorage) SaveAvatar(userID string, data io.Reader) (string, error) {
    // 按用户 ID 前缀分片（避免单目录文件过多）
    // 例：01HF... → avatars/01/HF.../avatar.jpg
    dir := filepath.Join(s.basePath, "avatars", userID[:2], userID)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return "", fmt.Errorf("mkdir: %w", err)
    }
    
    filepath := filepath.Join(dir, "avatar.jpg")
    f, err := os.Create(filepath)
    if err != nil {
        return "", fmt.Errorf("create: %w", err)
    }
    defer f.Close()
    
    if _, err := io.Copy(f, data); err != nil {
        return "", fmt.Errorf("write: %w", err)
    }
    
    // 返回公开 URL
    url := fmt.Sprintf("%s/uploads/avatars/%s/%s/avatar.jpg", s.baseURL, userID[:2], userID)
    return url, nil
}
```

#### nginx 配置

```nginx
# /etc/nginx/sites-available/yuqing.conf
location /uploads/ {
    alias /opt/yuqing/uploads/;
    
    # 缓存策略：头像不常变，缓存 30 天
    expires 30d;
    add_header Cache-Control "public, immutable";
    
    # 安全：禁止目录遍历
    autoindex off;
    
    # CORS（如果前端域名不同）
    add_header Access-Control-Allow-Origin "$http_origin" always;
    add_header Access-Control-Allow-Methods "GET, OPTIONS" always;
}
```

#### 安全校验

```go
func (h *Handlers) UploadAvatar(c *gin.Context) {
    file, err := c.FormFile("file")
    if err != nil {
        respondError(c, errors.New("INVALID_FILE", "file required"))
        return
    }
    
    // 1. 文件大小限制（2MB）
    if file.Size > 2*1024*1024 {
        respondError(c, errors.New("FILE_TOO_LARGE", "avatar must be less than 2MB"))
        return
    }
    
    // 2. MIME type 校验
    contentType := file.Header.Get("Content-Type")
    if !strings.HasPrefix(contentType, "image/") {
        respondError(c, errors.New("INVALID_FILE_TYPE", "only images allowed"))
        return
    }
    
    // 3. 文件头魔数校验（防伪造 Content-Type）
    f, _ := file.Open()
    defer f.Close()
    header := make([]byte, 512)
    f.Read(header)
    detected := http.DetectContentType(header)
    if !strings.HasPrefix(detected, "image/") {
        respondError(c, errors.New("INVALID_FILE_TYPE", "file header mismatch"))
        return
    }
    
    // 4. 保存
    f.Seek(0, 0) // 重置读取位置
    url, err := h.storage.SaveAvatar(c.GetString("user_id"), f)
    if err != nil {
        respondError(c, errors.Wrap(err, "upload failed"))
        return
    }
    
    c.JSON(200, gin.H{"avatar_url": url})
}
```

#### 容量规划

| 预估用户量 | 平均头像大小 | 总存储需求 |
|-----------|-------------|-----------|
| 1,000 | 500 KB | 500 MB |
| 10,000 | 500 KB | 5 GB |
| 100,000 | 500 KB | 50 GB |

**服务器磁盘要求**：预留 20GB（含日志/数据库/备份）。

---

## § 4. P0 功能详细设计

### 4.1 修改密码（2 人天）

#### 后端实现

**Service 方法**（`platform/internal/platform/auth/service.go`）：

```go
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
    // 1. 获取用户
    user, err := s.store.GetUserByID(ctx, userID)
    if err != nil {
        return err
    }
    
    // 2. 验证旧密码
    if !VerifyPassword(user.PasswordHash, oldPassword) {
        return pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "old password incorrect")
    }
    
    // 3. 密码强度校验（用户决策：≥8 位 + 字母 + 数字）
    if err := validatePasswordStrength(newPassword); err != nil {
        return err
    }
    
    // 4. 生成新哈希
    newHash, err := HashPassword(newPassword)
    if err != nil {
        return pkgerrors.Wrap(err, "hash generation failed")
    }
    
    // 5. 更新密码 + 时间戳
    if err := s.store.UpdatePassword(ctx, userID, newHash); err != nil {
        return err
    }
    
    // 6. 撤销所有 refresh_token（强制重新登录）
    // MVP 内存 store：无持久 token 表，JWT 自验证无法撤销
    // TODO: PostgreSQL store 接入后实现 token 黑名单
    
    return nil
}

func validatePasswordStrength(password string) error {
    if len(password) < 8 {
        return pkgerrors.New("WEAK_PASSWORD", "password must be at least 8 characters")
    }
    
    hasLetter := false
    hasDigit := false
    for _, ch := range password {
        if unicode.IsLetter(ch) {
            hasLetter = true
        }
        if unicode.IsDigit(ch) {
            hasDigit = true
        }
    }
    
    if !hasLetter || !hasDigit {
        return pkgerrors.New("WEAK_PASSWORD", "password must contain letters and digits")
    }
    
    return nil
}
```

**Store 接口扩展**：

```go
// platform/internal/platform/auth/store.go
type Store interface {
    // ... 已有方法
    GetUserByID(ctx context.Context, userID string) (*User, error)
    UpdatePassword(ctx context.Context, userID, passwordHash string) error
}

// platform/internal/platform/auth/store_pg.go
func (s *pgStore) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
    query := `
        UPDATE users 
        SET password_hash = $1, password_changed_at = now() 
        WHERE id = $2
    `
    tag, err := s.pool.Exec(ctx, query, passwordHash, userID)
    if err != nil {
        return err
    }
    if tag.RowsAffected() == 0 {
        return pkgerrors.ErrNotFound
    }
    return nil
}
```

**API 端点**（`platform/internal/api/v1/auth.go`）：

```go
// PUT /api/v1/auth/password
func (h *Handlers) ChangePassword(c *gin.Context) {
    var req struct {
        OldPassword string `json:"old_password" binding:"required"`
        NewPassword string `json:"new_password" binding:"required,min=8"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, pkgerrors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id") // 从 JWT middleware 获取
    
    if err := h.services.Auth.ChangePassword(c.Request.Context(), userID, req.OldPassword, req.NewPassword); err != nil {
        respondError(c, err)
        return
    }
    
    c.JSON(200, gin.H{"message": "password changed successfully, please log in again"})
}
```

#### 前端实现

**Modal 组件**（`web/src/components/settings/ChangePasswordModal.tsx`）：

```tsx
import { useState } from 'react';
import { Form, Input, Button, Modal, message, Space } from 'antd';
import { LockOutlined } from '@ant-design/icons';
import { authApi } from '@/api/auth';
import { useAuthStore } from '@/stores/authStore';
import { useNavigate } from 'react-router-dom';

interface ChangePasswordModalProps {
  open: boolean;
  onClose: () => void;
}

export function ChangePasswordModal({ open, onClose }: ChangePasswordModalProps) {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  const logout = useAuthStore((state) => state.logout);
  const navigate = useNavigate();
  
  const handleSubmit = async (values: any) => {
    setLoading(true);
    try {
      await authApi.changePassword(values.oldPassword, values.newPassword);
      message.success('密码修改成功，请重新登录');
      
      // 清空本地 token 并跳转登录页
      logout();
      navigate('/login');
    } catch (err: any) {
      if (err.code === 'UNAUTHORIZED') {
        form.setFields([{ name: 'oldPassword', errors: ['旧密码错误'] }]);
      } else if (err.code === 'WEAK_PASSWORD') {
        form.setFields([{ name: 'newPassword', errors: [err.message] }]);
      } else {
        message.error(err.message || '修改失败，请稍后重试');
      }
    } finally {
      setLoading(false);
    }
  };
  
  return (
    <Modal
      title="修改密码"
      open={open}
      onCancel={onClose}
      footer={null}
      width={480}
    >
      <Form
        form={form}
        onFinish={handleSubmit}
        layout="vertical"
        autoComplete="off"
      >
        <Form.Item
          label="旧密码"
          name="oldPassword"
          rules={[{ required: true, message: '请输入旧密码' }]}
        >
          <Input.Password
            prefix={<LockOutlined />}
            placeholder="请输入旧密码"
            autoComplete="current-password"
          />
        </Form.Item>
        
        <Form.Item
          label="新密码"
          name="newPassword"
          rules={[
            { required: true, message: '请输入新密码' },
            { min: 8, message: '密码至少 8 位' },
            { 
              pattern: /^(?=.*[A-Za-z])(?=.*\d)/, 
              message: '密码必须包含字母和数字' 
            },
          ]}
          hasFeedback
        >
          <Input.Password
            prefix={<LockOutlined />}
            placeholder="至少 8 位，包含字母和数字"
            autoComplete="new-password"
          />
        </Form.Item>
        
        <Form.Item
          label="确认新密码"
          name="confirmPassword"
          dependencies={['newPassword']}
          hasFeedback
          rules={[
            { required: true, message: '请确认新密码' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('newPassword') === value) {
                  return Promise.resolve();
                }
                return Promise.reject(new Error('两次输入的密码不一致'));
              },
            }),
          ]}
        >
          <Input.Password
            prefix={<LockOutlined />}
            placeholder="再次输入新密码"
            autoComplete="new-password"
          />
        </Form.Item>
        
        <Form.Item style={{ marginBottom: 0 }}>
          <Space style={{ width: '100%', justifyContent: 'flex-end' }}>
            <Button onClick={onClose}>取消</Button>
            <Button type="primary" htmlType="submit" loading={loading}>
              确认修改
            </Button>
          </Space>
        </Form.Item>
      </Form>
    </Modal>
  );
}
```

**API 客户端**（`web/src/api/auth.ts`，新增方法）：

```typescript
export const authApi = {
  // ... 已有 login/register/refresh
  
  changePassword: async (oldPassword: string, newPassword: string): Promise<void> => {
    await client.put('/auth/password', { old_password: oldPassword, new_password: newPassword });
  },
};
```

#### 测试用例

**单元测试**（`platform/internal/platform/auth/service_test.go`）：

```go
func TestService_ChangePassword(t *testing.T) {
    tests := []struct {
        name        string
        oldPassword string
        newPassword string
        wantErr     bool
        errCode     string
    }{
        {
            name:        "成功修改",
            oldPassword: "OldPass123",
            newPassword: "NewPass456",
            wantErr:     false,
        },
        {
            name:        "旧密码错误",
            oldPassword: "WrongPass",
            newPassword: "NewPass456",
            wantErr:     true,
            errCode:     "UNAUTHORIZED",
        },
        {
            name:        "新密码太短",
            oldPassword: "OldPass123",
            newPassword: "123",
            wantErr:     true,
            errCode:     "WEAK_PASSWORD",
        },
        {
            name:        "新密码无字母",
            oldPassword: "OldPass123",
            newPassword: "12345678",
            wantErr:     true,
            errCode:     "WEAK_PASSWORD",
        },
        {
            name:        "新密码无数字",
            oldPassword: "OldPass123",
            newPassword: "NewPassword",
            wantErr:     true,
            errCode:     "WEAK_PASSWORD",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 1. 创建用户
            svc, store := setupTestService(t)
            user := createTestUser(t, store, "user@test.com", "OldPass123")
            
            // 2. 修改密码
            err := svc.ChangePassword(context.Background(), user.ID, tt.oldPassword, tt.newPassword)
            
            // 3. 断言
            if tt.wantErr {
                require.Error(t, err)
                assert.Contains(t, err.Error(), tt.errCode)
            } else {
                require.NoError(t, err)
                
                // 4. 验证新密码可用
                updatedUser, _ := store.GetUserByID(context.Background(), user.ID)
                assert.True(t, VerifyPassword(updatedUser.PasswordHash, tt.newPassword))
                
                // 5. 验证旧密码失效
                assert.False(t, VerifyPassword(updatedUser.PasswordHash, tt.oldPassword))
            }
        })
    }
}
```

**E2E 测试**（`web/e2e/user-center.spec.ts`）：

```typescript
test('修改密码后强制重新登录', async ({ page }) => {
  // 1. 登录
  await page.goto('/login');
  await page.fill('[name=email]', 'test@example.com');
  await page.fill('[name=password]', 'OldPass123');
  await page.click('button[type=submit]');
  await page.waitForURL('/dashboard');
  
  // 2. 打开用户中心 → 修改密码
  await page.goto('/settings/security');
  await page.click('text=修改密码');
  
  // 3. 填写表单
  await page.fill('[name=oldPassword]', 'OldPass123');
  await page.fill('[name=newPassword]', 'NewPass456');
  await page.fill('[name=confirmPassword]', 'NewPass456');
  await page.click('button:has-text("确认修改")');
  
  // 4. 断言跳转登录页
  await page.waitForURL('/login');
  await expect(page.locator('.ant-message')).toContainText('密码修改成功');
  
  // 5. 验证新密码可登录
  await page.fill('[name=email]', 'test@example.com');
  await page.fill('[name=password]', 'NewPass456');
  await page.click('button[type=submit]');
  await page.waitForURL('/dashboard');
});
```

---

### 4.2 邮箱验证（3 人天）

#### 业务流程

```
1. 用户注册 → email_verified_at = NULL, trial_analysis_used = 0
2. 用户创建分析 → 检查验证状态：
   - 已验证：正常创建
   - 未验证 + trial_analysis_used = 0：允许创建，创建后置 trial_analysis_used = 1
   - 未验证 + trial_analysis_used = 1：HTTP 403 TRIAL_USED，提示验证邮箱
3. 用户点击"发送验证邮件" → 生成 token（24h 过期）→ 发送邮件
4. 用户点击邮件链接 → GET /auth/verify-email?token=xxx → 标记 email_verified_at
```

#### 后端实现

**Service 方法**（`platform/internal/platform/auth/service.go`）：

```go
func (s *Service) SendVerificationEmail(ctx context.Context, userID string) error {
    user, err := s.store.GetUserByID(ctx, userID)
    if err != nil {
        return err
    }
    
    // 已验证则跳过
    if user.EmailVerifiedAt != nil {
        return pkgerrors.New("ALREADY_VERIFIED", "email already verified")
    }
    
    // 生成 token（ULID，24h 过期）
    token := id.New()
    expiresAt := time.Now().Add(24 * time.Hour)
    
    if err := s.store.CreateVerificationToken(ctx, userID, token, "email_verification", expiresAt); err != nil {
        // EXCLUDE 约束冲突 → 5 分钟内已发送
        if pkgerrors.Is(err, pkgerrors.ErrConflict) {
            return pkgerrors.New("RATE_LIMITED", "verification email already sent, please wait 5 minutes")
        }
        return err
    }
    
    // 发送邮件
    verifyURL := fmt.Sprintf("https://%s/verify-email?token=%s", s.domain, token)
    if err := s.emailProvider.SendVerificationEmail(ctx, user.Email, user.Name, verifyURL); err != nil {
        s.logger.Error("failed to send verification email", "user_id", userID, "error", err)
        return pkgerrors.Wrap(err, "email send failed")
    }
    
    return nil
}

func (s *Service) VerifyEmail(ctx context.Context, token string) error {
    // 1. 查找 token
    vt, err := s.store.GetVerificationToken(ctx, token)
    if err != nil {
        if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
            return pkgerrors.New("INVALID_TOKEN", "token not found or expired")
        }
        return err
    }
    
    // 2. 检查过期
    if time.Now().After(vt.ExpiresAt) {
        return pkgerrors.New("TOKEN_EXPIRED", "verification link expired, please request a new one")
    }
    
    // 3. 检查已使用
    if vt.UsedAt != nil {
        return pkgerrors.New("TOKEN_USED", "token already used")
    }
    
    // 4. 标记邮箱已验证
    if err := s.store.MarkEmailVerified(ctx, vt.UserID); err != nil {
        return err
    }
    
    // 5. 标记 token 已使用
    if err := s.store.MarkTokenUsed(ctx, token); err != nil {
        // 非关键路径，失败不阻塞
        s.logger.Warn("failed to mark token used", "token", token, "error", err)
    }
    
    return nil
}
```

**Store 接口扩展**：

```go
type VerificationToken struct {
    ID        string
    UserID    string
    Token     string
    Type      string
    ExpiresAt time.Time
    UsedAt    *time.Time
    CreatedAt time.Time
}

type Store interface {
    // ... 已有方法
    CreateVerificationToken(ctx context.Context, userID, token, typ string, expiresAt time.Time) error
    GetVerificationToken(ctx context.Context, token string) (*VerificationToken, error)
    MarkEmailVerified(ctx context.Context, userID string) error
    MarkTokenUsed(ctx context.Context, token string) error
}
```

**功能受限检查**（`platform/internal/business/analysis/service.go`）：

```go
func (s *Service) Create(ctx context.Context, req CreateAnalysisRequest) (*Analysis, error) {
    // 1. 检查邮箱验证状态（方案 B：允许试用 1 次）
    user, err := s.authStore.GetUserByID(ctx, req.UserID)
    if err != nil {
        return nil, err
    }
    
    if user.EmailVerifiedAt == nil {
        if user.TrialAnalysisUsed > 0 {
            return nil, pkgerrors.New("TRIAL_USED", 
                "email verification required to create more analyses, please verify your email first")
        }
        
        // 试用 1 次：创建分析，但标记已用
        if err := s.authStore.MarkTrialUsed(ctx, req.UserID); err != nil {
            s.logger.Warn("failed to mark trial used", "user_id", req.UserID, "error", err)
            // 不阻塞分析创建
        }
    }
    
    // 2. 继续原有逻辑...
    // ...
}
```

#### 前端实现

**验证入口**（`web/src/pages/SettingsProfilePage.tsx`）：

```tsx
export function SettingsProfilePage() {
  const { data: user } = useQuery({ queryKey: ['currentUser'], queryFn: userApi.getCurrentUser });
  const [sending, setSending] = useState(false);
  
  const handleSendVerification = async () => {
    setSending(true);
    try {
      await authApi.sendVerificationEmail();
      message.success('验证邮件已发送，请查收邮箱');
    } catch (err: any) {
      if (err.code === 'ALREADY_VERIFIED') {
        message.info('邮箱已验证');
      } else if (err.code === 'RATE_LIMITED') {
        message.warning('请 5 分钟后再试');
      } else {
        message.error(err.message);
      }
    } finally {
      setSending(false);
    }
  };
  
  return (
    <Card title="个人资料">
      {!user?.emailVerifiedAt && (
        <Alert
          type="warning"
          message="邮箱未验证"
          description="请验证邮箱以使用完整功能。未验证用户仅可创建 1 次分析任务。"
          action={
            <Button size="small" onClick={handleSendVerification} loading={sending}>
              发送验证邮件
            </Button>
          }
          closable
          style={{ marginBottom: 24 }}
        />
      )}
      
      {/* ... 资料表单 */}
    </Card>
  );
}
```

**验证页面**（`web/src/pages/VerifyEmailPage.tsx`）：

```tsx
import { useEffect, useState } from 'react';
import { useSearchParams, Link } from 'react-router-dom';
import { Result, Spin, Button } from 'antd';
import { CheckCircleOutlined, CloseCircleOutlined } from '@ant-design/icons';
import { authApi } from '@/api/auth';

export function VerifyEmailPage() {
  const [searchParams] = useSearchParams();
  const token = searchParams.get('token');
  const [status, setStatus] = useState<'verifying' | 'success' | 'error'>('verifying');
  const [error, setError] = useState('');
  
  useEffect(() => {
    if (!token) {
      setStatus('error');
      setError('验证链接无效');
      return;
    }
    
    authApi.verifyEmail(token)
      .then(() => setStatus('success'))
      .catch((err) => {
        setStatus('error');
        if (err.code === 'TOKEN_EXPIRED') {
          setError('验证链接已过期，请重新发送');
        } else if (err.code === 'TOKEN_USED') {
          setError('该链接已使用过');
        } else {
          setError(err.message || '验证失败');
        }
      });
  }, [token]);
  
  if (status === 'verifying') {
    return (
      <div style={{ textAlign: 'center', paddingTop: '20vh' }}>
        <Spin size="large" tip="正在验证邮箱..." />
      </div>
    );
  }
  
  if (status === 'success') {
    return (
      <Result
        icon={<CheckCircleOutlined style={{ color: '#52c41a' }} />}
        title="邮箱验证成功"
        subTitle="您现在可以使用完整功能了"
        extra={[
          <Button type="primary" key="dashboard">
            <Link to="/dashboard">前往控制台</Link>
          </Button>,
        ]}
      />
    );
  }
  
  return (
    <Result
      icon={<CloseCircleOutlined style={{ color: '#ff4d4f' }} />}
      title="验证失败"
      subTitle={error}
      extra={[
        <Button key="profile">
          <Link to="/settings/profile">返回个人资料</Link>
        </Button>,
        <Button type="primary" key="resend">
          <Link to="/settings/profile">重新发送验证邮件</Link>
        </Button>,
      ]}
    />
  );
}
```

**创建分析前检查**（`web/src/pages/AnalysisNewPage.tsx`）：

```tsx
const handleSubmit = async (values: any) => {
  // 1. 检查邮箱验证（前端提前提示，后端仍会校验）
  const currentUser = authStore.getState().user;
  if (!currentUser?.emailVerifiedAt && currentUser?.trialAnalysisUsed) {
    Modal.warning({
      title: '需要验证邮箱',
      content: '您的试用次数已用完，请先验证邮箱才能创建更多分析任务',
      okText: '去验证',
      onOk: () => navigate('/settings/profile'),
    });
    return;
  }
  
  // 2. 继续创建分析
  try {
    await analysisApi.create(values);
    message.success('分析任务已创建');
    navigate('/analyses');
  } catch (err: any) {
    if (err.code === 'TRIAL_USED') {
      // 后端拒绝
      Modal.error({
        title: '需要验证邮箱',
        content: err.message,
        okText: '去验证',
        onOk: () => navigate('/settings/profile'),
      });
    } else {
      message.error(err.message);
    }
  }
};
```

#### 测试用例

**集成测试**（`platform/test/integration/auth_test.go`）：

```go
func TestEmailVerificationFlow(t *testing.T) {
    // 1. 注册用户（未验证）
    user, _, err := authSvc.Register(ctx, "test@example.com", "Pass123", "Test User")
    require.NoError(t, err)
    assert.Nil(t, user.EmailVerifiedAt)
    
    // 2. 创建第 1 次分析（试用）
    analysis1, err := analysisSvc.Create(ctx, CreateAnalysisRequest{
        UserID: user.ID, TenantID: user.TenantID, Keyword: "test",
    })
    require.NoError(t, err)
    assert.NotNil(t, analysis1)
    
    // 3. 创建第 2 次分析（拒绝）
    _, err = analysisSvc.Create(ctx, CreateAnalysisRequest{
        UserID: user.ID, TenantID: user.TenantID, Keyword: "test2",
    })
    require.Error(t, err)
    assert.Contains(t, err.Error(), "TRIAL_USED")
    
    // 4. 发送验证邮件
    err = authSvc.SendVerificationEmail(ctx, user.ID)
    require.NoError(t, err)
    
    // 5. 获取 token（从内存 store）
    tokens, _ := authStore.ListVerificationTokens(ctx, user.ID)
    require.Len(t, tokens, 1)
    token := tokens[0].Token
    
    // 6. 验证邮箱
    err = authSvc.VerifyEmail(ctx, token)
    require.NoError(t, err)
    
    // 7. 验证后可创建分析
    analysis2, err := analysisSvc.Create(ctx, CreateAnalysisRequest{
        UserID: user.ID, TenantID: user.TenantID, Keyword: "test3",
    })
    require.NoError(t, err)
    assert.NotNil(t, analysis2)
}
```

---

### 4.3 个人资料编辑（3 人天）

#### 后端实现

**新建 user 包**（`platform/internal/platform/user/`）：

```go
// service.go
package user

import (
    "context"
    "fmt"
    "strings"
    "unicode/utf8"
    
    pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

type Profile struct {
    ID              string  `json:"id"`
    Email           string  `json:"email"`
    Name            string  `json:"name"`
    AvatarURL       string  `json:"avatar_url,omitempty"`
    Timezone        string  `json:"timezone"`
    Phone           string  `json:"phone,omitempty"`
    EmailVerifiedAt *string `json:"email_verified_at,omitempty"`
    PhoneVerifiedAt *string `json:"phone_verified_at,omitempty"`
}

type UpdateProfileRequest struct {
    Name      string `json:"name"`
    AvatarURL string `json:"avatar_url"`
    Timezone  string `json:"timezone"`
}

type Store interface {
    GetProfile(ctx context.Context, userID string) (*Profile, error)
    UpdateProfile(ctx context.Context, userID string, req UpdateProfileRequest) error
}

type Service struct {
    store Store
}

func NewService(store Store) *Service {
    return &Service{store: store}
}

func (s *Service) GetProfile(ctx context.Context, userID string) (*Profile, error) {
    return s.store.GetProfile(ctx, userID)
}

func (s *Service) UpdateProfile(ctx context.Context, userID string, req UpdateProfileRequest) error {
    // 1. 昵称校验
    name := strings.TrimSpace(req.Name)
    if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 20 {
        return pkgerrors.New("INVALID_NAME", "name must be 2-20 characters")
    }
    
    // 2. 时区校验
    validTimezones := []string{
        "Asia/Shanghai", "Asia/Hong_Kong", "Asia/Tokyo",
        "America/New_York", "Europe/London", "UTC",
    }
    if req.Timezone != "" && !contains(validTimezones, req.Timezone) {
        return pkgerrors.New("INVALID_TIMEZONE", "unsupported timezone")
    }
    
    // 3. 头像 URL 校验（可选）
    if req.AvatarURL != "" {
        if !strings.HasPrefix(req.AvatarURL, "http://") && !strings.HasPrefix(req.AvatarURL, "https://") {
            return pkgerrors.New("INVALID_AVATAR_URL", "avatar URL must be HTTP(S)")
        }
    }
    
    // 4. 更新
    return s.store.UpdateProfile(ctx, userID, req)
}

func contains(slice []string, item string) bool {
    for _, s := range slice {
        if s == item {
            return true
        }
    }
    return false
}
```

**Store 实现**（`platform/internal/platform/user/store_pg.go`）：

```go
package user

import (
    "context"
    "github.com/jackc/pgx/v5/pgxpool"
    pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

type pgStore struct {
    pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) Store {
    return &pgStore{pool: pool}
}

func (s *pgStore) GetProfile(ctx context.Context, userID string) (*Profile, error) {
    query := `
        SELECT id, email, name, avatar_url, timezone, phone,
               email_verified_at, phone_verified_at
        FROM users
        WHERE id = $1
    `
    var p Profile
    err := s.pool.QueryRow(ctx, query, userID).Scan(
        &p.ID, &p.Email, &p.Name, &p.AvatarURL, &p.Timezone, &p.Phone,
        &p.EmailVerifiedAt, &p.PhoneVerifiedAt,
    )
    if err != nil {
        return nil, pkgerrors.Wrap(pkgerrors.ErrNotFound, "user not found")
    }
    return &p, nil
}

func (s *pgStore) UpdateProfile(ctx context.Context, userID string, req UpdateProfileRequest) error {
    query := `
        UPDATE users
        SET name = $1, avatar_url = $2, timezone = $3
        WHERE id = $4
    `
    tag, err := s.pool.Exec(ctx, query, req.Name, req.AvatarURL, req.Timezone, userID)
    if err != nil {
        return err
    }
    if tag.RowsAffected() == 0 {
        return pkgerrors.ErrNotFound
    }
    return nil
}
```

**API 端点**（`platform/internal/api/v1/user.go`，全新文件）：

```go
package v1

import (
    "github.com/gin-gonic/gin"
    pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
    "github.com/yuqing/platform/internal/platform/user"
)

// GET /api/v1/user/profile
func (h *Handlers) GetProfile(c *gin.Context) {
    userID := c.GetString("user_id")
    
    profile, err := h.services.User.GetProfile(c.Request.Context(), userID)
    if err != nil {
        respondError(c, err)
        return
    }
    
    c.JSON(200, profile)
}

// PUT /api/v1/user/profile
func (h *Handlers) UpdateProfile(c *gin.Context) {
    var req user.UpdateProfileRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, pkgerrors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id")
    
    if err := h.services.User.UpdateProfile(c.Request.Context(), userID, req); err != nil {
        respondError(c, err)
        return
    }
    
    // 返回更新后的用户信息
    profile, _ := h.services.User.GetProfile(c.Request.Context(), userID)
    c.JSON(200, profile)
}

// POST /api/v1/user/avatar
func (h *Handlers) UploadAvatar(c *gin.Context) {
    file, err := c.FormFile("file")
    if err != nil {
        respondError(c, pkgerrors.New("INVALID_FILE", "file required"))
        return
    }
    
    // 1. 文件大小限制（2MB）
    if file.Size > 2*1024*1024 {
        respondError(c, pkgerrors.New("FILE_TOO_LARGE", "avatar must be less than 2MB"))
        return
    }
    
    // 2. MIME type 校验
    contentType := file.Header.Get("Content-Type")
    if !strings.HasPrefix(contentType, "image/") {
        respondError(c, pkgerrors.New("INVALID_FILE_TYPE", "only images allowed"))
        return
    }
    
    // 3. 打开文件并验证文件头
    f, err := file.Open()
    if err != nil {
        respondError(c, pkgerrors.Wrap(err, "open file failed"))
        return
    }
    defer f.Close()
    
    header := make([]byte, 512)
    f.Read(header)
    detected := http.DetectContentType(header)
    if !strings.HasPrefix(detected, "image/") {
        respondError(c, pkgerrors.New("INVALID_FILE_TYPE", "file header mismatch"))
        return
    }
    
    // 4. 保存到本地存储
    f.Seek(0, 0)
    userID := c.GetString("user_id")
    avatarURL, err := h.storage.SaveAvatar(userID, f)
    if err != nil {
        respondError(c, pkgerrors.Wrap(err, "upload failed"))
        return
    }
    
    c.JSON(200, gin.H{"avatar_url": avatarURL})
}
```

#### 前端实现

**页面组件**（`web/src/pages/SettingsProfilePage.tsx`）：

```tsx
import { useState } from 'react';
import { Card, Form, Input, Select, Button, Upload, Avatar, message, Space, Alert } from 'antd';
import { UploadOutlined, UserOutlined } from '@ant-design/icons';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { userApi } from '@/api/user';
import { authApi } from '@/api/auth';

export function SettingsProfilePage() {
  const [form] = Form.useForm();
  const queryClient = useQueryClient();
  const [avatarUrl, setAvatarUrl] = useState('');
  
  const { data: profile, isLoading } = useQuery({
    queryKey: ['profile'],
    queryFn: userApi.getProfile,
    onSuccess: (data) => {
      form.setFieldsValue({
        name: data.name,
        timezone: data.timezone || 'Asia/Shanghai',
      });
      setAvatarUrl(data.avatar_url || '');
    },
  });
  
  const updateMutation = useMutation({
    mutationFn: userApi.updateProfile,
    onSuccess: () => {
      message.success('保存成功');
      queryClient.invalidateQueries({ queryKey: ['profile'] });
    },
  });
  
  const uploadMutation = useMutation({
    mutationFn: userApi.uploadAvatar,
    onSuccess: (data) => {
      setAvatarUrl(data.avatar_url);
      message.success('头像上传成功');
    },
  });
  
  const handleAvatarUpload = async (file: File) => {
    const formData = new FormData();
    formData.append('file', file);
    uploadMutation.mutate(formData);
    return false; // 阻止默认上传
  };
  
  const handleSubmit = (values: any) => {
    updateMutation.mutate({
      name: values.name,
      avatar_url: avatarUrl,
      timezone: values.timezone,
    });
  };
  
  if (isLoading) return <Card loading />;
  
  return (
    <div className="settings-profile">
      {!profile?.email_verified_at && (
        <Alert
          type="warning"
          message="邮箱未验证"
          description="请验证邮箱以使用完整功能。未验证用户仅可创建 1 次分析任务。"
          action={
            <Button size="small" onClick={() => authApi.sendVerificationEmail()}>
              发送验证邮件
            </Button>
          }
          closable
          style={{ marginBottom: 24 }}
        />
      )}
      
      <Card title="个人资料">
        <Form
          form={form}
          onFinish={handleSubmit}
          layout="vertical"
          style={{ maxWidth: 600 }}
        >
          <Form.Item label="头像">
            <Space direction="vertical" align="center" style={{ width: '100%' }}>
              <Avatar
                size={120}
                src={avatarUrl}
                icon={!avatarUrl && <UserOutlined />}
              />
              <Upload
                beforeUpload={handleAvatarUpload}
                showUploadList={false}
                accept="image/jpeg,image/png,image/jpg"
              >
                <Button icon={<UploadOutlined />} loading={uploadMutation.isPending}>
                  点击上传
                </Button>
              </Upload>
              <div style={{ color: '#999', fontSize: 12 }}>
                支持 JPG、PNG 格式，文件小于 2MB
              </div>
            </Space>
          </Form.Item>
          
          <Form.Item
            label="昵称"
            name="name"
            rules={[
              { required: true, message: '请输入昵称' },
              { min: 2, max: 20, message: '昵称长度 2-20 个字符' },
            ]}
          >
            <Input placeholder="输入昵称" />
          </Form.Item>
          
          <Form.Item label="邮箱">
            <Input value={profile?.email} disabled addonAfter={
              profile?.email_verified_at ? (
                <span style={{ color: '#52c41a' }}>已验证</span>
              ) : (
                <span style={{ color: '#faad14' }}>未验证</span>
              )
            } />
          </Form.Item>
          
          <Form.Item label="手机号">
            {profile?.phone ? (
              <Input
                value={profile.phone.replace(/(\d{3})\d{4}(\d{4})/, '$1****$2')}
                disabled
                addonAfter={
                  <Button type="link" size="small" href="/settings/connections">
                    管理绑定
                  </Button>
                }
              />
            ) : (
              <Button type="dashed" block href="/settings/connections">
                绑定手机号
              </Button>
            )}
          </Form.Item>
          
          <Form.Item label="时区" name="timezone">
            <Select>
              <Select.Option value="Asia/Shanghai">北京时间 (UTC+8)</Select.Option>
              <Select.Option value="Asia/Hong_Kong">香港时间 (UTC+8)</Select.Option>
              <Select.Option value="Asia/Tokyo">东京时间 (UTC+9)</Select.Option>
              <Select.Option value="America/New_York">纽约时间 (UTC-5)</Select.Option>
              <Select.Option value="Europe/London">伦敦时间 (UTC+0)</Select.Option>
            </Select>
          </Form.Item>
          
          <Form.Item>
            <Space>
              <Button onClick={() => form.resetFields()}>取消</Button>
              <Button
                type="primary"
                htmlType="submit"
                loading={updateMutation.isPending}
              >
                保存更改
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
}
```

**API 客户端**（`web/src/api/user.ts`，全新文件）：

```typescript
import { client } from './client';

export interface Profile {
  id: string;
  email: string;
  name: string;
  avatar_url?: string;
  timezone: string;
  phone?: string;
  email_verified_at?: string;
  phone_verified_at?: string;
}

export interface UpdateProfileRequest {
  name: string;
  avatar_url: string;
  timezone: string;
}

export const userApi = {
  getProfile: async (): Promise<Profile> => {
    const res = await client.get('/user/profile');
    return res.data;
  },
  
  updateProfile: async (req: UpdateProfileRequest): Promise<Profile> => {
    const res = await client.put('/user/profile', req);
    return res.data;
  },
  
  uploadAvatar: async (formData: FormData): Promise<{ avatar_url: string }> => {
    const res = await client.post('/user/avatar', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
    });
    return res.data;
  },
};
```

---

### 4.4 手机号绑定（6 人天）

#### Redis 验证码存储

```go
// pkg/cache/redis.go
package cache

import (
    "context"
    "fmt"
    "time"
    "github.com/redis/go-redis/v9"
)

type RedisCache struct {
    client *redis.Client
}

func NewRedisCache(addr, password string) *RedisCache {
    return &RedisCache{
        client: redis.NewClient(&redis.Options{
            Addr:     addr,
            Password: password,
            DB:       0,
        }),
    }
}

func (c *RedisCache) SetVerificationCode(ctx context.Context, phone, code string, ttl time.Duration) error {
    key := fmt.Sprintf("sms_code:%s", phone)
    return c.client.Set(ctx, key, code, ttl).Err()
}

func (c *RedisCache) GetVerificationCode(ctx context.Context, phone string) (string, error) {
    key := fmt.Sprintf("sms_code:%s", phone)
    return c.client.Get(ctx, key).Result()
}

func (c *RedisCache) DeleteVerificationCode(ctx context.Context, phone string) error {
    key := fmt.Sprintf("sms_code:%s", phone)
    return c.client.Del(ctx, key).Err()
}
```

#### 后端实现

**Service 方法**（`platform/internal/platform/user/service.go`，扩展）：

```go
type Service struct {
    store       Store
    smsProvider sms.Provider
    cache       cache.Cache
}

func (s *Service) SendPhoneVerificationCode(ctx context.Context, userID, phone string) error {
    // 1. 校验手机号格式（中国大陆）
    if !isValidChinaPhone(phone) {
        return pkgerrors.New("INVALID_PHONE", "invalid phone number format")
    }
    
    // 2. 检查是否已被其他用户绑定
    existingUser, _ := s.store.GetUserByPhone(ctx, phone)
    if existingUser != nil && existingUser.ID != userID {
        return pkgerrors.New("PHONE_TAKEN", "phone number already bound to another account")
    }
    
    // 3. 生成 6 位验证码
    code := generateRandomCode(6)
    
    // 4. 存储验证码（Redis，5 分钟过期）
    if err := s.cache.SetVerificationCode(ctx, phone, code, 5*time.Minute); err != nil {
        return pkgerrors.Wrap(err, "cache failed")
    }
    
    // 5. 发送短信
    if err := s.smsProvider.SendVerificationCode(ctx, phone, code); err != nil {
        return pkgerrors.Wrap(err, "SMS send failed")
    }
    
    return nil
}

func (s *Service) BindPhone(ctx context.Context, userID, phone, code string) error {
    // 1. 验证验证码
    storedCode, err := s.cache.GetVerificationCode(ctx, phone)
    if err != nil || storedCode != code {
        return pkgerrors.New("INVALID_CODE", "verification code incorrect or expired")
    }
    
    // 2. 再次检查手机号是否被占用（防止并发）
    existingUser, _ := s.store.GetUserByPhone(ctx, phone)
    if existingUser != nil && existingUser.ID != userID {
        return pkgerrors.New("PHONE_TAKEN", "phone number already bound")
    }
    
    // 3. 绑定手机号
    if err := s.store.BindPhone(ctx, userID, phone); err != nil {
        return err
    }
    
    // 4. 删除验证码
    s.cache.DeleteVerificationCode(ctx, phone)
    
    return nil
}

func (s *Service) UnbindPhone(ctx context.Context, userID, password string) error {
    // 1. 获取用户
    user, err := s.store.GetUser(ctx, userID)
    if err != nil {
        return err
    }
    
    // 2. 验证密码（防会话劫持）
    if !auth.VerifyPassword(user.PasswordHash, password) {
        return pkgerrors.New("INVALID_PASSWORD", "password incorrect")
    }
    
    // 3. 检查是否为唯一登录方式
    if user.EmailVerifiedAt == nil && user.Phone != "" {
        // TODO: 检查是否有 OAuth 绑定
        return pkgerrors.New("CANNOT_UNBIND", "phone is your only login method, please verify email first")
    }
    
    // 4. 解绑
    return s.store.UnbindPhone(ctx, userID)
}

func isValidChinaPhone(phone string) bool {
    matched, _ := regexp.MatchString(`^1[3-9]\d{9}$`, phone)
    return matched
}

func generateRandomCode(length int) string {
    const digits = "0123456789"
    b := make([]byte, length)
    for i := range b {
        n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
        b[i] = digits[n.Int64()]
    }
    return string(b)
}
```

**Store 接口扩展**：

```go
type Store interface {
    // ... 已有方法
    GetUserByPhone(ctx context.Context, phone string) (*User, error)
    BindPhone(ctx context.Context, userID, phone string) error
    UnbindPhone(ctx context.Context, userID string) error
    GetUser(ctx context.Context, userID string) (*User, error)
}
```

**API 端点**（`platform/internal/api/v1/user.go`，扩展）：

```go
// POST /api/v1/user/phone/send-code
func (h *Handlers) SendPhoneCode(c *gin.Context) {
    var req struct {
        Phone string `json:"phone" binding:"required"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, pkgerrors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id")
    
    if err := h.services.User.SendPhoneVerificationCode(c.Request.Context(), userID, req.Phone); err != nil {
        respondError(c, err)
        return
    }
    
    c.JSON(200, gin.H{"message": "verification code sent", "expires_in": 300})
}

// POST /api/v1/user/phone/bind
func (h *Handlers) BindPhone(c *gin.Context) {
    var req struct {
        Phone string `json:"phone" binding:"required"`
        Code  string `json:"code" binding:"required,len=6"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, pkgerrors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id")
    
    if err := h.services.User.BindPhone(c.Request.Context(), userID, req.Phone, req.Code); err != nil {
        respondError(c, err)
        return
    }
    
    profile, _ := h.services.User.GetProfile(c.Request.Context(), userID)
    c.JSON(200, profile)
}

// POST /api/v1/user/phone/unbind
func (h *Handlers) UnbindPhone(c *gin.Context) {
    var req struct {
        Password string `json:"password" binding:"required"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, pkgerrors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id")
    
    if err := h.services.User.UnbindPhone(c.Request.Context(), userID, req.Password); err != nil {
        respondError(c, err)
        return
    }
    
    c.JSON(200, gin.H{"message": "phone unbound successfully"})
}
```

#### 前端实现

**绑定 Modal**（`web/src/components/settings/BindPhoneModal.tsx`）：

```tsx
import { useState, useEffect } from 'react';
import { Modal, Form, Input, Button, Select, Space, Alert, message } from 'antd';
import { useMutation } from '@tanstack/react-query';
import { userApi } from '@/api/user';

interface Props {
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
}

export function BindPhoneModal({ open, onClose, onSuccess }: Props) {
  const [step, setStep] = useState<1 | 2>(1);
  const [phone, setPhone] = useState('');
  const [countdown, setCountdown] = useState(0);
  const [form] = Form.useForm();
  
  const sendCodeMutation = useMutation({
    mutationFn: (phone: string) => userApi.sendPhoneCode(phone),
    onSuccess: () => {
      message.success('验证码已发送');
      setCountdown(60);
      setStep(2);
    },
    onError: (err: any) => {
      if (err.code === 'PHONE_TAKEN') {
        message.error('该手机号已被绑定');
      } else {
        message.error(err.message);
      }
    },
  });
  
  const bindMutation = useMutation({
    mutationFn: ({ phone, code }: { phone: string; code: string }) =>
      userApi.bindPhone(phone, code),
    onSuccess: () => {
      message.success('绑定成功');
      onSuccess();
      onClose();
      form.resetFields();
      setStep(1);
    },
    onError: (err: any) => {
      if (err.code === 'INVALID_CODE') {
        form.setFields([{ name: 'code', errors: ['验证码错误或已过期'] }]);
      } else {
        message.error(err.message);
      }
    },
  });
  
  useEffect(() => {
    if (countdown > 0) {
      const timer = setTimeout(() => setCountdown(countdown - 1), 1000);
      return () => clearTimeout(timer);
    }
  }, [countdown]);
  
  const handleSendCode = () => {
    form.validateFields(['phone']).then(({ phone }) => {
      setPhone(phone);
      sendCodeMutation.mutate(phone);
    });
  };
  
  const handleConfirm = () => {
    form.validateFields(['code']).then(({ code }) => {
      bindMutation.mutate({ phone, code });
    });
  };
  
  const handleCancel = () => {
    onClose();
    form.resetFields();
    setStep(1);
    setCountdown(0);
  };
  
  return (
    <Modal
      title={`绑定手机号 - 步骤 ${step}/2`}
      open={open}
      onCancel={handleCancel}
      footer={null}
      width={480}
    >
      <Form form={form} layout="vertical">
        {step === 1 && (
          <>
            <Form.Item
              label="手机号"
              name="phone"
              rules={[
                { required: true, message: '请输入手机号' },
                { pattern: /^1[3-9]\d{9}$/, message: '手机号格式不正确' },
              ]}
            >
              <Input
                addonBefore={
                  <Select defaultValue="+86" style={{ width: 70 }}>
                    <Select.Option value="+86">+86</Select.Option>
                  </Select>
                }
                placeholder="请输入手机号"
                maxLength={11}
              />
            </Form.Item>
            
            <Form.Item>
              <Button
                type="primary"
                onClick={handleSendCode}
                loading={sendCodeMutation.isPending}
                block
              >
                发送验证码
              </Button>
            </Form.Item>
          </>
        )}
        
        {step === 2 && (
          <>
            <Alert
              message={`验证码已发送至 ${phone.replace(/(\d{3})\d{4}(\d{4})/, '$1****$2')}`}
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
            />
            
            <Form.Item
              label="验证码"
              name="code"
              rules={[
                { required: true, message: '请输入验证码' },
                { len: 6, message: '验证码为 6 位数字' },
              ]}
            >
              <Input placeholder="请输入验证码" maxLength={6} />
            </Form.Item>
            
            <div style={{ marginBottom: 16, color: '#999' }}>
              {countdown > 0 ? (
                `${countdown}s 后可重新发送`
              ) : (
                <Button type="link" size="small" onClick={handleSendCode} style={{ padding: 0 }}>
                  重新发送验证码
                </Button>
              )}
            </div>
            
            <Alert
              message="绑定后可用于"
              description={
                <ul style={{ marginBottom: 0, paddingLeft: 20 }}>
                  <li>手机号登录</li>
                  <li>找回密码</li>
                  <li>支付验证</li>
                </ul>
              }
              type="info"
              style={{ marginBottom: 16 }}
            />
            
            <Form.Item style={{ marginBottom: 0 }}>
              <Space style={{ width: '100%', justifyContent: 'flex-end' }}>
                <Button onClick={() => setStep(1)}>上一步</Button>
                <Button
                  type="primary"
                  onClick={handleConfirm}
                  loading={bindMutation.isPending}
                >
                  确认绑定
                </Button>
              </Space>
            </Form.Item>
          </>
        )}
      </Form>
    </Modal>
  );
}
```

**解绑确认 Modal**（`web/src/components/settings/UnbindPhoneModal.tsx`）：

```tsx
import { Form, Input, Button, Modal, Alert, Space, message } from 'antd';
import { useMutation } from '@tanstack/react-query';
import { userApi } from '@/api/user';

interface Props {
  phone: string;
  open: boolean;
  onClose: () => void;
  onSuccess: () => void;
}

export function UnbindPhoneModal({ phone, open, onClose, onSuccess }: Props) {
  const [form] = Form.useForm();
  
  const unbindMutation = useMutation({
    mutationFn: (password: string) => userApi.unbindPhone(password),
    onSuccess: () => {
      message.success('已解绑');
      onSuccess();
      onClose();
      form.resetFields();
    },
    onError: (err: any) => {
      if (err.code === 'INVALID_PASSWORD') {
        form.setFields([{ name: 'password', errors: ['密码错误'] }]);
      } else if (err.code === 'CANNOT_UNBIND') {
        message.error('手机号是您唯一的登录方式，请先验证邮箱或绑定其他登录方式');
      } else {
        message.error(err.message);
      }
    },
  });
  
  const handleSubmit = (values: any) => {
    unbindMutation.mutate(values.password);
  };
  
  return (
    <Modal
      title="确认解绑手机号"
      open={open}
      onCancel={() => {
        onClose();
        form.resetFields();
      }}
      footer={null}
      width={480}
    >
      <Alert
        type="warning"
        message="解绑后将无法使用手机号登录和找回密码"
        style={{ marginBottom: 16 }}
      />
      
      <Form form={form} onFinish={handleSubmit} layout="vertical">
        <div style={{ marginBottom: 16 }}>
          即将解绑手机号：<strong>{phone.replace(/(\d{3})\d{4}(\d{4})/, '$1****$2')}</strong>
        </div>
        
        <Form.Item
          label="为了账户安全,请输入登录密码确认"
          name="password"
          rules={[{ required: true, message: '请输入密码' }]}
        >
          <Input.Password placeholder="请输入登录密码" />
        </Form.Item>
        
        <Form.Item style={{ marginBottom: 0 }}>
          <Space style={{ width: '100%', justifyContent: 'flex-end' }}>
            <Button onClick={onClose}>取消</Button>
            <Button
              type="primary"
              danger
              htmlType="submit"
              loading={unbindMutation.isPending}
            >
              确认解绑
            </Button>
          </Space>
        </Form.Item>
      </Form>
    </Modal>
  );
}
```

---

### 4.5 套餐与余额页（2 人天）

**说明**：后端 API 已完整（`billing.Service` + `credit.Service`），只需前端页面。

#### 前端实现

**页面组件**（`web/src/pages/SettingsBillingPage.tsx`）：

```tsx
import { Card, Descriptions, Tag, Statistic, Alert, Button, Space, Progress } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { billingApi } from '@/api/billing';
import { Link } from 'react-router-dom';

export function SettingsBillingPage() {
  const { data: credits, isLoading: creditsLoading } = useQuery({
    queryKey: ['credits'],
    queryFn: billingApi.getCredits,
  });
  
  const { data: plansData, isLoading: plansLoading } = useQuery({
    queryKey: ['plans'],
    queryFn: billingApi.getPlans,
  });
  
  const currentPlan = plansData?.plans.find((p) => p.code === credits?.plan_code);
  const isLowBalance = (credits?.balance || 0) <= 2;
  const balancePercent = currentPlan
    ? Math.round(((credits?.balance || 0) / currentPlan.credits_per_cycle) * 100)
    : 0;
  
  if (creditsLoading || plansLoading) return <Card loading />;
  
  return (
    <div className="settings-billing">
      <Card title="当前套餐" style={{ marginBottom: 24 }}>
        <Descriptions column={2} bordered>
          <Descriptions.Item label="套餐名称">
            <Tag color="blue" style={{ fontSize: 14 }}>{currentPlan?.name}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="月费">
            ¥{((currentPlan?.price_monthly_cny || 0) / 100).toFixed(2)}
          </Descriptions.Item>
          <Descriptions.Item label="分析模式">
            {currentPlan?.analysis_mode === 'quick' ? (
              <Tag>速览版（3 维）</Tag>
            ) : (
              <Tag color="gold">完整版（5 维）</Tag>
            )}
          </Descriptions.Item>
          <Descriptions.Item label="周期额度">
            {currentPlan?.credits_per_cycle} 次/月
          </Descriptions.Item>
          <Descriptions.Item label="最大并发">
            {currentPlan?.max_concurrent} 个任务
          </Descriptions.Item>
          <Descriptions.Item label="数据保留">
            {currentPlan?.retention_days} 天
          </Descriptions.Item>
        </Descriptions>
        
        <div style={{ marginTop: 16 }}>
          <Space>
            {credits?.plan_code !== 'enterprise' && (
              <Link to="/plans">
                <Button type="primary">升级套餐</Button>
              </Link>
            )}
            <Link to="/billing/history">
              <Button>查看账单</Button>
            </Link>
          </Space>
        </div>
      </Card>
      
      <Card title="额度余额">
        <Statistic
          title="剩余次数"
          value={credits?.balance || 0}
          suffix="次"
          valueStyle={{ color: isLowBalance ? '#cf1322' : '#3f8600', fontSize: 36 }}
        />
        
        <Progress
          percent={balancePercent}
          status={isLowBalance ? 'exception' : 'active'}
          style={{ marginTop: 16 }}
        />
        
        {credits?.balance === 0 && (
          <Alert
            type="error"
            message="额度已用完"
            description="请升级套餐或购买加购包以继续使用"
            action={
              <Space direction="vertical" size="small">
                <Link to="/plans">
                  <Button size="small" type="primary" block>
                    升级套餐
                  </Button>
                </Link>
                <Link to="/billing/addon">
                  <Button size="small" block>
                    购买加购包
                  </Button>
                </Link>
              </Space>
            }
            style={{ marginTop: 16 }}
          />
        )}
        
        {isLowBalance && credits?.balance > 0 && (
          <Alert
            type="warning"
            message="额度即将用完"
            description={`您还剩 ${credits.balance} 次分析机会，建议提前充值`}
            action={
              <Link to="/plans">
                <Button size="small" type="primary">
                  立即充值
                </Button>
              </Link>
            }
            style={{ marginTop: 16 }}
          />
        )}
        
        <div style={{ marginTop: 24, color: '#999', fontSize: 12 }}>
          <p>• 每次创建/重新运行分析消耗 1 次额度</p>
          <p>• 分析失败/取消会自动回补额度</p>
          <p>• 套餐额度每月 1 日刷新，不可累积</p>
        </div>
      </Card>
      
      <Card title="快捷操作" style={{ marginTop: 24 }}>
        <Space direction="vertical" style={{ width: '100%' }}>
          <Link to="/billing/transactions">
            <Button block>查看额度明细</Button>
          </Link>
          <Link to="/billing/invoices">
            <Button block>下载发票</Button>
          </Link>
          {currentPlan?.code === 'enterprise' && (
            <Link to="/settings/team">
              <Button block>管理团队成员</Button>
            </Link>
          )}
        </Space>
      </Card>
    </div>
  );
}
```

**API 客户端**（`web/src/api/billing.ts`，已有，无需修改）：

```typescript
export interface Credits {
  balance: number;
  plan_code: string;
}

export interface Plan {
  code: string;
  name: string;
  price_monthly_cny: number;
  credits_per_cycle: number;
  analysis_mode: string;
  max_concurrent: number;
  retention_days: number;
}

export const billingApi = {
  getCredits: async (): Promise<Credits> => {
    const res = await client.get('/billing/credits');
    return res.data;
  },
  
  getPlans: async (): Promise<{ plans: Plan[] }> => {
    const res = await client.get('/billing/plans');
    return res.data;
  },
};
```

---

## § 5. 安全与权限

### 5.1 密码强度校验

**正则表达式**（用户决策：≥8 位 + 字母 + 数字）：

```regex
^(?=.*[A-Za-z])(?=.*\d)[A-Za-z\d]{8,}$
```

**Go 实现**：

```go
func validatePasswordStrength(password string) error {
    if len(password) < 8 {
        return pkgerrors.New("WEAK_PASSWORD", "password must be at least 8 characters")
    }
    
    hasLetter := false
    hasDigit := false
    for _, ch := range password {
        if unicode.IsLetter(ch) {
            hasLetter = true
        }
        if unicode.IsDigit(ch) {
            hasDigit = true
        }
    }
    
    if !hasLetter || !hasDigit {
        return pkgerrors.New("WEAK_PASSWORD", "password must contain letters and digits")
    }
    
    return nil
}
```

**前端实时反馈**：

```tsx
const getPasswordStrength = (password: string): { level: number; text: string; color: string } => {
  if (!password) return { level: 0, text: '', color: '' };
  
  let score = 0;
  if (password.length >= 8) score++;
  if (password.length >= 12) score++;
  if (/[a-z]/.test(password) && /[A-Z]/.test(password)) score++;
  if (/\d/.test(password)) score++;
  if (/[^a-zA-Z0-9]/.test(password)) score++;
  
  if (score <= 1) return { level: 1, text: '弱', color: '#ff4d4f' };
  if (score <= 2) return { level: 2, text: '中等', color: '#faad14' };
  if (score <= 3) return { level: 3, text: '强', color: '#52c41a' };
  return { level: 4, text: '很强', color: '#1890ff' };
};
```

### 5.2 邮箱验证 Token 防刷

**机制**：PostgreSQL EXCLUDE 约束 + 5 分钟窗口

```sql
CONSTRAINT unique_user_type_recent 
    EXCLUDE USING btree (user_id WITH =, type WITH =) 
    WHERE (created_at > now() - interval '5 minutes' AND used_at IS NULL)
```

**效果**：同一用户 5 分钟内只能创建 1 个同类型 token（未使用状态）

### 5.3 手机验证码防刷

**机制 1**：前端 60 秒倒计时，按钮置灰

```tsx
const [countdown, setCountdown] = useState(0);

useEffect(() => {
  if (countdown > 0) {
    const timer = setTimeout(() => setCountdown(countdown - 1), 1000);
    return () => clearTimeout(timer);
  }
}, [countdown]);

<Button disabled={countdown > 0} onClick={handleSendCode}>
  {countdown > 0 ? `${countdown}s 后重试` : '发送验证码'}
</Button>
```

**机制 2**：后端 Redis 限流（可选，P1）

```go
func (s *Service) SendPhoneVerificationCode(ctx context.Context, userID, phone string) error {
    // 检查发送频率
    key := fmt.Sprintf("sms_rate:%s", phone)
    count, _ := s.cache.Incr(ctx, key)
    if count == 1 {
        s.cache.Expire(ctx, key, time.Minute)
    }
    if count > 5 {
        return pkgerrors.New("RATE_LIMITED", "too many requests, please try again later")
    }
    
    // ... 继续发送逻辑
}
```

### 5.4 解绑二次确认

**手机号解绑**：需输入登录密码

```go
func (s *Service) UnbindPhone(ctx context.Context, userID, password string) error {
    user, _ := s.store.GetUser(ctx, userID)
    
    // 密码验证
    if !auth.VerifyPassword(user.PasswordHash, password) {
        return pkgerrors.New("INVALID_PASSWORD", "password incorrect")
    }
    
    // 唯一登录方式保护
    if user.EmailVerifiedAt == nil && user.Phone != "" {
        return pkgerrors.New("CANNOT_UNBIND", "phone is your only login method")
    }
    
    return s.store.UnbindPhone(ctx, userID)
}
```

### 5.5 唯一登录方式保护

**规则**：用户必须至少保留 1 种登录方式（已验证邮箱 OR 手机号 OR OAuth）

**实现**：

```go
func (s *Service) canUnbindLoginMethod(ctx context.Context, userID string, method string) (bool, error) {
    user, _ := s.store.GetUser(ctx, userID)
    
    loginMethods := 0
    if user.EmailVerifiedAt != nil {
        loginMethods++
    }
    if user.Phone != "" {
        loginMethods++
    }
    
    // TODO: 检查 OAuth 绑定
    // oauthCount, _ := s.oauthStore.CountConnections(ctx, userID)
    // loginMethods += oauthCount
    
    // 解绑后仍有其他方式
    if method == "phone" && loginMethods > 1 {
        return true, nil
    }
    
    return false, pkgerrors.New("CANNOT_UNBIND", "cannot unbind your only login method")
}
```

### 5.6 API Key 权限隔离

**RBAC 检查**（仅 `tenant_admin` 可管理 API Key）：

```go
// middleware/rbac.go
func RequirePermission(permission string) gin.HandlerFunc {
    return func(c *gin.Context) {
        roles := c.GetStringSlice("roles")
        
        hasPermission := false
        for _, role := range roles {
            if auth.RoleHasPermission(role, permission) {
                hasPermission = true
                break
            }
        }
        
        if !hasPermission {
            respondError(c, pkgerrors.New("FORBIDDEN", "insufficient permissions"))
            c.Abort()
            return
        }
        
        c.Next()
    }
}

// 路由注册
apiKeysGroup := v1.Group("/api-keys")
apiKeysGroup.Use(middleware.RequirePermission("apikeys:manage"))
{
    apiKeysGroup.POST("", handlers.CreateAPIKey)
    apiKeysGroup.GET("", handlers.ListAPIKeys)
    apiKeysGroup.DELETE("/:id", handlers.RevokeAPIKey)
}
```

**前端条件渲染**：

```tsx
export function SettingsLayout() {
  const user = useAuthStore((state) => state.user);
  const isTenantAdmin = user?.roles.includes('tenant_admin');
  
  return (
    <Layout>
      <Sider>
        <Menu>
          <Menu.Item key="profile">个人资料</Menu.Item>
          <Menu.Item key="security">账户安全</Menu.Item>
          <Menu.Item key="connections">绑定与集成</Menu.Item>
          {isTenantAdmin && (
            <Menu.Item key="api-keys">API 管理</Menu.Item>
          )}
          <Menu.Item key="billing">套餐与订阅</Menu.Item>
        </Menu>
      </Sider>
      <Content><Outlet /></Content>
    </Layout>
  );
}
```

---

## § 6. 实施计划

### Sprint 1（3 天）：基础设施 + 修改密码 + 邮箱验证

**Day 1**：
- [ ] 数据库迁移 0007（users 字段 + verification_tokens/login_sessions 表）
- [ ] Resend.com 账号申请 + 域名配置（DNS 记录）
- [ ] `pkg/email` 邮件发送模块（Resend SDK 集成）
- [ ] 单元测试：邮件发送成功/失败/重试

**Day 2**：
- [ ] `auth.Service.ChangePassword` 实现 + Store 扩展
- [ ] `auth.Service.SendVerificationEmail` / `VerifyEmail` 实现
- [ ] API 端点：PUT /auth/password, POST /auth/send-verification-email, GET /auth/verify-email
- [ ] 单元测试：密码强度校验/旧密码验证/token 过期/已使用

**Day 3**：
- [ ] 前端：ChangePasswordModal 组件
- [ ] 前端：VerifyEmailPage + 验证入口（Alert）
- [ ] 前端：功能受限检查（创建分析前）+ 试用 1 次逻辑
- [ ] E2E 测试：注册 → 未验证创建分析（1 次）→ 第 2 次拒绝 → 验证 → 创建成功

### Sprint 2（2 天）：个人资料编辑

**Day 4**：
- [ ] 新建 `user` 包（`platform/internal/platform/user/`）
- [ ] `user.Service.UpdateProfile` / `GetProfile` 实现
- [ ] Store 接口 + memory/postgres 实现
- [ ] API 端点：PUT /user/profile, GET /user/profile, POST /user/avatar
- [ ] `pkg/storage/local.go` 本地存储实现
- [ ] 单元测试：昵称校验/时区校验/头像上传

**Day 5**：
- [ ] 前端：SettingsProfilePage 完整实现
- [ ] 前端：头像上传组件（Ant Design Upload）
- [ ] nginx 静态文件配置（/uploads/ location + 缓存策略）
- [ ] E2E 测试：修改昵称 → 上传头像 → 切换时区 → 刷新验证

### Sprint 3（3 天）：手机号绑定 + 套餐余额页

**Day 6**：
- [ ] SMS 服务申请（阿里云/腾讯云签名 + 模板审核）
- [ ] `pkg/sms` SMS 发送模块（interface + 双实现）
- [ ] `pkg/cache/redis.go` 验证码存储
- [ ] `user.Service` 手机号方法（SendPhoneCode / BindPhone / UnbindPhone）
- [ ] 单元测试：验证码生成/校验/过期/手机号占用

**Day 7**：
- [ ] API 端点：POST /user/phone/send-code, POST /user/phone/bind, POST /user/phone/unbind
- [ ] 前端：BindPhoneModal（2 步流程 + 60 秒倒计时）
- [ ] 前端：UnbindPhoneModal（密码确认 + 唯一登录方式保护）
- [ ] E2E 测试：绑定手机 → 解绑手机 → 唯一登录方式拒绝

**Day 8**：
- [ ] 前端：SettingsBillingPage（套餐信息 + 额度显示 + 进度条）
- [ ] 前端：SettingsLayout（左侧导航 + 路由配置）
- [ ] 前端：条件渲染（API Key 管理仅 tenant_admin 可见）
- [ ] E2E 测试：完整流程（修改密码 → 邮箱验证 → 编辑资料 → 绑定手机 → 查看余额）

---

## § 7. 风险与依赖

### 7.1 外部服务依赖

| 服务 | 风险 | 缓解措施 |
|------|------|----------|
| **Resend.com** | 国内可达性不稳定 | • 实测延迟 200-300ms（可接受）<br>• 备用方案：SMTP fallback（阿里云邮件推送） |
| **阿里云 SMS** | 签名审核失败 | • 提前准备营业执照 + 授权书<br>• 预留 3-5 工作日审核时间 |
| **腾讯云 SMS** | 同上 | • 双供应商策略：同时申请阿里云 + 腾讯云 |

### 7.2 技术实施风险

| 风险项 | 影响 | 缓解措施 |
|--------|------|----------|
| **邮箱验证"试用 1 次"实现复杂度** | Day +0.5 | • `users.trial_analysis_used` 字段已设计<br>• 业务逻辑清晰（Create 时检查 + 标记） |
| **PostgreSQL EXCLUDE 约束兼容性** | PG < 9.0 不支持 | • 项目已用 PG 15，无风险<br>• 测试迁移前先验证约束语法 |
| **登录历史触发器性能** | 高频登录用户延迟 +10ms | • 触发器仅删除超出窗口记录（< 100 条）<br>• 实测影响可忽略 |
| **本地头像存储容量** | 10 万用户 = 50GB | • 预留 20GB 磁盘空间<br>• P1 再迁移 OSS |

### 7.3 Resend 国内可达性验证

**已验证**（开发总监责任）：
- ✅ 从国内服务器 `curl https://api.resend.com` 可通
- ✅ 实测发送邮件延迟 200-300ms
- ⚠️ 未验证：大量并发发送时的稳定性

**建议**：
1. Sprint 1 Day 1 立即测试：注册 Resend → 发送测试邮件 → 确认国内收件
2. 若测试失败，立即切换备用方案（阿里云邮件推送 SMTP）

### 7.4 SMS 申请周期

**关键路径**：手机号绑定依赖 SMS 服务

**时间线**：
- Day 0：提交签名审核（营业执照 + 授权书）
- Day 1-2：签名审核通过
- Day 2：提交模板审核（验证码模板）
- Day 2：模板审核通过（2 小时）
- Day 3：Sprint 3 Day 6 开始开发

**建议**：提前 1 周申请 SMS 服务，避免阻塞 Sprint 3。

---

## § 8. 测试策略

### 8.1 单元测试（目标覆盖率 ≥80%）

**关键测试点**：

| 包 | 测试文件 | 关键用例 |
|---|----------|----------|
| `auth` | `service_test.go` | 修改密码：成功/旧密码错误/新密码弱<br>邮箱验证：成功/token 过期/已使用/5 分钟防刷 |
| `user` | `service_test.go` | 个人资料：昵称长度/时区校验<br>手机号：绑定成功/验证码错误/手机号占用/唯一登录方式保护 |
| `pkg/email` | `resend_test.go` | 发送成功/速率限制重试/永久失败 |
| `pkg/sms` | `aliyun_test.go`<br>`tencent_test.go` | 发送成功/手机号格式错误/余额不足 |

**Mock 策略**：
- SMTP/SMS 服务：mock `Provider` 接口
- Redis：使用 `miniredis`（内存 Redis）
- PostgreSQL：使用测试数据库（`YUQING_TEST_PG_URL` gate）

### 8.2 集成测试

**测试文件**：`platform/test/integration/user_center_test.go`

**关键路径**：

```go
func TestUserCenterIntegration(t *testing.T) {
    // 1. 注册用户（未验证）
    user, _, _ := authSvc.Register(ctx, "test@example.com", "Pass123", "Test")
    assert.Nil(t, user.EmailVerifiedAt)
    
    // 2. 试用 1 次分析
    analysis1, _ := analysisSvc.Create(ctx, CreateAnalysisRequest{UserID: user.ID, ...})
    assert.NotNil(t, analysis1)
    
    // 3. 第 2 次拒绝
    _, err := analysisSvc.Create(ctx, CreateAnalysisRequest{UserID: user.ID, ...})
    assert.ErrorContains(t, err, "TRIAL_USED")
    
    // 4. 邮箱验证
    authSvc.SendVerificationEmail(ctx, user.ID)
    token := getTokenFromStore(t, user.ID)
    authSvc.VerifyEmail(ctx, token)
    
    // 5. 验证后可创建
    analysis2, _ := analysisSvc.Create(ctx, CreateAnalysisRequest{UserID: user.ID, ...})
    assert.NotNil(t, analysis2)
    
    // 6. 修改密码后重登录
    authSvc.ChangePassword(ctx, user.ID, "Pass123", "NewPass456")
    _, _, err = authSvc.Login(ctx, user.Email, "Pass123")
    assert.Error(t, err) // 旧密码失效
    _, _, err = authSvc.Login(ctx, user.Email, "NewPass456")
    assert.NoError(t, err) // 新密码可用
    
    // 7. 绑定手机号
    userSvc.SendPhoneVerificationCode(ctx, user.ID, "13800138000")
    code := getCodeFromRedis(t, "13800138000")
    userSvc.BindPhone(ctx, user.ID, "13800138000", code)
    
    // 8. 解绑手机号（密码验证）
    err = userSvc.UnbindPhone(ctx, user.ID, "WrongPass")
    assert.ErrorContains(t, err, "INVALID_PASSWORD")
    err = userSvc.UnbindPhone(ctx, user.ID, "NewPass456")
    assert.NoError(t, err)
}
```

### 8.3 E2E 测试（Playwright）

**测试文件**：`web/e2e/user-center.spec.ts`

**场景覆盖**（至少 10 个 spec）：

```typescript
test.describe('用户中心 P0 功能', () => {
  test('修改密码后强制重新登录', async ({ page }) => {
    // 1. 登录 → 2. 修改密码 → 3. 跳转登录页 → 4. 新密码登录
  });
  
  test('邮箱验证流程', async ({ page }) => {
    // 1. 注册 → 2. 发送验证邮件 → 3. 点击链接 → 4. 验证成功
  });
  
  test('未验证用户试用 1 次', async ({ page }) => {
    // 1. 注册 → 2. 创建分析（成功）→ 3. 第 2 次创建（拒绝，提示验证）
  });
  
  test('个人资料编辑', async ({ page }) => {
    // 1. 修改昵称 → 2. 上传头像 → 3. 切换时区 → 4. 保存 → 5. 刷新验证
  });
  
  test('绑定手机号', async ({ page }) => {
    // 1. 输入手机号 → 2. 发送验证码 → 3. 输入验证码 → 4. 确认绑定 → 5. 验证显示
  });
  
  test('解绑手机号需密码', async ({ page }) => {
    // 1. 点击解绑 → 2. 输入错误密码（拒绝）→ 3. 输入正确密码（成功）
  });
  
  test('唯一登录方式不可解绑', async ({ page }) => {
    // 1. 未验证邮箱 + 已绑手机 → 2. 尝试解绑手机 → 3. 提示"请先验证邮箱"
  });
  
  test('套餐余额页展示', async ({ page }) => {
    // 1. 登录 → 2. 进入计费页 → 3. 验证套餐信息/余额/进度条
  });
  
  test('API Key 管理仅 admin 可见', async ({ page, context }) => {
    // 1. tenant_admin 登录 → 2. 左侧导航显示"API 管理"
    // 3. 普通用户登录 → 4. 左侧导航不显示"API 管理"
  });
  
  test('密码强度实时反馈', async ({ page }) => {
    // 1. 修改密码 Modal → 2. 输入弱密码 → 3. 显示"弱"+ 红色
    // 4. 输入强密码 → 5. 显示"强"+ 绿色
  });
});
```

### 8.4 测试数据准备

**Fixture**（`web/e2e/fixtures.ts`）：

```typescript
export const testUsers = {
  unverified: {
    email: 'unverified@test.com',
    password: 'TestPass123',
    name: 'Unverified User',
  },
  verified: {
    email: 'verified@test.com',
    password: 'TestPass123',
    name: 'Verified User',
    emailVerifiedAt: '2026-09-01T00:00:00Z',
  },
  withPhone: {
    email: 'withphone@test.com',
    password: 'TestPass123',
    name: 'User With Phone',
    phone: '13800138000',
  },
};
```

---

## § 9. 部署清单

### 9.1 环境变量新增项

**server.env**（`platform/config/server.env`）：

```bash
# ─── 邮件服务 ────────────────────────────────────
EMAIL_PROVIDER=resend
RESEND_API_KEY=re_***（从 resend.com/api-keys 获取）
EMAIL_FROM=盘古舆情 <noreply@pangu-cloud.com>

# ─── 短信服务（可选服务商）──────────────────────
SMS_PROVIDER=aliyun  # aliyun | tencent

# 阿里云 SMS
SMS_ALIYUN_ACCESS_KEY_ID=LTAI***
SMS_ALIYUN_ACCESS_KEY_SECRET=***
SMS_ALIYUN_SIGN_NAME=盘古舆情
SMS_ALIYUN_TEMPLATE_CODE=SMS_123456789

# 腾讯云 SMS
SMS_TENCENT_SECRET_ID=AKI***
SMS_TENCENT_SECRET_KEY=***
SMS_TENCENT_SDK_APP_ID=1400***
SMS_TENCENT_SIGN_NAME=盘古舆情
SMS_TENCENT_TEMPLATE_ID=123456

# ─── Redis（验证码存储）────────────────────────
REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=
REDIS_DB=0

# ─── 本地存储 ───────────────────────────────────
STORAGE_TYPE=local  # local | oss
STORAGE_LOCAL_PATH=/opt/yuqing/uploads
STORAGE_BASE_URL=https://yuqing.pangu-cloud.com
```

### 9.2 nginx 配置修改

**添加 /uploads/ location**（`/etc/nginx/sites-available/yuqing.conf`）：

```nginx
server {
    listen 443 ssl http2;
    server_name yuqing.pangu-cloud.com;
    
    # ... 已有配置
    
    # 静态文件服务（头像）
    location /uploads/ {
        alias /opt/yuqing/uploads/;
        
        # 缓存策略：头像不常变，缓存 30 天
        expires 30d;
        add_header Cache-Control "public, immutable";
        
        # 安全：禁止目录遍历
        autoindex off;
        
        # GZIP 压缩
        gzip on;
        gzip_types image/jpeg image/png image/gif;
        
        # 防盗链（可选）
        valid_referers none blocked server_names *.pangu-cloud.com;
        if ($invalid_referer) {
            return 403;
        }
    }
    
    # ... 其他 location
}
```

### 9.3 数据库迁移执行

```bash
# 1. 备份数据库
sudo -u postgres pg_dump yuqing_platform > backup_$(date +%Y%m%d).sql

# 2. 执行迁移
cd /opt/yuqing/platform
./bin/yuqing-cli migrate platform

# 3. 验证迁移
psql -U yuqing -d yuqing_platform -c "\d users"
# 应显示新增字段：password_changed_at, email_verified_at, avatar_url, timezone, phone, phone_verified_at, trial_analysis_used

psql -U yuqing -d yuqing_platform -c "\d verification_tokens"
# 应显示表结构 + EXCLUDE 约束

# 4. 验证触发器
psql -U yuqing -d yuqing_platform -c "\df prune_login_sessions"
# 应显示触发器函数
```

### 9.4 服务重启顺序

```bash
# 1. 停止服务
sudo systemctl stop yuqing-server yuqing-worker

# 2. 部署新二进制（本地交叉编译后上传）
cd /opt/yuqing/platform
make build  # 本地执行
scp bin/yuqing-* user@server:/opt/yuqing/bin/

# 3. 执行迁移
./bin/yuqing-cli migrate platform

# 4. 重启服务
sudo systemctl start yuqing-server
sudo systemctl start yuqing-worker

# 5. 验证启动
sudo journalctl -u yuqing-server -f
# 应无报错，看到"HTTP server started on :8080"

# 6. 健康检查
curl http://localhost:8080/health
# {"status":"ok"}
```

### 9.5 前端部署

```bash
# 本地构建
cd web
npm ci
npm run build  # → dist/

# 上传到服务器
scp -r dist/* user@server:/var/www/yuqing/

# nginx reload
sudo nginx -t
sudo systemctl reload nginx
```

### 9.6 验证部署

**Checklist**：

- [ ] 访问 `https://yuqing.pangu-cloud.com/settings/profile` 显示个人资料页
- [ ] 上传头像成功，URL 可访问（`/uploads/avatars/...`）
- [ ] 发送验证邮件成功，收件箱收到邮件，点击链接验证成功
- [ ] 绑定手机号成功，验证码短信收到
- [ ] 修改密码后跳转登录页，新密码可登录
- [ ] 套餐余额页显示正确的额度与套餐信息
- [ ] API Key 管理页仅 tenant_admin 可见

---

## § 10. 可行性评审结论

### 10.1 技术栈验证

| 组件 | 状态 | 备注 |
|------|------|------|
| **Resend Go SDK** | ✅ 可用 | `github.com/resendlabs/resend-go` v2.x，文档完整 |
| **阿里云 SMS SDK** | ✅ 可用 | `github.com/aliyun/alibaba-cloud-sdk-go/services/dysmsapi` |
| **腾讯云 SMS SDK** | ✅ 可用 | `github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms` |
| **PostgreSQL 15 EXCLUDE 约束** | ✅ 支持 | PG 9.0+ 支持，项目用 PG 15 无风险 |
| **Redis Go Client** | ✅ 可用 | `github.com/redis/go-redis/v9`，项目已依赖 |
| **argon2id 密码强度** | ✅ 已实现 | `platform/internal/platform/auth/auth.go` 已有 `HashPassword` / `VerifyPassword` |

### 10.2 外部服务依赖

| 服务 | 申请难度 | 周期 | 成本 |
|------|----------|------|------|
| **Resend.com** | 🟢 低 | 即时 | 免费 3000 封/月 |
| **阿里云 SMS** | 🟡 中 | 1-2 工作日 | 0.045 元/条 |
| **腾讯云 SMS** | 🟡 中 | 1-2 工作日 | 0.04 元/条 |

### 10.3 实施风险评估

| 风险 | 概率 | 影响 | 缓解措施 | 残余风险 |
|------|------|------|----------|----------|
| Resend 国内不可达 | 低 | 高 | SMTP fallback 备用方案 | 低 |
| SMS 审核失败 | 低 | 中 | 提前准备材料 + 双供应商 | 低 |
| PostgreSQL 迁移失败 | 低 | 高 | 迁移前备份 + 验证约束语法 | 极低 |
| 触发器性能影响 | 低 | 低 | 仅影响高频登录用户 +10ms | 可接受 |
| 本地存储容量不足 | 中 | 低 | 预留 20GB + P1 迁移 OSS | 低 |

### 10.4 工作量评估

| 阶段 | 工作量 | 信心度 |
|------|--------|--------|
| **P0 实施**（5 功能） | 8 人天 | 高（90%） |
| **外部服务申请** | 0.5 人天 | 中（70%，审核不可控） |
| **测试与修复** | 2 人天 | 高（85%） |
| **部署与验证** | 0.5 人天 | 高（95%） |
| **总计** | **11 人天** | |

**缓冲**：按 20% 预留 → **13.2 人天**（约 2.5 周，1 人专职）

### 10.5 最终结论

**✅ 可行**

**理由**：
1. ✅ 技术栈成熟，Go SDK/PostgreSQL/Redis 均已验证可用
2. ✅ 外部服务（Resend/SMS）有备用方案，无单点故障
3. ✅ 现有代码库架构清晰，扩展点明确（auth/user 包独立）
4. ✅ 用户决策已明确（Resend/SMS 双供应商/本地存储/方案 B），无需重新讨论
5. ⚠️ **唯一阻塞项**：SMS 服务申请需 1-2 工作日，建议提前申请

**建议实施顺序**：
1. **立即**：申请阿里云 + 腾讯云 SMS 服务（签名 + 模板）
2. **Sprint 1**（Day 1-3）：修改密码 + 邮箱验证（无外部依赖）
3. **Sprint 2**（Day 4-5）：个人资料编辑（无外部依赖）
4. **Sprint 3**（Day 6-8）：手机号绑定（依赖 SMS 审核通过）+ 套餐余额页

**预期交付时间**：2.5 周（含测试与部署）

---

## § 11. 附录

### 11.1 API 端点汇总

| 端点 | 方法 | 权限 | 说明 |
|------|------|------|------|
| `/api/v1/auth/password` | PUT | 需登录 | 修改密码 |
| `/api/v1/auth/send-verification-email` | POST | 需登录 | 发送验证邮件 |
| `/api/v1/auth/verify-email?token=xxx` | GET | 免鉴权 | 验证邮箱 |
| `/api/v1/user/profile` | GET | 需登录 | 获取个人资料 |
| `/api/v1/user/profile` | PUT | 需登录 | 更新个人资料 |
| `/api/v1/user/avatar` | POST | 需登录 | 上传头像 |
| `/api/v1/user/phone/send-code` | POST | 需登录 | 发送手机验证码 |
| `/api/v1/user/phone/bind` | POST | 需登录 | 绑定手机号 |
| `/api/v1/user/phone/unbind` | POST | 需登录 | 解绑手机号 |
| `/api/v1/api-keys` | POST | tenant_admin | 创建 API Key |
| `/api/v1/api-keys` | GET | tenant_admin | 列出 API Keys |
| `/api/v1/api-keys/:id` | DELETE | tenant_admin | 撤销 API Key |

### 11.2 错误码汇总

| 错误码 | HTTP 状态 | 说明 |
|--------|----------|------|
| `WEAK_PASSWORD` | 400 | 密码强度不足（< 8 位 OR 无字母 OR 无数字） |
| `UNAUTHORIZED` | 401 | 旧密码错误 / Token 无效 |
| `ALREADY_VERIFIED` | 400 | 邮箱已验证 |
| `INVALID_TOKEN` | 400 | Token 不存在或已过期 |
| `TOKEN_EXPIRED` | 400 | Token 已过期 |
| `TOKEN_USED` | 400 | Token 已使用 |
| `RATE_LIMITED` | 429 | 5 分钟内已发送验证邮件 |
| `TRIAL_USED` | 403 | 试用次数已用完，需验证邮箱 |
| `INVALID_NAME` | 400 | 昵称长度不符（2-20 字符） |
| `INVALID_TIMEZONE` | 400 | 时区不支持 |
| `INVALID_AVATAR_URL` | 400 | 头像 URL 格式错误 |
| `FILE_TOO_LARGE` | 400 | 文件超过 2MB |
| `INVALID_FILE_TYPE` | 400 | 文件类型不支持（仅图片） |
| `INVALID_PHONE` | 400 | 手机号格式错误 |
| `PHONE_TAKEN` | 409 | 手机号已被其他用户绑定 |
| `INVALID_CODE` | 400 | 验证码错误或已过期 |
| `INVALID_PASSWORD` | 401 | 密码错误（解绑手机时） |
| `CANNOT_UNBIND` | 403 | 不可解绑唯一登录方式 |
| `FORBIDDEN` | 403 | 权限不足（API Key 管理） |

### 11.3 前端路由汇总

| 路由 | 组件 | 说明 |
|------|------|------|
| `/settings` | `SettingsLayout` | 用户中心根布局 |
| `/settings/profile` | `SettingsProfilePage` | 个人资料 |
| `/settings/security` | `SettingsSecurityPage` | 账户安全 |
| `/settings/connections` | `SettingsConnectionsPage` | 绑定与集成 |
| `/settings/notifications` | `SettingsNotificationsPage` | 通知设置（P1） |
| `/settings/api-keys` | `SettingsAPIKeysPage` | API Key 管理 |
| `/settings/billing` | `SettingsBillingPage` | 套餐与余额 |
| `/verify-email?token=xxx` | `VerifyEmailPage` | 邮箱验证页 |

---

**文档结束**
