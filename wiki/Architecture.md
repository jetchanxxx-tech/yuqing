# 架构设计

盘古舆情采用 **模块化单体（Modular Monolith）** 架构，平衡了微服务的模块化优势与单体的部署简洁性。

## 📐 整体架构

```
┌─────────────────────────────────────────────────────────┐
│                     前端层 (React)                       │
│         Vite Dev :5173 / Nginx 生产静态文件               │
└────────────────────┬────────────────────────────────────┘
                     │ HTTPS/HTTP
┌────────────────────▼────────────────────────────────────┐
│              Go 平台层 (:8080)                           │
│  ┌─────────────────────────────────────────────────┐   │
│  │ API Layer (Gin Router)                          │   │
│  │  ├─ /api/v1/auth     (JWT + ApiKey 双认证)     │   │
│  │  ├─ /api/v1/analyses (分析任务 CRUD)           │   │
│  │  ├─ /api/v1/billing  (额度包 + 支付)           │   │
│  │  ├─ /api/v1/trends   (热榜聚合)                │   │
│  │  └─ /api/v1/admin    (平台管理)                │   │
│  └─────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────┐   │
│  │ Business Layer                                   │   │
│  │  ├─ analysis   (状态机 + 管线)                  │   │
│  │  ├─ report     (套餐 gating)                    │   │
│  │  ├─ dashboard  (实时聚合)                       │   │
│  │  └─ alert      (阈值检查)                       │   │
│  └─────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────┐   │
│  │ Platform Layer                                   │   │
│  │  ├─ auth       (注册 + 三连 + RBAC)             │   │
│  │  ├─ tenant     (多租户 + 状态机)                │   │
│  │  ├─ billing    (额度包 + 套餐)                  │   │
│  │  ├─ payment    (三渠道 + 验签)                  │   │
│  │  └─ settings   (零重启配置)                     │   │
│  └─────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────┐   │
│  │ 分析管线 (同进程 Pipeline)                       │   │
│  │  queued → acquiring_budget → fetching →         │   │
│  │  analyzing → generating_report → completed       │   │
│  └─────────────────────────────────────────────────┘   │
└────────┬───────────────────────────┬──────────────────┘
         │ HTTP                       │ HTTP
    ┌────▼────────┐            ┌─────▼──────────────────┐
    │ PostgreSQL  │            │  Python 引擎层 (5端口)  │
    │   平台库    │            │  ├─ query   :8000      │
    │ (租户表带   │            │  ├─ media   :8001      │
    │  tenant_id) │            │  ├─ insight :8002      │
    │             │            │  ├─ report  :8003      │
    │  Redis      │            │  └─ forum   :8004      │
    │  (缓存)     │            │                         │
    └─────────────┘            │  Scrapling + Bocha     │
                               │  FastAPI + LLM Client  │
                               └────────────────────────┘
                                        │
                                ┌───────┴────────┐
                                │   外部 API     │
                                │ ├─ Bocha 搜索  │
                                │ ├─ 智谱 GLM    │
                                │ ├─ 支付宝      │
                                │ ├─ 微信支付    │
                                │ └─ 银联        │
                                └────────────────┘
```

## 🏗️ 分层边界（关键设计）

### Platform vs Business 双向隔离

```
internal/
├── platform/    ← 平台库（跨租户资源）
│   ├── auth/    ── 用户认证、角色、权限
│   ├── tenant/  ── 租户生命周期、状态机
│   ├── billing/ ── 套餐定义、额度包
│   ├── payment/ ── 三渠道支付、验签、对账
│   └── settings/── 零重启配置（Bocha/LLM）
│
├── business/    ← 业务库（租户隔离资源）
│   ├── analysis/── 分析任务、状态机、管线
│   ├── report/  ── 报告生成、格式 gating
│   ├── dashboard/─ 看板聚合
│   └── alert/   ── 阈值告警
│
├── engine/      ← 引擎契约（Go ↔ Python HTTP）
│   ├── contract.go  ── 6 个接口定义
│   └── real.go      ── HTTP 实现（180s 超时）
│
├── api/         ← HTTP 层（薄壳）
│   ├── v1/      ── Handlers（DTO 校验 → service）
│   └── middleware/─ authn, tenant, RBAC, audit
│
└── pkg/         ← 共享基础设施
    ├── db/      ── pgx 连接池、迁移
    ├── queue/   ── 内存队列（长期换 Redis）
    ├── llm/     ── MeteredProvider（预算检查）
    └── errors/  ── Sentinel 错误 + Wrap
```

**铁律**：
- ❌ Platform 层**不得** import Business 层
- ❌ Business 层**不得** import Platform 层
- ✅ 唯一例外：`report → billing`（定价查询，长期下沉 pkg）

### 依赖注入（Composition Root）

`platform/internal/app/container.go` 的 `Build()` 是全部服务的**唯一装配点**：

