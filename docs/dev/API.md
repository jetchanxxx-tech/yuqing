# API 参考文档

Base URL: `https://your-domain.com/api/v1`

## 认证

所有业务接口需 `Authorization: Bearer <access_token>`。

### POST /auth/register
注册新账户（同时创建租户和数据库）。

```json
// Request
{ "email": "alice@example.com", "password": "********", "name": "Alice" }
// Response 201
{ "access_token": "eyJ...", "refresh_token": "r_...", "user": { "user_id": "01...", "tenant_id": "01...", "email": "alice@example.com", "roles": ["tenant_admin"], "plan_code": "free" } }
```

### POST /auth/login
```json
// Request
{ "email": "alice@example.com", "password": "********" }
// Response 200
{ "access_token": "eyJ...", "refresh_token": "r_...", "user": { ... } }
```

### POST /auth/refresh
```json
// Request
{ "refresh_token": "r_..." }
// Response 200
{ "access_token": "eyJ...", "refresh_token": "r_new..." }
```

### POST /auth/logout
```json
// Request (empty body, token from header)
{}
// Response 200
{ "message": "logged out" }
```

---

## 健康检查

### GET /health
（无需认证）
```json
// Response 200
{ "status": "ok" }
```

---

## 分析任务

### POST /analyses
创建新分析任务。权限：`analyses:create`

```json
// Request
{ "name": "新品舆情监测", "analysis_type": "brand", "keywords": ["新品", "上线"], "sources": ["weibo", "news", "xiaohongshu"] }
// Response 200
{ "id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "name": "新品舆情监测", "state": "queued", "created_at": "2026-08-12T00:00:00Z" }
```

### GET /analyses
分析任务列表。权限：`analyses:list`

```json
// Response 200
{ "analyses": [...], "tenant_id": "t_...", "total": 5 }
```

### GET /analyses/:id
任务详情

```json
// Response 200
{ "id": "01...", "state": "completed", "analysis_type": "brand" }
```

### GET /analyses/:id/result
分析结果

```json
// Response 200
{ "id": "01...", "state": "completed", "documents": [...], "sentiments": { "positive": 65, "negative": 12, "neutral": 23 } }
```

### POST /analyses/:id/cancel
取消任务

```json
// Response 200
{ "id": "01...", "state": "canceled" }
```

### GET /analyses/:id/events
实时进度 (Server-Sent Events)

```
event: progress
data: {"state":"fetching","progress":45}
```

---

## 报告

### GET /reports
报告列表

```json
// Response 200
{ "reports": [{ "id": "R01", "title": "日报", "format": "html", "status": "completed" }], "total": 1 }
```

### GET /reports/:id/download?format=html
下载报告（格式：html | markdown | pdf | docx，按套餐限制）

### GET /reports/templates
可用模板列表

```json
// Response 200
{ "templates": [{ "id": "daily", "name": "日报模板" }, { "id": "weekly", "name": "周报模板" }] }
```

---

## 数据面板

### GET /dashboard/overview
概览指标

```json
// Response 200
{ "total_analyses": 12, "total_docs": 2847, "sentiment_pos": 65, "sentiment_neg": 12, "sentiment_neu": 23, "success_rate": 0.92 }
```

### GET /dashboard/trend?from=2026-08-01&to=2026-08-12
趋势数据

```json
// Response 200
{ "dates": ["2026-08-01", ...], "counts": [120, ...], "scores": [0.65, ...] }
```

### GET /dashboard/sources
来源分布

```json
// Response 200
{ "sources": [{ "name": "微博", "count": 800, "pct": 0.35 }, ...] }
```

### GET /dashboard/topics
热门话题

```json
// Response 200
{ "topics": [{ "name": "舆情事件A", "doc_count": 320, "trend": "rising" }] }
```

---

## 计费（F18 收费体系 · 方案 B）

额度模型：每次分析 Create/Rerun 各扣 1 次；管线失败/取消自动回补；额度不足返回 `402 NO_CREDITS`。新注册赠 1 次试用。

### GET /billing/plans
套餐目录 + 加购 SKU + 当前可用支付渠道（未配置渠道不出现在列表）。

