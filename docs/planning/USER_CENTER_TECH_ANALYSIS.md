# 用户中心功能 - 技术实施分析

> 基于产品方案的技术可行性验证与实施路径设计  
> 状态：初步分析，待产品方案完整后细化

## 一、现状评估

### 1.1 数据库现状

**users 表（0001_init.sql）**：
```sql
CREATE TABLE users (
    id             TEXT PRIMARY KEY,
    email          CITEXT UNIQUE NOT NULL,
    password_hash  TEXT NOT NULL,
    name           TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'active',
    last_login_at  TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**缺失字段（P0 功能需要）**：
- ❌ `phone` CITEXT UNIQUE - 手机号（F19 依赖）
- ❌ `email_verified_at` TIMESTAMPTZ - 邮箱验证时间戳
- ❌ `avatar_url` TEXT - 头像 URL
- ❌ `timezone` TEXT - 时区（默认 Asia/Shanghai）
- ❌ `notification_prefs` JSONB - 通知偏好

**需要新增的表**：
- ❌ `verification_tokens` - 邮箱验证 token（24h 过期）
- ❌ `login_sessions` - 登录历史（IP/设备/地点）
- ❌ `oauth_connections` - 第三方账号绑定（微信/GitHub）
- ❌ `notifications` - 站内通知（P2 功能）
- ❌ `invoice_requests` - 发票申请（P1 功能）

### 1.2 服务层现状

**auth.Service 已有方法**：
```go
type Service interface {
    Register(ctx, req) (*User, *Tenant, *AccessToken, error)
    Login(ctx, email, password) (*User, *AccessToken, error)
    Authenticate(ctx, token) (*User, error)
    Refresh(ctx, refreshToken) (*AccessToken, error)
    Logout(ctx, refreshToken) error
}
```

**缺失方法（P0 功能需要）**：
- ❌ `ChangePassword(ctx, userID, oldPassword, newPassword) error`
- ❌ `SendVerificationEmail(ctx, userID) error`
- ❌ `VerifyEmail(ctx, token) error`
- ❌ `UpdateProfile(ctx, userID, req) error`
- ❌ `BindPhone(ctx, userID, phone, code) error`
- ❌ `UnbindPhone(ctx, userID, password) error`

### 1.3 前端现状

**路由结构**（web/src/）：
```
src/pages/
├── auth/
│   ├── LoginPage.tsx
│   └── RegisterPage.tsx
├── dashboard/
├── analyses/
└── settings/  ❌ 不存在，需全新创建
```

**API 客户端**（web/src/api/）：
- ✅ `auth.ts` - 登录/注册/刷新
- ❌ `user.ts` - 个人资料/修改密码
- ❌ `settings.ts` - 通知偏好/API Key
- ❌ `team.ts` - 成员管理

---

## 二、P0 功能技术设计

### 2.1 修改密码（2 人天）

#### 后端实现

**迁移 SQL**（0007_user_center_p0.sql）：
```sql
-- 无需新增字段，password_hash 已存在
-- 但需在 users 表添加 password_changed_at
ALTER TABLE users ADD COLUMN password_changed_at TIMESTAMPTZ;
UPDATE users SET password_changed_at = created_at WHERE password_changed_at IS NULL;
```

**Service 方法**（auth/service.go）：
```go
func (s *service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
    // 1. 获取用户
    user, err := s.store.GetUser(ctx, userID)
    if err != nil {
        return err
    }
    
    // 2. 验证旧密码
    if err := argon2id.CompareHashAndPassword(user.PasswordHash, oldPassword); err != nil {
        return errors.Wrap(ErrInvalidCredentials, "old password incorrect")
    }
    
    // 3. 密码强度校验
    if len(newPassword) < 8 {
        return errors.New("WEAK_PASSWORD", "password must be at least 8 characters")
    }
    // TODO: 增加强度检查（大小写+数字+特殊符号）
    
    // 4. 生成新哈希
    newHash, err := argon2id.CreateHash(newPassword, argon2id.DefaultParams)
    if err != nil {
        return errors.Wrap(err, "hash generation failed")
    }
    
    // 5. 更新密码 + 时间戳
    if err := s.store.UpdatePassword(ctx, userID, newHash); err != nil {
        return err
    }
    
    // 6. 撤销所有 refresh_token（强制重新登录）
    if err := s.store.RevokeAllRefreshTokens(ctx, userID); err != nil {
        s.logger.Warn("failed to revoke tokens", "user_id", userID, "error", err)
        // 不阻塞密码修改流程
    }
    
    return nil
}
```

**API 端点**（api/v1/auth_handlers.go）：
```go
// PUT /api/v1/auth/password
func (h *Handlers) ChangePassword(c *gin.Context) {
    var req struct {
        OldPassword string `json:"old_password" binding:"required"`
        NewPassword string `json:"new_password" binding:"required,min=8"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, errors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id") // 从 JWT middleware 获取
    
    if err := h.services.Auth.ChangePassword(c.Request.Context(), userID, req.OldPassword, req.NewPassword); err != nil {
        respondError(c, err)
        return
    }
    
    c.JSON(200, gin.H{"message": "password changed, please log in again"})
}
```

#### 前端实现

**Modal 组件**（web/src/components/settings/ChangePasswordModal.tsx）：
```tsx
export function ChangePasswordModal({ open, onClose }: Props) {
  const [form] = Form.useForm();
  const [loading, setLoading] = useState(false);
  
  const handleSubmit = async (values: ChangePasswordForm) => {
    setLoading(true);
    try {
      await userApi.changePassword(values);
      message.success('密码修改成功，请重新登录');
      // 清空本地 token
      authStore.logout();
      // 跳转登录页
      navigate('/login');
    } catch (err) {
      if (err.code === 'INVALID_CREDENTIALS') {
        form.setFields([{ name: 'oldPassword', errors: ['旧密码错误'] }]);
      } else {
        message.error(err.message);
      }
    } finally {
      setLoading(false);
    }
  };
  
  return (
    <Modal title="修改密码" open={open} onCancel={onClose} footer={null}>
      <Form form={form} onFinish={handleSubmit} layout="vertical">
        <Form.Item
          label="旧密码"
          name="oldPassword"
          rules={[{ required: true, message: '请输入旧密码' }]}
        >
          <Input.Password />
        </Form.Item>
        
        <Form.Item
          label="新密码"
          name="newPassword"
          rules={[
            { required: true, message: '请输入新密码' },
            { min: 8, message: '密码至少 8 位' },
            // TODO: 密码强度实时反馈
          ]}
        >
          <Input.Password />
        </Form.Item>
        
        <Form.Item
          label="确认新密码"
          name="confirmPassword"
          dependencies={['newPassword']}
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
          <Input.Password />
        </Form.Item>
        
        <Form.Item>
          <Space>
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

**密码强度指示器**（可选增强）：
```tsx
const getPasswordStrength = (password: string): { score: number; text: string } => {
  let score = 0;
  if (password.length >= 8) score++;
  if (password.length >= 12) score++;
  if (/[a-z]/.test(password) && /[A-Z]/.test(password)) score++;
  if (/\d/.test(password)) score++;
  if (/[^a-zA-Z0-9]/.test(password)) score++;
  
  const labels = ['弱', '中等', '强', '很强', '极强'];
  return { score, text: labels[Math.min(score, 4)] };
};
```

---

### 2.2 邮箱验证（3 人天）

#### 后端实现

**迁移 SQL**：
```sql
-- users 表添加验证时间戳
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;

-- 验证 token 表
CREATE TABLE verification_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT UNIQUE NOT NULL,
    type       TEXT NOT NULL, -- email_verification, password_reset
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_verification_tokens_token ON verification_tokens(token);
CREATE INDEX idx_verification_tokens_user_type ON verification_tokens(user_id, type);
```

**Service 方法**（auth/service.go）：
```go
func (s *service) SendVerificationEmail(ctx context.Context, userID string) error {
    user, err := s.store.GetUser(ctx, userID)
    if err != nil {
        return err
    }
    
    // 已验证则跳过
    if user.EmailVerifiedAt != nil {
        return errors.New("ALREADY_VERIFIED", "email already verified")
    }
    
    // 生成 token（ULID）
    token := id.NewULID()
    expiresAt := time.Now().Add(24 * time.Hour)
    
    if err := s.store.CreateVerificationToken(ctx, userID, token, "email_verification", expiresAt); err != nil {
        return err
    }
    
    // 发送邮件（SMTP）
    verifyURL := fmt.Sprintf("https://%s/verify-email?token=%s", s.domain, token)
    if err := s.emailSender.SendVerificationEmail(user.Email, user.Name, verifyURL); err != nil {
        s.logger.Error("failed to send verification email", "user_id", userID, "error", err)
        return errors.Wrap(err, "email send failed")
    }
    
    return nil
}

func (s *service) VerifyEmail(ctx context.Context, token string) error {
    // 1. 查找 token
    vt, err := s.store.GetVerificationToken(ctx, token)
    if err != nil {
        return errors.New("INVALID_TOKEN", "token not found or expired")
    }
    
    // 2. 检查过期
    if time.Now().After(vt.ExpiresAt) {
        return errors.New("TOKEN_EXPIRED", "verification link expired")
    }
    
    // 3. 检查已使用
    if vt.UsedAt != nil {
        return errors.New("TOKEN_USED", "token already used")
    }
    
    // 4. 标记邮箱已验证
    if err := s.store.MarkEmailVerified(ctx, vt.UserID); err != nil {
        return err
    }
    
    // 5. 标记 token 已使用
    if err := s.store.MarkTokenUsed(ctx, token); err != nil {
        s.logger.Warn("failed to mark token used", "token", token, "error", err)
    }
    
    return nil
}
```

**API 端点**：
```go
// POST /api/v1/auth/send-verification-email
func (h *Handlers) SendVerificationEmail(c *gin.Context) {
    userID := c.GetString("user_id")
    
    if err := h.services.Auth.SendVerificationEmail(c.Request.Context(), userID); err != nil {
        if err.Code == "ALREADY_VERIFIED" {
            c.JSON(200, gin.H{"message": "email already verified"})
            return
        }
        respondError(c, err)
        return
    }
    
    c.JSON(200, gin.H{"message": "verification email sent"})
}

// GET /api/v1/auth/verify-email?token=xxx（无需登录）
func (h *Handlers) VerifyEmail(c *gin.Context) {
    token := c.Query("token")
    if token == "" {
        respondError(c, errors.New("INVALID_REQUEST", "token required"))
        return
    }
    
    if err := h.services.Auth.VerifyEmail(c.Request.Context(), token); err != nil {
        respondError(c, err)
        return
    }
    
    c.JSON(200, gin.H{"message": "email verified successfully"})
}
```

#### 前端实现

**验证入口**（SettingsProfilePage.tsx）：
```tsx
{!user.emailVerifiedAt && (
  <Alert
    type="warning"
    message="邮箱未验证"
    description="请验证邮箱以使用完整功能"
    action={
      <Button size="small" onClick={handleSendVerification} loading={sending}>
        重新发送验证邮件
      </Button>
    }
    closable
  />
)}
```

**验证页面**（VerifyEmailPage.tsx）：
```tsx
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
        setError(err.message);
      });
  }, [token]);
  
  if (status === 'verifying') {
    return <Spin tip="正在验证邮箱..." />;
  }
  
  if (status === 'success') {
    return (
      <Result
        status="success"
        title="邮箱验证成功"
        subTitle="您现在可以使用完整功能了"
        extra={<Button type="primary" href="/dashboard">前往控制台</Button>}
      />
    );
  }
  
  return (
    <Result
      status="error"
      title="验证失败"
      subTitle={error}
      extra={<Button href="/settings/profile">返回个人资料</Button>}
    />
  );
}
```

**功能受限检查**（middleware）：
```tsx
// 创建分析前检查
if (!currentUser.emailVerifiedAt) {
  Modal.warning({
    title: '需要验证邮箱',
    content: '请先验证邮箱才能创建分析任务',
    okText: '去验证',
    onOk: () => navigate('/settings/profile'),
  });
  return;
}
```

**SMTP 配置**（环境变量）：
```bash
# config/server.env
SMTP_HOST=smtp.exmail.qq.com
SMTP_PORT=465
SMTP_USERNAME=noreply@pangu-cloud.com
SMTP_PASSWORD=***
SMTP_FROM=盘古舆情 <noreply@pangu-cloud.com>
```

---

### 2.3 个人资料编辑（3 人天）

#### 后端实现

**迁移 SQL**：
```sql
ALTER TABLE users ADD COLUMN avatar_url TEXT;
ALTER TABLE users ADD COLUMN timezone TEXT DEFAULT 'Asia/Shanghai';
-- name 字段已存在，无需添加
```

**Service 方法**（新建 user/service.go）：
```go
package user