```go
func Build(cfg *config.Config, logger *log.Logger) (*Container, error) {
    // 1. 基础设施
    db := db.NewPool(cfg.Database)
    queue := queue.NewMemoryQueue()
    
    // 2. Store 层（memory/postgres 切换）
    authStore := auth.NewMemoryStore()  // 或 NewPgxStore(db)
    tenantStore := tenant.NewSharedStore()
    
    // 3. Platform 服务
    authSvc := auth.NewService(authStore, tenantStore)
    billingSvc := billing.NewService(creditStore, orderStore, paymentProviders)
    
    // 4. Business 服务（注入 Platform 依赖）
    analysisSvc := analysis.NewService(
        analysisStore,
        engineFetcher,   // HTTP → Python
        insightAnalyzer, // HTTP → Python
        reportGenerator, // HTTP → Python
    )
    
    // 5. 管线启动（仅当引擎配置时）
    if cfg.Engines.Query.URL != "" {
        pipeline := analysis.NewPipeline(analysisSvc, queue)
        pipeline.Start()
    }
    
    return &Container{
        Auth: authSvc,
        Analysis: analysisSvc,
        Billing: billingSvc,
        // ...
    }
}
```

## 🔄 分析管线（核心流程）

### 为什么在 server 进程内？

**原因**：内存 store 与内存 queue 都是**进程私有**的。

```
# ❌ 错误架构（当前无法工作）
yuqing-server    → queue.Publish(msg)  // 写入进程 A 的内存队列
yuqing-worker    → queue.Subscribe()   // 订阅进程 B 的内存队列（空！）

# ✅ 当前架构（MVP 阶段）
yuqing-server    → queue.Publish(msg)  // 同进程发布
  └─ pipeline    → queue.Subscribe()   // 同进程消费
```

**长期演进**：接入 PostgreSQL store + Redis/RabbitMQ 队列后，管线可搬回独立 worker 进程。

### 状态机与进度

```go
// 9 个状态
const (
    StateDraft               = "draft"                 // 0%
    StateQueued              = "queued"                // 0%
    StateAcquiringBudget     = "acquiring_budget"      // 10%
    StateFetching            = "fetching"              // 25%
    StateAnalyzing           = "analyzing"             // 60%
    StateGeneratingReport    = "generating_report"     // 85%
    StateCompleted           = "completed"             // 100%
    StateFailed              = "failed"                // -
    StateCanceled            = "canceled"              // -
)
```

**状态跃迁规则**：
- `draft` → `queued`（用户提交）
- `queued` → `acquiring_budget`（管线启动）
- 任意 active 状态 → `failed`（引擎报错）
- 任意 active 状态 → `canceled`（用户取消）
- 终态（completed/failed/canceled）不可跃迁（`ErrConflict`）

### 管线步骤详解

```go
func (p *Pipeline) Handle(ctx context.Context, msg TaskMessage) error {
    // 1. acquiring_budget (10%)
    //    - 检查额度余额
    //    - 扣减 1 次（Create/Rerun 各扣一次）
    //    - 失败 → 自动回补
    
    // 2. fetching (25%)
    //    - HTTP → Python query_engine /search
    //    - Bocha 搜索关键词 → URL 列表
    //    - Scrapling 抓取正文（adaptive 自适应选择器）
    //    - 正文失败 → 降级用 Bocha snippet
    //    - 返回 []Document → store.AddDocuments()
    
    // 3. analyzing (60%)
    //    - HTTP → Python insight_engine /analyze
    //    - 情感分析（temperature=0 确定性）
    //    - 话题聚类（LLM + 关键词提取）
    //    - 五维研判（5 个独立 LLM 调用，并发闸门 3）
    //    - 批判重写摘要
    //    - 引用保真校验（编造引语丢弃）
    //    - 失败降级：部分维度成功仍保存，记 warning
    
    // 4. generating_report (85%)
    //    - HTTP → Python report_engine /generate
    //    - LLM 生成研判结论
    //    - Jinja2 模板渲染 HTML
    //    - 失败降级：纯数据报告（无 LLM 结论）
    
    // 5. completed (100%)
    //    - 写入最终状态
    //    - SSE 推送 final 事件
}
```

## 🔐 认证与授权

### 双通道认证

```go
// middleware.AuthAny：JWT 或 ApiKey
router.Use(middleware.AuthAny(authSvc, apikeySvc))

// 分流逻辑
if strings.HasPrefix(token, "pangu_") {
    // API Key 验证（SHA-256 哈希比对）
    // 映射到最小权限角色 api_service
    user = apikeySvc.ValidateKey(token)
} else {
    // JWT 验证（golang-jwt/v5）
    user = authSvc.Authenticate(token)
}
```

### RBAC 权限矩阵

| 角色 | 权限 |
|------|------|
| `platform_admin` | 全部权限（admin:*） |
| `tenant_admin` | 租户管理、分析、报告、计费 |
| `tenant_member` | 分析查看、报告下载（只读） |
| `api_service` | 分析提交、结果查询（API Key 专用） |