```json
// Response 200
{ "plans": [
    { "code": "free",  "name": "体验版", "price_monthly_cny": 0,      "credits_per_cycle": 0,  "analysis_mode": "quick" },
    { "code": "lite",  "name": "速览版", "price_monthly_cny": 9900,   "credits_per_cycle": 4,  "analysis_mode": "quick" },
    { "code": "pro",   "name": "研判版", "price_monthly_cny": 99900,  "credits_per_cycle": 10, "analysis_mode": "full" },
    { "code": "enterprise", "name": "旗舰版", "price_monthly_cny": 499900, "credits_per_cycle": 50, "analysis_mode": "full", "priority_queue": true } ],
  "addons": [ { "code": "addon_report", "name": "报告加购包", "kind": "addon", "price_cents": 6900, "credits": 1 } ],
  "channels": [ "alipay", "wechat", "unionpay" ] }
```

### GET /billing/credits
额度余额与当前套餐标记。

```json
// Response 200
{ "balance": 7, "plan_code": "pro" }
```

### GET /billing/transactions
额度流水（新→旧，最多 100 条）。reason ∈ `trial`/`grant`/`purchase`/`consume`/`refund`。

```json
// Response 200
{ "transactions": [ { "id": "01...", "delta": -1, "reason": "consume", "analysis_id": "01...",
    "balance_after": 6, "created_at": "..." } ], "total": 1 }
```

### POST /billing/orders
创建支付订单（金额/额度取自服务端目录，前端只传选择）。渠道未配置 → 400。

```json
// Request
{ "sku_code": "lite", "channel": "alipay" }
// Response 201
{ "order": { "id": "01...", "amount_cents": 9900, "credits": 4, "state": "pending",
    "qr_code_url": "https://qr.alipay.com/...", "channel": "alipay", "expires_at": "..." } }
```

### GET /billing/orders/:id
订单详情。**轮询即对账**：pending 订单顺带向渠道主动查单（回调丢失时自愈入账）。轮询建议 3s。

### POST /callbacks/payment/:channel
支付渠道异步回调（**免鉴权**，安全完全依赖渠道验签）。仅由支付宝/微信/银联服务器调用。响应为渠道要求的 ACK 格式；验签/金额不符返回 400。

### GET /billing/orders/:id/paypage?token=<access_token>
银联收银台跳转页（HTML 表单自动提交；浏览器跳转无法带 Authorization 头，改用 query token）。

### 旧占位端点
`GET /billing/subscription`、`POST /billing/subscribe`、`GET /billing/usage`、`GET /billing/invoices`（+ `:id/download`）—— 订阅制时代占位，保留 JSON envelope，P2 订阅化时替换。

---

## 热榜（F21）

### GET /trends
多平台热榜快照（纯内存缓存，服务端 5 分钟刷新；无租户维度，登录即可看）。**不落库**，快照过了就过了。

```json
// Response 200
{ "platforms": [
    { "name": "微博", "status": "ok", "updated_at": "...", "items": [
        { "rank": 1, "title": "…", "url": "https://…", "hot": "486.2万" } ] },
    { "name": "知乎", "status": "stale", "items": [ "..." ], "error": "" },
    { "name": "B站", "status": "error", "items": [], "error": "该平台暂不可用" } ] }
```

三态语义：`ok` 实时 / `stale` 上次成功快照（`updated_at` 为真实新鲜度）/ `error` 无数据。RSSHub 不可达时**仍返回 200**（数据源状态，非服务器故障）；服务未配置（rsshub_base 空）→ `503 TRENDS_UNAVAILABLE`。

---

## 用户中心

账户自助管理：密码修改、邮箱验证、个人资料、手机号绑定。除 `GET /verify-email`（邮件链接落地）外均需 `Authorization: Bearer <access_token>`。

### PUT /auth/password
修改登录密码。新密码要求 ≥8 位且含字母与数字（强度不足返回 `409`）；成功后**当前登录态立即失效**，需用新密码重新登录。

```json
// Request
{ "old_password": "********", "new_password": "********" }
// Response 200
{ "message": "password changed, please log in again" }
```

### POST /auth/send-verification-email
发送邮箱验证邮件（60s 节流，窗口内重复请求 `429`）。邮件服务未配置时返回 500（fail-closed，不会产生「看似已发送」的假状态）。

```json
// Response 200
{ "message": "verification email sent" }
```

### GET /verify-email?token=xxx
邮箱验证链接落地端点（**免鉴权**，用户从邮件点击跳转）。token 一次性，验证成功后 `email_verified=true`；token 无效或过期返回 `404 NOT_FOUND`。

