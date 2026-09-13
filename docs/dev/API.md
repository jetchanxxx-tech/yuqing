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

## 计费

### GET /billing/plans
套餐列表

```json
// Response 200
{ "plans": [
  { "code": "free", "name": "体验版", "price": 0, "quota": "1M tokens" },
  { "code": "pro", "name": "专业版", "price": 9900, "quota": "10M tokens" },
  { "code": "business", "name": "企业版", "price": 49900, "quota": "100M tokens" },
  { "code": "enterprise", "name": "旗舰版", "price": 0, "quota": "unlimited" }
]}
```

### GET /billing/subscription
当前订阅状态

### POST /billing/subscribe
升级/订阅套餐

```json
// Request
{ "plan_code": "pro" }
```

### GET /billing/usage
用量概览

### GET /billing/invoices
账单记录

---

## 管理员

权限：`platform_admin` 角色

### GET /admin/tenants
租户列表

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
| `CONFLICT` | 409 | 资源冲突 |
| `INTERNAL` | 500 | 服务器内部错误 |