**权限检查**：
```go
middleware.RequirePermission("analyses:create")
middleware.RequirePermission("admin:tenants:suspend")
```

### 引导管理员

```bash
# systemd drop-in: yuqing-server.service.d/bootstrap-admin.conf
Environment="YUQING_BOOTSTRAP_ADMIN_EMAIL=admin@pangu.com"
```

该邮箱注册时自动获得 `platform_admin` 角色，无需手工 SQL 授权。

## 💾 数据持久化

### 双 Store 架构

```yaml
# config.yaml
store:
  driver: memory  # 或 postgres
```

| Driver | 适用场景 | 特点 |
|--------|---------|------|
| `memory` | 本地开发、测试 | 零依赖、进程重启丢失 |
| `postgres` | 生产环境 | 持久化、重启不丢数据 |

### PostgreSQL 模式

**当前实现**：单库多租户（业务表带 `tenant_id` 列）

```sql
-- 平台库：yuqing_platform
CREATE TABLE users (...);
CREATE TABLE tenants (...);
CREATE TABLE platform_settings (...);

-- 业务表（带 tenant_id）
CREATE TABLE analyses (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    ...
);
```

**长期演进**：database-per-tenant 物理隔离
- 租户独立数据库
- 更强隔离、更灵活扩展
- 迁移工具自动创建租户库

## 🔌 引擎通信协议

### Go → Python HTTP

```go
// platform/internal/engine/contract.go
type CrawlerEngine interface {
    Search(ctx, keywords, sources []string) ([]Document, error)
}

type InsightAnalyzer interface {
    Analyze(ctx, docs []Document, mode string) (*InsightResult, error)
}

// platform/internal/engine/real.go
type RealCrawlerEngine struct {
    baseURL    string  // http://127.0.0.1:8000
    httpClient *http.Client  // Timeout: 180s
}

func (e *RealCrawlerEngine) Search(...) ([]Document, error) {
    resp := httpPost(e.baseURL + "/search", payload)
    return parseDocuments(resp)
}
```

### 超时配置

| 引擎 | 超时 | 原因 |
|------|------|------|
| query | 180s | Bocha + 逐 URL 抓取含重试 |
| insight | 420s | GLM 思考型五维实测 266s |
| report | 420s | LLM 生成研判结论 |

## 📊 可观测性

### 日志

```go
import "log/slog"

logger.Info("pipeline started",
    "analysis_id", id,
    "tenant_id", tenantID,
    "state", "fetching")

logger.Warn("insight partial failure",
    "analysis_id", id,
    "failed_dimensions", []string{"background", "heat"})
```

### 指标（计划中）

- 分析任务：创建/完成/失败率
- 管线步骤：各步骤耗时分布
- LLM 调用：token 消耗、成本
- 爬虫：采集成功率、平均文档数

### 追踪（计划中）

- Request ID：全链路追踪
- Trace：Go → Python 引擎调用链

## 🧪 测试金字塔

```
        E2E (27 用例)
       Playwright 生产站点
      ╱                    ╲
     集成测试 (6 用例)
    全链路/计费/隔离/并发
   ╱                        ╲
  契约测试 (50+ 用例)
 锁 endpoint 结构 + RBAC
╱                            ╲
单元测试 (353 用例 Go + 54 Python)
     TDD 红绿重构
```

详见 [[测试指南|Testing-Guide]]。

## 🚀 部署架构

### Ubuntu 24.04 标准部署

```
┌─────────────────────────────────────────┐
│ nginx :80/:443                          │
│  ├─ / → /opt/yuqing/web/dist           │
│  ├─ /api → http://127.0.0.1:8080       │
│  └─ /.well-known → acme.sh 证书        │
└─────────────────┬───────────────────────┘
                  │
    ┌─────────────┼─────────────┐
    │             │             │
┌───▼───┐   ┌─────▼──────┐   ┌─▼────────┐
│  Go   │   │   Python   │   │ RSSHub   │
│ :8080 │   │ :8000-8004 │   │  :1200   │
└───┬───┘   └─────┬──────┘   └──────────┘
    │             │
┌───▼─────────────▼───┐
│ PostgreSQL :5432    │
│ Redis :6379         │
└─────────────────────┘
```

### yuqing2 生产环境（CentOS Stream 9）

特殊配置：
- **oneinstack 源码 nginx**（vhost include 机制）
- **PG15 PGDG 源**（`--nobest` OpenSSL 1.1.1 兼容）
- **Node.js 在 /usr/local/node/bin**（systemd 需全路径）
- **用户既有 MySQL 并存**（root/nishi250，勿动）

详见 [[生产部署|Production-Deployment]] §9。

## 📖 相关文档

- [[本地开发|Local-Development]] - 开发环境完整配置
- [[API 参考|API-Reference]] - REST API 详细文档
- [[测试指南|Testing-Guide]] - TDD 实践与测试分层
- [[配置参考|Configuration]] - 环境变量与配置文件