```json
// Response 200
{ "message": "email verified successfully" }
```

### GET /user/profile
当前用户资料。`phone` 脱敏返回（如 `138****8000`，未绑定为空串）；`trial_used` 表示免费试用分析是否已消耗（0/1）。

```json
// Response 200
{ "id": "01...", "email": "alice@example.com", "name": "Alice", "avatar_url": "",
  "timezone": "Asia/Shanghai", "phone": "", "email_verified": false, "phone_verified": false,
  "trial_used": 0, "password_changed_at": "" }
```

### PUT /user/profile
修改昵称与时区。昵称 2-20 字符，超长返回 `409`。

```json
// Request
{ "name": "新昵称", "timezone": "Asia/Shanghai" }
// Response 200（返回更新后的完整 profile，结构同 GET /user/profile）
```

### POST /user/phone/send-code
发送短信验证码（绑定场景，6 位数字，5 分钟有效）。60s 节流（`429`）；同一手机号新码覆盖旧码。短信服务未配置时返回 500（fail-closed）。

```json
// Request
{ "phone": "13800138000" }
// Response 200
{ "message": "verification code sent", "expires_in": 300 }
```

### POST /user/phone/bind
校验验证码并绑定手机号。验证码输错 5 次即作废（防穷举，`404`）；手机号已被其他账号占用返回 `409`。

```json
// Request
{ "phone": "13800138000", "code": "123456" }
// Response 200（返回更新后的完整 profile，结构同 GET /user/profile）
```

### POST /user/phone/unbind
解绑手机号。需验证登录密码（防会话劫持，密码错误 `401`）；**唯一登录方式保护**：邮箱未验证时拒绝解绑（`409`），避免账号失去唯一可登录凭证。

```json
// Request
{ "password": "********" }
// Response 200
{ "message": "phone unbound" }
```

---

## 管理员

权限：`platform_admin` 角色

### GET /admin/tenants
租户列表

### GET+PUT /admin/settings（含支付渠道配置 · F18）
数据源与支付渠道统一读写。支付渠道值为 **JSON 字符串**（各渠道结构不同），保存即时生效（购买页渠道列表与支付回调验签实时读表）。

```json
// PUT /admin/settings 请求体（各渠道可独立保存；未提及的键不变）
{
  "payment_alipay":   "{\"enabled\":true,\"app_id\":\"2021...\",\"private_key\":\"...\",\"alipay_public_key\":\"...\",\"notify_url\":\"https://<域名>/api/v1/callbacks/payment/alipay\"}",
  "payment_wechat":   "{\"enabled\":false,\"appid\":\"wx...\",\"mch_id\":\"...\",\"mch_serial_no\":\"...\",\"private_key\":\"...\",\"api_v3_key\":\"...\",\"notify_url\":\"...\"}",
  "payment_unionpay": "{\"enabled\":false,\"mer_id\":\"...\",\"sign_cert_pfx\":\"<base64 .pfx>\",\"sign_cert_password\":\"...\",\"notify_url\":\"...\",\"front_url\":\"...\"}"
}
```
启用开关即渠道的 `enabled` 字段；支付回调地址格式固定为 `https://<域名>/api/v1/callbacks/payment/<channel>`。

### POST /admin/tenants/:id/suspend
挂起租户

### POST /admin/tenants/:id/resume
恢复租户

---

## 错误响应

所有错误使用统一信封：

```json
{
  "code": "FORBIDDEN",
  "message": "insufficient permissions",
  "details": null,
  "request_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"
}
```

| Code | HTTP | 含义 |
|------|------|------|
| `UNAUTHORIZED` | 401 | Token 缺失或无效 |
| `FORBIDDEN` | 403 | 权限不足或租户被挂起 |
| `TENANT_SUSPENDED` | 403 | 租户已被管理员挂起 |
| `NOT_FOUND` | 404 | 资源不存在 |
| `BUDGET_EXCEEDED` | 429 | Token 配额已用完 |
| `QUOTA_EXCEEDED` | 429 | 并发/次数配额已满 |
| `CONFLICT` | 409 | 资源冲突（邮箱/手机号已被占用、昵称长度不符、密码强度不足、唯一登录方式保护等） |
| `INTERNAL` | 500 | 服务器内部错误 |
