# API 参考

盘古舆情 REST API 完整文档。

**Base URL**: `https://yuqing2.pangu-cloud.com/api/v1`

**认证方式**：
- JWT Bearer Token（用户登录）
- API Key（`pangu_` 前缀，程序化调用）

## 📋 目录

- [认证](#认证)
- [分析任务](#分析任务)
- [报告](#报告)
- [仪表盘](#仪表盘)
- [计费](#计费)
- [热榜](#热榜)
- [管理员](#管理员)
- [错误响应](#错误响应)

## 🔐 认证

所有业务接口需 `Authorization: Bearer <token>` 头。

### POST /auth/register

注册新账户（同时创建租户和数据库）。

**请求**：
```json
{
  "email": "alice@example.com",
  "password": "StrongPass123!",
  "name": "Alice"
}
```

**响应 201**：
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "r_01ARZ3NDEKTSV4RRFF...",
  "user": {
    "user_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    "tenant_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    "email": "alice@example.com",
    "name": "Alice",
    "roles": ["tenant_admin"],
    "plan_code": "free"
  }
}
```

**自动赠送**：
- 1 次免费试用额度
- `tenant_admin` 角色

### POST /auth/login

邮箱密码登录。

**请求**：
```json
{
  "email": "alice@example.com",
  "password": "StrongPass123!"
}
```

**响应 200**：
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "r_01ARZ3NDEKTSV4RRFF...",
  "user": { ... }
}
```

### POST /auth/refresh

刷新 Access Token（Access Token 过期时前端自动调用）。

**请求**：
```json
{
  "refresh_token": "r_01ARZ3NDEKTSV4RRFF..."
}
```

**响应 200**：
```json
{
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "r_01ARZ3NEW_TOKEN..."
}
```

### POST /auth/logout

登出（使 Refresh Token 失效）。

**请求**：
```json
{}
```

**响应 200**：
```json
{
  "message": "logged out"
}
```

## 📊 分析任务

### POST /analyses

创建新分析任务。

**权限**：`analyses:create`

**请求**：
```json
{
  "name": "新品舆情监测",
  "analysis_type": "brand",
  "keywords": ["新品", "上线"],
  "sources": ["weibo", "news", "xiaohongshu"]
}
```

**响应 200**：
```json
{
  "id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
  "name": "新品舆情监测",
  "state": "queued",
  "created_at": "2026-09-22T08:00:00Z"
}
```

**自动扣减 1 次额度**，余额不足返回 `402 NO_CREDITS`。

### GET /analyses

分析任务列表。

**权限**：`analyses:list`

**响应 200**：
```json
{
  "analyses": [
    {
      "id": "01ARZ3...",
      "name": "新品舆情监测",
      "state": "completed",
      "progress": 100,
      "created_at": "2026-09-22T08:00:00Z",
      "completed_at": "2026-09-22T08:05:00Z"
    }
  ],
  "tenant_id": "t_...",
  "total": 5
}
```

### GET /analyses/:id

任务详情。

**响应 200**：
```json
{
  "id": "01ARZ3...",
  "name": "新品舆情监测",
  "state": "completed",
  "progress": 100,
  "analysis_type": "brand",
  "keywords": ["新品", "上线"],
  "sources": ["weibo", "news", "xiaohongshu"],
  "created_at": "2026-09-22T08:00:00Z",
  "completed_at": "2026-09-22T08:05:00Z"
}
```

### GET /analyses/:id/result

分析结果（情感/话题/五维/报告/warning）。

**响应 200**：
```json
{
  "id": "01ARZ3...",
  "state": "completed",
  "documents": [
    {
      "id": "d1",
      "title": "某新品上线引热议",
      "content": "...",
      "source_type": "weibo",
      "published_at": "2026-09-22T07:00:00Z"
    }
  ],
  "sentiments": {
    "positive": 65,
    "negative": 12,
    "neutral": 23,
    "details": [
      {
        "document_id": "d1",
        "sentiment": "positive",
        "score": 0.85,
        "confidence": 0.92
      }
    ]
  },
  "topics": [
    {
      "id": "t1",
      "name": "产品质量",
      "keywords": ["质量", "做工"],
      "doc_count": 8,
      "doc_ids": ["d1", "d2"],
      "trend": "rising"
    }
  ],
  "dimensions": [
    {
      "id": "background",
      "title": "事件背景",
      "findings": "核心发现...",
      "data_points": ["数据点1", "数据点2"],
      "quotes": [{"text": "引用原文", "source": "微博"}],
      "deep_read": "深层解读...",
      "trend": "稳定"
    }
  ],
  "summary": "本次分析共采集 17 条文档...",
  "report": "<html>...</html>",
  "warning": []
}
```

### GET /analyses/:id/events

实时进度（SSE）。

**响应**：`text/event-stream`

```
event: progress
data: {"state":"fetching","progress":25}

event: progress
data: {"state":"analyzing","progress":60}

event: final
data: {"state":"completed","progress":100}
```

### POST /analyses/:id/cancel

取消任务。

**响应 200**：
```json
{
  "id": "01ARZ3...",
  "state": "canceled"
}
```

**自动回补 1 次额度**。

### POST /analyses/:id/rerun

重新运行任务。

**响应 200**：
```json
{
  "id": "01ARZ3_NEW...",
  "state": "queued"
}
```

**扣减 1 次额度**，复用原任务的参数。

## 📄 报告

### GET /reports

报告列表。

**响应 200**：
```json
{
  "reports": [
    {
      "id": "R01",
      "title": "新品舆情监测-日报",
      "format": "html",
      "status": "completed",
      "created_at": "2026-09-22T08:05:00Z"
    }
  ],
  "total": 1
}
```

### GET /reports/:id/download?format=html

下载报告（格式：html / markdown / pdf / docx，按套餐限制）。

**套餐 gating**：
- Free/Lite：仅 HTML
- Pro：HTML + Markdown + PDF
- Enterprise：全格式

**响应 200**：返回文件流或重定向到下载 URL。

### GET /reports/templates

可用模板列表。

**响应 200**：
```json
{
  "templates": [
    {"id": "daily", "name": "日报模板"},
    {"id": "weekly", "name": "周报模板"}
  ]
}
```

## 📈 仪表盘

### GET /dashboard/overview

概览指标。

**响应 200**：
```json
{
  "total_analyses": 12,
  "total_docs": 2847,
  "sentiment_pos": 65,
  "sentiment_neg": 12,
  "sentiment_neu": 23,
  "success_rate": 0.92
}
```

### GET /dashboard/trend?from=2026-09-01&to=2026-09-22

趋势数据。

**响应 200**：
```json
{
  "dates": ["2026-09-01", "2026-09-02", "..."],
  "counts": [120, 135, "..."],
  "scores": [0.65, 0.68, "..."]
}
```

### GET /dashboard/sources

来源分布。

**响应 200**：
```json
{
  "sources": [
    {"name": "微博", "count": 800, "pct": 0.35},
    {"name": "新闻", "count": 600, "pct": 0.26},
    {"name": "小红书", "count": 500, "pct": 0.22}
  ]
}
```

### GET /dashboard/topics

热门话题。

**响应 200**：
```json
{
  "topics": [
    {"name": "产品质量", "doc_count": 320, "trend": "rising"},
    {"name": "价格讨论", "doc_count": 180, "trend": "stable"}
  ]
}
```

## 💳 计费

### GET /billing/plans

套餐目录 + 加购 SKU + 支付渠道。

**响应 200**：
```json
{
  "plans": [
    {
      "code": "free",
      "name": "体验版",
      "price_monthly_cny": 0,
      "credits_per_cycle": 0,
      "analysis_mode": "quick"
    },
    {
      "code": "lite",
      "name": "速览版",
      "price_monthly_cny": 9900,
      "credits_per_cycle": 4,
      "analysis_mode": "quick"
    },
    {
      "code": "pro",
      "name": "研判版",
      "price_monthly_cny": 99900,
      "credits_per_cycle": 10,
      "analysis_mode": "full"
    },
    {
      "code": "enterprise",
      "name": "旗舰版",
      "price_monthly_cny": 499900,
      "credits_per_cycle": 50,
      "analysis_mode": "full",
      "priority_queue": true
    }
  ],
  "addons": [
    {
      "code": "addon_report",
      "name": "报告加购包",
      "kind": "addon",
      "price_cents": 6900,
      "credits": 1
    }
  ],
  "channels": ["alipay", "wechat", "unionpay"]
}
```

### GET /billing/credits

额度余额与当前套餐。

**响应 200**：
```json
{
  "balance": 7,
  "plan_code": "pro"
}
```

### GET /billing/transactions

额度流水（新→旧，最多 100 条）。

**响应 200**：
```json
{
  "transactions": [
    {
      "id": "01ARZ3...",
      "delta": -1,
      "reason": "consume",
      "analysis_id": "01ARZ3...",
      "balance_after": 6,
      "created_at": "2026-09-22T08:00:00Z"
    },
    {
      "id": "01ARZ2...",
      "delta": 4,
      "reason": "purchase",
      "reference_id": "order_01ARZ1...",
      "balance_after": 7,
      "created_at": "2026-09-22T07:00:00Z"
    }
  ],
  "total": 12
}
```

**reason 类型**：
- `trial` - 新注册试用
- `grant` - 管理员发放
- `purchase` - 套餐购买/加购
- `consume` - 分析任务扣减
- `refund` - 管线失败回补

### POST /billing/orders

创建支付订单。

**请求**：
```json
{
  "sku_code": "lite",
  "channel": "alipay"
}
```

**响应 201**：
```json
{
  "order": {
    "id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    "amount_cents": 9900,
    "credits": 4,
    "state": "pending",
    "qr_code_url": "https://qr.alipay.com/bax08471...",
    "channel": "alipay",
    "expires_at": "2026-09-22T08:15:00Z"
  }
}
```

### GET /billing/orders/:id

订单详情（**轮询即对账**：pending 订单顺带向渠道主动查单）。

**响应 200**：
```json
{
  "order": {
    "id": "01ARZ3...",
    "state": "paid",
    "amount_cents": 9900,
    "credits": 4,
    "channel": "alipay",
    "paid_at": "2026-09-22T08:03:21Z",
    "provider_txn_id": "2024092222001234567890123456"
  }
}
```

**state 枚举**：`pending` / `paid` / `expired` / `canceled`

### POST /callbacks/payment/:channel

支付渠道异步回调（**免鉴权**，安全完全依赖渠道验签）。

**仅由支付宝/微信/银联服务器调用**。

**响应**：渠道要求的 ACK 格式（验签/金额不符返回 400）。

### GET /billing/orders/:id/paypage?token=<access_token>

银联收银台跳转页（HTML 表单自动提交）。

浏览器跳转无法带 `Authorization` 头，改用 query `token`。

## 🔥 热榜

### GET /trends

多平台热榜快照（纯内存缓存，5 分钟刷新；无租户维度，登录即可看）。

**响应 200**：
```json
{
  "platforms": [
    {
      "name": "微博",
      "status": "ok",
      "updated_at": "2026-09-22T08:00:00Z",
      "items": [
        {
          "rank": 1,
          "title": "中秋国庆假期安排出炉",
          "url": "https://s.weibo.com/weibo?q=%23...",
          "hot": "486.2万"
        }
      ]
    },
    {
      "name": "知乎",
      "status": "stale",
      "updated_at": "2026-09-22T07:45:00Z",
      "items": [...]
    },
    {
      "name": "B站",
      "status": "error",
      "items": [],
      "error": "该平台暂不可用"
    }
  ]
}
```

**三态语义**：
- `ok` - 实时数据
- `stale` - 上次成功快照（`updated_at` 为真实新鲜度）
- `error` - 无数据

**RSSHub 不可达时仍返回 200**（数据源状态，非服务器故障）。

服务未配置（`rsshub_base` 空）→ `503 TRENDS_UNAVAILABLE`。

## 👨‍💼 管理员

权限：`platform_admin` 角色

### GET /admin/tenants

租户列表。

**响应 200**：
```json
{
  "tenants": [
    {
      "id": "01ARZ3...",
      "name": "企业A",
      "state": "active",
      "plan_code": "pro",
      "created_at": "2026-09-01T00:00:00Z"
    }
  ],
  "total": 10
}
```

### POST /admin/tenants/:id/suspend

挂起租户。

**响应 200**：
```json
{
  "id": "01ARZ3...",
  "state": "suspended"
}
```

挂起后该租户所有用户登录返回 `403 TENANT_SUSPENDED`。

### POST /admin/tenants/:id/resume

恢复租户。

**响应 200**：
```json
{
  "id": "01ARZ3...",
  "state": "active"
}
```

### GET /admin/settings

数据源与支付渠道配置。

**响应 200**：
```json
{
  "bocha_api_key": "sk-xxx",
  "llm_api_key": "sk-yyy",
  "llm_base_url": "https://open.bigmodel.cn/api/paas/v4",
  "llm_model": "glm-5.3-flash",
  "payment_alipay": "{\"enabled\":true,\"app_id\":\"2021...\"}",
  "payment_wechat": "{\"enabled\":false,...}",
  "payment_unionpay": "{\"enabled\":false,...}"
}
```

### PUT /admin/settings

更新配置（零重启生效）。

**请求**：
```json
{
  "bocha_api_key": "sk-new-key",
  "llm_model": "deepseek-chat",
  "payment_alipay": "{\"enabled\":true,...}"
}
```

**响应 200**：
```json
{
  "message": "settings updated"
}
```

支付渠道值为 **JSON 字符串**（各渠道结构不同），保存即时生效。

## ❌ 错误响应

所有错误使用统一信封：

```json
{
  "code": "FORBIDDEN",
  "message": "insufficient permissions",
  "details": null,
  "request_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"
}
```

### 错误码表

| Code | HTTP | 含义 |
|------|------|------|
| `UNAUTHORIZED` | 401 | Token 缺失或无效 |
| `FORBIDDEN` | 403 | 权限不足或租户被挂起 |
| `TENANT_SUSPENDED` | 403 | 租户已被管理员挂起 |
| `NOT_FOUND` | 404 | 资源不存在 |
| `NO_CREDITS` | 402 | 额度余额不足 |
| `BUDGET_EXCEEDED` | 429 | Token 配额已用完（LLM） |
| `QUOTA_EXCEEDED` | 429 | 并发/次数配额已满 |
| `CONFLICT` | 409 | 资源冲突（如终态任务不可跃迁） |
| `TRENDS_UNAVAILABLE` | 503 | 热榜服务未配置 |
| `INTERNAL` | 500 | 服务器内部错误 |

## 🔧 API Key 认证

### 创建 API Key

```bash
POST /api/v1/apikeys
Authorization: Bearer <jwt-token>
Content-Type: application/json

{
  "name": "生产环境 Key",
  "scopes": ["analyses:create", "analyses:read"]
}

# Response 201
{
  "key": {
    "id": "01ARZ3...",
    "name": "生产环境 Key",
    "key": "pangu_01ARZ3NDEKTSV4RRFFQ69G5FAV",
    "scopes": ["analyses:create", "analyses:read"],
    "created_at": "2026-09-22T08:00:00Z"
  }
}
```

**仅返回一次原始 Key**，后续只能查看 ID 和前缀。

### 使用 API Key

```bash
curl https://yuqing2.pangu-cloud.com/api/v1/analyses \
  -H "Authorization: Bearer pangu_01ARZ3NDEKTSV4RRFFQ69G5FAV"
```

API Key 自动映射到 `api_service` 角色（最小权限）。

### 撤销 API Key

```bash
DELETE /api/v1/apikeys/:id
Authorization: Bearer <jwt-token>

# Response 200
{
  "message": "key revoked"
}
```

## 📖 相关文档

- [[快速开始|Quick-Start]] - 5 分钟上手
- [[架构设计|Architecture]] - 理解 API 背后的系统架构
- [[收费体系|Billing-System]] - 额度包计费详解
- [[热榜聚合|Trending-Topics]] - `/trends` 端点实现原理