type UpdateProfileRequest struct {
    Name     string `json:"name"`
    AvatarURL string `json:"avatar_url"`
    Timezone string `json:"timezone"`
}

func (s *service) UpdateProfile(ctx context.Context, userID string, req UpdateProfileRequest) error {
    // 1. 昵称校验
    if len(req.Name) < 2 || len(req.Name) > 20 {
        return errors.New("INVALID_NAME", "name must be 2-20 characters")
    }
    
    // 2. 时区校验
    validTimezones := []string{"Asia/Shanghai", "Asia/Hong_Kong", "Asia/Tokyo", "America/New_York", "Europe/London"}
    if req.Timezone != "" && !contains(validTimezones, req.Timezone) {
        return errors.New("INVALID_TIMEZONE", "unsupported timezone")
    }
    
    // 3. 头像 URL 校验（可选）
    if req.AvatarURL != "" {
        if !strings.HasPrefix(req.AvatarURL, "http") {
            return errors.New("INVALID_AVATAR_URL", "avatar URL must be HTTP(S)")
        }
    }
    
    // 4. 更新
    return s.store.UpdateProfile(ctx, userID, req)
}
```

**API 端点**（api/v1/user_handlers.go）：
```go
// PUT /api/v1/user/profile
func (h *Handlers) UpdateProfile(c *gin.Context) {
    var req user.UpdateProfileRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, errors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id")
    
    if err := h.services.User.UpdateProfile(c.Request.Context(), userID, req); err != nil {
        respondError(c, err)
        return
    }
    
    // 返回更新后的用户信息
    user, _ := h.services.User.GetProfile(c.Request.Context(), userID)
    c.JSON(200, user)
}
```

#### 前端实现

**页面组件**（SettingsProfilePage.tsx）：
```tsx
export function SettingsProfilePage() {
  const [form] = Form.useForm();
  const { data: user, isLoading } = useQuery({
    queryKey: ['currentUser'],
    queryFn: userApi.getCurrentUser,
  });
  
  const updateMutation = useMutation({
    mutationFn: userApi.updateProfile,
    onSuccess: () => {
      message.success('保存成功');
      queryClient.invalidateQueries({ queryKey: ['currentUser'] });
    },
  });
  
  const handleAvatarUpload = async (file: File) => {
    // TODO: 上传到 OSS，返回 URL
    // MVP 阶段：先用 Gravatar
    const gravatarURL = `https://www.gravatar.com/avatar/${md5(user.email)}?d=identicon&s=200`;
    form.setFieldValue('avatarUrl', gravatarURL);
  };
  
  return (
    <div className="settings-profile">
      <Card title="个人资料">
        <Form
          form={form}
          initialValues={{
            name: user?.name,
            avatarUrl: user?.avatarUrl,
            timezone: user?.timezone || 'Asia/Shanghai',
          }}
          onFinish={(values) => updateMutation.mutate(values)}
          layout="vertical"
        >
          <Form.Item label="头像">
            <Upload
              beforeUpload={handleAvatarUpload}
              showUploadList={false}
              accept="image/jpeg,image/png"
            >
              <Avatar size={120} src={form.getFieldValue('avatarUrl')} />
              <Button icon={<UploadOutlined />}>点击上传</Button>
            </Upload>
            <Typography.Text type="secondary">
              支持 JPG、PNG 格式，文件小于 2MB
            </Typography.Text>
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
            <Input value={user?.email} disabled />
            {user?.emailVerifiedAt ? (
              <Tag color="success">已验证</Tag>
            ) : (
              <Button type="link" size="small">重新发送验证邮件</Button>
            )}
          </Form.Item>
          
          <Form.Item label="手机号">
            {user?.phone ? (
              <>
                <Input value={formatPhone(user.phone)} disabled />
                <Button type="link" onClick={() => navigate('/settings/connections')}>
                  管理绑定
                </Button>
              </>
            ) : (
              <Button onClick={() => navigate('/settings/connections')}>
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

---

### 2.4 手机号绑定（6 人天，F19 一部分）

**说明**：手机号绑定是 F19 的基础，但不包含"手机号注册"和"手机号登录"。完整 F19 需额外实现：
- 手机号注册流程（替代邮箱注册）
- 手机号登录流程（SMS 验证码登录）
- 找回密码（手机号 + 验证码）

#### 后端实现

**迁移 SQL**：
```sql
ALTER TABLE users ADD COLUMN phone CITEXT UNIQUE;
ALTER TABLE users ADD COLUMN phone_verified_at TIMESTAMPTZ;
```

**SMS 服务接口**（pkg/sms/interface.go）：
```go
package sms

type Provider interface {
    SendVerificationCode(phone, code string) error
}

// 阿里云实现
type AliyunProvider struct {
    accessKeyID     string
    accessKeySecret string
    signName        string
    templateCode    string
}

func (p *AliyunProvider) SendVerificationCode(phone, code string) error {
    // TODO: 调用阿里云 SMS API
    // https://help.aliyun.com/zh/sms/developer-reference/api-dysmsapi-2017-05-25-sendsms
    return nil
}
```

**Service 方法**（user/service.go）：
```go
func (s *service) SendPhoneVerificationCode(ctx context.Context, userID, phone string) error {
    // 1. 校验手机号格式
    if !isValidChinaPhone(phone) {
        return errors.New("INVALID_PHONE", "invalid phone number")
    }
    
    // 2. 检查是否已被其他用户绑定
    existingUser, _ := s.store.GetUserByPhone(ctx, phone)
    if existingUser != nil && existingUser.ID != userID {
        return errors.New("PHONE_TAKEN", "phone number already bound to another account")
    }
    
    // 3. 生成 6 位验证码
    code := generateRandomCode(6)
    
    // 4. 存储验证码（Redis，5 分钟过期）
    key := fmt.Sprintf("sms_code:%s", phone)
    if err := s.cache.Set(ctx, key, code, 5*time.Minute); err != nil {
        return err
    }
    
    // 5. 发送短信
    if err := s.smsProvider.SendVerificationCode(phone, code); err != nil {
        return errors.Wrap(err, "SMS send failed")
    }
    
    return nil
}

func (s *service) BindPhone(ctx context.Context, userID, phone, code string) error {
    // 1. 验证验证码
    key := fmt.Sprintf("sms_code:%s", phone)
    storedCode, err := s.cache.Get(ctx, key)
    if err != nil || storedCode != code {
        return errors.New("INVALID_CODE", "verification code incorrect or expired")
    }
    
    // 2. 绑定手机号
    if err := s.store.BindPhone(ctx, userID, phone); err != nil {
        return err
    }
    
    // 3. 删除验证码
    s.cache.Delete(ctx, key)
    
    return nil
}

func (s *service) UnbindPhone(ctx context.Context, userID, password string) error {
    // 1. 获取用户
    user, err := s.store.GetUser(ctx, userID)
    if err != nil {
        return err
    }
    
    // 2. 验证密码（防会话劫持）
    if err := argon2id.CompareHashAndPassword(user.PasswordHash, password); err != nil {
        return errors.New("INVALID_PASSWORD", "password incorrect")
    }
    
    // 3. 检查是否为唯一登录方式
    if user.EmailVerifiedAt == nil && user.Phone != "" && !hasOAuthConnections(ctx, userID) {
        return errors.New("CANNOT_UNBIND", "phone is your only login method, please verify email first")
    }
    
    // 4. 解绑
    return s.store.UnbindPhone(ctx, userID)
}
```

**API 端点**：
```go
// POST /api/v1/user/phone/send-code
func (h *Handlers) SendPhoneCode(c *gin.Context) {
    var req struct {
        Phone string `json:"phone" binding:"required"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, errors.New("INVALID_REQUEST", err.Error()))
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
        respondError(c, errors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id")
    
    if err := h.services.User.BindPhone(c.Request.Context(), userID, req.Phone, req.Code); err != nil {
        respondError(c, err)
        return
    }
    
    user, _ := h.services.User.GetProfile(c.Request.Context(), userID)
    c.JSON(200, user)
}

// POST /api/v1/user/phone/unbind
func (h *Handlers) UnbindPhone(c *gin.Context) {
    var req struct {
        Password string `json:"password" binding:"required"`
    }
    if err := c.ShouldBindJSON(&req); err != nil {
        respondError(c, errors.New("INVALID_REQUEST", err.Error()))
        return
    }
    
    userID := c.GetString("user_id")
    
    if err := h.services.User.UnbindPhone(c.Request.Context(), userID, req.Password); err != nil {
        respondError(c, err)
        return
    }
    
    c.JSON(200, gin.H{"message": "phone unbound"})
}
```

#### 前端实现

**绑定 Modal**（BindPhoneModal.tsx）：
```tsx
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
  });
  
  const bindMutation = useMutation({
    mutationFn: ({ phone, code }: { phone: string; code: string }) =>
      userApi.bindPhone(phone, code),
    onSuccess: () => {
      message.success('绑定成功');
      onSuccess();
      onClose();
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
    form.validateFields().then(({ code }) => {
      bindMutation.mutate({ phone, code });
    });
  };
  
  return (
    <Modal
      title={`绑定手机号 - 步骤 ${step}/2`}
      open={open}
      onCancel={onClose}
      footer={null}
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
                addonBefore={<Select defaultValue="+86"><Select.Option value="+86">+86</Select.Option></Select>}
                placeholder="请输入手机号"
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
              message={`验证码已发送至 ${formatPhone(phone)}`}
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
            
            <Typography.Text type="secondary">
              {countdown > 0 ? (
                `${countdown}s 后可重新发送`
              ) : (
                <Button type="link" size="small" onClick={handleSendCode}>
                  重新发送验证码
                </Button>
              )}
            </Typography.Text>
            
            <Form.Item>
              <Space>
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
      
      {step === 2 && (
        <Alert
          message="绑定后可用于"
          description={
            <ul>
              <li>手机号登录</li>
              <li>找回密码</li>
              <li>支付验证</li>
            </ul>
          }
          type="info"
          showIcon
        />
      )}
    </Modal>
  );
}
```

**解绑确认**（UnbindPhoneModal.tsx）：
```tsx
export function UnbindPhoneModal({ phone, open, onClose, onSuccess }: Props) {
  const [form] = Form.useForm();
  
  const unbindMutation = useMutation({
    mutationFn: (password: string) => userApi.unbindPhone(password),
    onSuccess: () => {
      message.success('已解绑');
      onSuccess();
      onClose();
    },
    onError: (err) => {
      if (err.code === 'INVALID_PASSWORD') {
        form.setFields([{ name: 'password', errors: ['密码错误'] }]);
      } else if (err.code === 'CANNOT_UNBIND') {
        message.error('手机号是您唯一的登录方式，请先验证邮箱或绑定其他登录方式');
      } else {
        message.error(err.message);
      }
    },
  });
  
  return (
    <Modal
      title="确认解绑手机号"
      open={open}
      onCancel={onClose}
      footer={null}
    >
      <Alert
        type="warning"
        message="解绑后将无法使用手机号登录和找回密码"
        style={{ marginBottom: 16 }}
      />
      
      <Form
        form={form}
        onFinish={(values) => unbindMutation.mutate(values.password)}
        layout="vertical"
      >
        <Typography.Text>
          即将解绑手机号：<strong>{formatPhone(phone)}</strong>
        </Typography.Text>
        
        <Form.Item
          label="为了账户安全,请输入登录密码确认"
          name="password"
          rules={[{ required: true, message: '请输入密码' }]}
          style={{ marginTop: 16 }}
        >
          <Input.Password placeholder="请输入登录密码" />
        </Form.Item>
        
        <Form.Item>
          <Space>
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

### 2.5 套餐与余额页（2 人天）

**说明**：这个功能后端已完全实现（billing.Service），只需前端页面。

#### 前端实现

**页面组件**（SettingsBillingPage.tsx）：
```tsx
export function SettingsBillingPage() {
  const { data: credits } = useQuery({
    queryKey: ['credits'],
    queryFn: billingApi.getCredits,
  });
  
  const { data: plans } = useQuery({
    queryKey: ['plans'],
    queryFn: billingApi.getPlans,
  });
  
  const currentPlan = plans?.plans.find((p) => p.code === credits?.planCode);
  
  return (
    <div className="settings-billing">
      <Card title="当前套餐" style={{ marginBottom: 24 }}>
        <Descriptions column={2}>
          <Descriptions.Item label="套餐名称">
            <Tag color="blue">{currentPlan?.name}</Tag>
          </Descriptions.Item>
          <Descriptions.Item label="月费">
            ¥{(currentPlan?.priceMonthly CNY / 100).toFixed(2)}
          </Descriptions.Item>
          <Descriptions.Item label="分析模式">
            {currentPlan?.analysisMode === 'quick' ? '速览版' : '完整版'}
          </Descriptions.Item>
          <Descriptions.Item label="周期额度">
            {currentPlan?.creditsPerCycle} 次/月
          </Descriptions.Item>
        </Descriptions>
        
        <Button type="primary" style={{ marginTop: 16 }}>
          升级套餐
        </Button>
      </Card>
      
      <Card title="额度余额">
        <Statistic
          title="剩余次数"
          value={credits?.balance || 0}
          suffix="次"
          valueStyle={{ color: credits?.balance > 0 ? '#3f8600' : '#cf1322' }}
        />
        
        {credits?.balance === 0 && (
          <Alert
            type="error"
            message="额度已用完"
            description="请升级套餐或购买加购包以继续使用"
            action={
              <Space>
                <Button size="small" type="primary">升级套餐</Button>
                <Button size="small">购买加购包</Button>
              </Space>
            }
            style={{ marginTop: 16 }}
          />
        )}
        
        <Typography.Text type="secondary" style={{ display: 'block', marginTop: 16 }}>
          下次额度刷新时间：{/* TODO: 显示周期结束时间 */}
        </Typography.Text>
      </Card>
    </div>
  );
}
```

---

## 三、数据库迁移汇总

**0007_user_center_p0.sql**：
```sql
-- +goose Up
-- 用户中心 P0 功能：修改密码 + 邮箱验证 + 个人资料 + 手机号绑定

-- 1. users 表新增字段
ALTER TABLE users ADD COLUMN password_changed_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN avatar_url TEXT;
ALTER TABLE users ADD COLUMN timezone TEXT DEFAULT 'Asia/Shanghai';
ALTER TABLE users ADD COLUMN phone CITEXT UNIQUE;
ALTER TABLE users ADD COLUMN phone_verified_at TIMESTAMPTZ;

-- 历史数据修正
UPDATE users SET password_changed_at = created_at WHERE password_changed_at IS NULL;

-- 2. 验证 token 表
CREATE TABLE verification_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token      TEXT UNIQUE NOT NULL,
    type       TEXT NOT NULL, -- email_verification, password_reset, phone_verification
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_verification_tokens_token ON verification_tokens(token);
CREATE INDEX idx_verification_tokens_user_type ON verification_tokens(user_id, type);

-- 3. 登录历史表（P1 功能，预留）
CREATE TABLE login_sessions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip_address TEXT NOT NULL,
    user_agent TEXT NOT NULL,
    device     TEXT, -- Windows 11, macOS, iPhone, etc
    location   TEXT, -- 北京市, 香港, etc
    logged_in_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_login_sessions_user_id ON login_sessions(user_id);
CREATE INDEX idx_login_sessions_logged_in_at ON login_sessions(logged_in_at DESC);

-- +goose Down
DROP TABLE IF EXISTS login_sessions;
DROP TABLE IF EXISTS verification_tokens;

ALTER TABLE users DROP COLUMN IF EXISTS phone_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS phone;
ALTER TABLE users DROP COLUMN IF EXISTS timezone;
ALTER TABLE users DROP COLUMN IF EXISTS avatar_url;
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS password_changed_at;
```

---

## 四、外部服务依赖

### 4.1 SMTP（邮箱验证 + 通知）

**推荐方案**：阿里云邮件推送（DirectMail）
- 国内到达率高（>95%）
- 价格：0.5 元/千封（首月 1 万封免费）
- 支持 SMTP 和 API 两种接入方式

**配置示例**（config/server.env）：
```bash
SMTP_HOST=smtpdm.aliyun.com
SMTP_PORT=465
SMTP_USERNAME=noreply@mail.pangu-cloud.com
SMTP_PASSWORD=***（SMTP 密码）
SMTP_FROM=盘古舆情 <noreply@mail.pangu-cloud.com>
```

**申请流程**：
1. 阿里云 → 邮件推送 → 开通服务
2. 配置发信域名（mail.pangu-cloud.com）
3. 添加发信地址（noreply@mail.pangu-cloud.com）
4. 设置 SPF/DKIM 记录（提升到达率）

### 4.2 SMS（手机号验证）

**推荐方案**：阿里云短信服务
- 价格：0.045 元/条（验证码短信）
- 到达率：>99%
- 支持三网合一

**配置示例**（config/server.env）：
```bash
SMS_PROVIDER=aliyun
SMS_ACCESS_KEY_ID=LTAI***
SMS_ACCESS_KEY_SECRET=***
SMS_SIGN_NAME=盘古舆情
SMS_TEMPLATE_CODE=SMS_***（验证码模板）
```

**申请流程**：
1. 阿里云 → 短信服务 → 开通服务
2. 添加签名"盘古舆情"（需营业执照）
3. 申请验证码模板："您的验证码为${code},有效期5分钟"
4. 获取 AccessKey

### 4.3 OSS（头像上传，P1 优化）

**推荐方案**：
- **MVP 阶段**：本地存储（/opt/yuqing/uploads）+ Gravatar 兜底
- **P1 阶段**：阿里云 OSS（按量付费，0.12 元/GB/月）

**本地存储方案**（MVP）：
```go
// 上传接口
// POST /api/v1/upload/avatar
func (h *Handlers) UploadAvatar(c *gin.Context) {
    file, err := c.FormFile("file")
    if err != nil {
        respondError(c, errors.New("INVALID_FILE", "file required"))
        return
    }
    
    // 校验文件类型
    if !strings.HasPrefix(file.Header.Get("Content-Type"), "image/") {
        respondError(c, errors.New("INVALID_FILE_TYPE", "only images allowed"))
        return
    }
    
    // 校验文件大小（<2MB）
    if file.Size > 2*1024*1024 {
        respondError(c, errors.New("FILE_TOO_LARGE", "file must be less than 2MB"))
        return
    }
    
    // 生成唯一文件名
    ext := filepath.Ext(file.Filename)
    filename := id.NewULID() + ext
    filepath := filepath.Join("/opt/yuqing/uploads/avatars", filename)
    
    // 保存文件
    if err := c.SaveUploadedFile(file, filepath); err != nil {
        return errors.Wrap(err, "save failed")
    }
    
    // 返回 URL
    url := fmt.Sprintf("https://%s/uploads/avatars/%s", c.Request.Host, filename)
    c.JSON(200, gin.H{"url": url})
}
```

**nginx 静态文件配置**：
```nginx
location /uploads/ {
    alias /opt/yuqing/uploads/;
    expires 30d;
    add_header Cache-Control "public, immutable";
}
```

---

## 五、P0 实施路线（8 人天）

### Sprint 1（3 天）：基础设施 + 修改密码 + 邮箱验证

**Day 1**：
- [ ] 数据库迁移 0007（users 字段 + verification_tokens 表）
- [ ] SMTP 服务接入（阿里云邮件推送申请 + 配置）
- [ ] `pkg/email` 邮件发送模块

**Day 2**：
- [ ] `auth.Service.ChangePassword` 实现 + 测试
- [ ] `auth.Service.SendVerificationEmail` / `VerifyEmail` 实现 + 测试
- [ ] API 端点：PUT /auth/password, POST /auth/send-verification-email, GET /auth/verify-email

**Day 3**：
- [ ] 前端：ChangePasswordModal 组件
- [ ] 前端：VerifyEmailPage + 验证入口（Alert）
- [ ] 前端：功能受限检查（创建分析前）
- [ ] E2E 测试：注册 → 未验证创建分析 → 验证 → 创建成功

### Sprint 2（2 天）：个人资料编辑

**Day 4**：
- [ ] 新建 `user` 包（internal/platform/user/）
- [ ] `user.Service.UpdateProfile` 实现 + 测试
- [ ] API 端点：PUT /user/profile, GET /user/profile

**Day 5**：
- [ ] 前端：SettingsProfilePage 完整实现
- [ ] 头像上传（本地存储 + Gravatar 兜底）
- [ ] nginx 静态文件配置（/uploads/）

### Sprint 3（3 天）：手机号绑定 + 套餐余额页

**Day 6**：
- [ ] SMS 服务接入（阿里云短信申请 + 配置）
- [ ] `pkg/sms` SMS 发送模块
- [ ] `user.Service` 手机号相关方法（SendPhoneCode / BindPhone / UnbindPhone）

**Day 7**：
- [ ] API 端点：POST /user/phone/send-code, POST /user/phone/bind, POST /user/phone/unbind
- [ ] 前端：BindPhoneModal + UnbindPhoneModal

**Day 8**：
- [ ] 前端：SettingsBillingPage（套餐信息 + 余额显示）
- [ ] 前端：SettingsLayout（左侧导航 + 路由）
- [ ] E2E 测试：修改密码 → 邮箱验证 → 编辑资料 → 绑定手机 → 查看余额

---

## 六、待讨论问题（优先级排序）

### 高优（影响 P0 实施）

1. **邮箱验证"功能受限"具体范围**？
   - 方案 A：完全禁止创建分析（硬限制）
   - 方案 B：允许试用 1 次（体验优先，推荐）
   - 建议：方案 B，降低注册流失率

2. **SMTP 服务选择**？
   - 阿里云邮件推送（推荐，国内到达率高）
   - SendGrid（国际化考虑）
   - 自建 SMTP（不推荐，维护成本高）

3. **SMS 服务选择**？
   - 阿里云短信（推荐，到达率 99%+）
   - 腾讯云短信
   - 建议：阿里云短信，与 SMTP 统一在阿里云

4. **手机号绑定与 F19 边界划分**？
   - 用户中心：手机号绑定/解绑（6 人天）
   - F19 完整：+ 手机号注册 + 手机号登录 + 找回密码（额外 4-5 人天）
   - 建议：P0 只做绑定/解绑，F19 迭代补全注册登录

### 中优（影响体验）

5. **头像上传方案**？
   - MVP：本地存储 + Gravatar 兜底（推荐）
   - P1：阿里云 OSS（按量付费，0.12 元/GB/月）

6. **登录历史存储策略**？
   - 全量存储（无限增长，成本高）
   - 滚动窗口（保留最近 100 条，推荐）
   - 定期归档（冷热分离）

7. **密码强度要求**？
   - 当前：≥8 位
   - 增强：≥8 位 + 大小写 + 数字 + 特殊符号
   - 建议：先≥8位（降低门槛），P1 增强

### 低优（P1/P2 规划）

8. **通知偏好默认值**？
   - 任务完成：✅ 立即通知
   - 负面舆情告警：✅ 立即通知
   - 账单提醒：✅ 立即通知
   - 产品更新：❌ 默认关闭（减少打扰）

9. **第三方登录优先级**？
   - 微信（P1，降低门槛）
   - GitHub（P2，技术社区推广）
   - 企业微信/钉钉（P2，ToB 需求明确后）

10. **API Key 管理权限范围**？
    - 只读（read:analyses, read:reports）
    - 读写（write:analyses, write:reports）
    - 管理员（admin:settings, admin:team）

---

**下一步**：等待产品经理补充剩余内容（API 管理、计费订阅、团队管理、流程图、MVP 路线图）后，制定完整的技术实施方案并提交用户审阅。
