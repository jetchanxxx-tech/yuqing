# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

**盘古舆情** — AI 原生 SaaS 舆情监测平台，面向中小企业与个人品牌。以 Scrapling 自适应爬虫 + Bocha AI 搜索实现真实数据采集，用多 Agent 辩论协作将"监测"升级为"研判"。

参考开源项目 BettaFish（GPL-2.0）架构灵感，从零构建平台层（Go），引擎层独立重写（Python，规避 GPL 传染）。爬虫层集成 Scrapling（BSD-3-Clause）。

> ⚠️ **仓库是公开的（github.com/jetchanxxx-tech/yuqing）** —— 绝不提交任何凭据。详见「凭据与安全」。

## Tech Stack

- **Platform**: Go 1.25, Gin, pgx v5, goose migrations, golang-jwt v5, argon2id
- **Engines**: Python 3.11+, FastAPI × 5 (query/media/insight/report/forum), Scrapling (爬虫)
- **Frontend**: React 19 + TypeScript + Vite 8 + Ant Design 5（含 v5-patch-for-react-19）+ ECharts 5（自写按需封装）+ TanStack Query + React Router 7 + Zustand
- **Database**: PostgreSQL 15（database-per-tenant 隔离；MVP 阶段为内存 store，见下）
- **Cache**: Redis 7
- **Search**: Bocha AI API（OpenAI 兼容）
- **Deploy**: Go 交叉编译二进制 + systemd，无容器。Nginx 反代 + 静态文件。

## Commands

```bash
# ── Go (platform/) ────────────────────────────────────────
cd platform
make test                            # go test ./... -count=1 -race （26 包；含用户中心 auth 契约 11+ 测试）
go test ./... -count=1               # 不带 -race 的快速跑
go test -run TestRegister ./internal/platform/auth/   # 单个测试
go test ./internal/api/v1/ -count=1  # 单个包（契约测试）
make test-cover                      # 覆盖率
make build                           # 交叉编译 linux/amd64 → bin/{yuqing-server,yuqing-worker,yuqing-cli}
make lint                            # = go vet ./...
go run ./cmd/server                  # 开发模式 (:8080) — 内存 store，无需 PG/Redis

# ── 前端 (web/) ───────────────────────────────────────────
cd web
npm ci && npm run build              # = tsc -b && vite build → dist/
npm run dev                          # Vite dev :5173（proxy /api → 127.0.0.1:8080）
npm run lint                         # oxlint

# ── E2E (web/e2e/) ────────────────────────────────────────
npx playwright test --config e2e/playwright.config.ts     # 本地 dev server 冒烟
npx playwright test --config e2e/production.config.ts     # 打生产站点（27 用例）
# 生产套件凭据**只能**来自环境变量，缺失时 spec 直接抛错：
#   E2E_EMAIL / E2E_PASSWORD / E2E_BASE_URL（默认 https://yuqing.pangu-cloud.com —— 老机域名已废弃，打生产时显式设为 https://yuqing2.pangu-cloud.com）
#   E2E_BOCHA_KEY 可选，用于 5.3 数据源写入验证

# ── Python engines (engines/) ─────────────────────────────
cd engines
python3 -m venv venv && source venv/bin/activate
pip install -r requirements.txt      # 含 scrapling[fetchers]
scrapling install --chromium         # 首次 ~150MB
python3 -m pytest tests/ -v          # 54 用例（scraper/llm_client/insight 含五维/报告）
# 开发：在具体引擎目录下 `uvicorn main:app --port 8000`
# 生产（systemd）：WorkingDirectory 与 PYTHONPATH 均为 /opt/pangu-source，
#   ExecStart=/opt/yuqing/engines/venv/bin/uvicorn engines.<name>_engine.main:app
# 端口：8000=query 8001=media 8002=insight 8003=report 8004=forum
BOCHA_API_KEY=sk-xxx uvicorn main:app --port 8000

# ── 演示与部署 ────────────────────────────────────────────
双击 demo/index.html                  # "雅阁后排" 7 步产品演示（零依赖，离线可用）
sudo YUQING_DOMAIN=<域名> bash scripts/deploy.sh   # 幂等部署（仅 Ubuntu；当前唯一生产 yuqing2 为 CentOS，流程见 RUNBOOK §9）
```

## Architecture: Modular Monolith

`platform/cmd/` 下三个 Go 二进制，由 `platform/Makefile` 交叉编译：

| Binary | Role |
|--------|------|
| `yuqing-server` | HTTP API + **分析管线（同进程）** |
| `yuqing-worker` | Queue 消费者（当前无分析任务可消费，见「分析管线」） |
| `yuqing-cli` | 运维 CLI |

### 组合根（DI 唯一装配点）

`platform/internal/app/container.go` 的 `Build(cfg, logger)` 是全部服务实现的唯一装配处：

- 共享 tenant store：`auth.NewSharedTenantStore` 让注册与 admin 读同一份数据
- Report 套餐 gating 双闭包注入：`planCodeFor` + `planProvider`，未知租户 fail-closed
- Alert EmailSender 为 nil → 静默丢弃（SMTP 未接）
- Platform Settings：从环境变量种子（`BOCHA_API_KEY`/`DEEPSEEK_API_KEY`），admin 可在线覆盖（存 PG 的 `platform_settings` 表，重启不丢）。**server unit 必须带 `EnvironmentFile=engines.env`** —— 缺它时种子为空，后台「数据源配置」永远显示未配置（实测踩坑）
- 引导管理员：`YUQING_BOOTSTRAP_ADMIN_EMAIL` 指定的邮箱注册即得 `platform_admin`
- **管线仅当 `cfg.Engines.Query.URL != ""` 时启动** —— 测试用的精简配置不含该地址，强行启动会让管线发真实 HTTP 并快速失败，破坏断言 `queued` 的用例

### 分析管线（关键）

`platform/internal/business/analysis/pipeline.go` + `platform/internal/app/pipeline.go`。

```
POST /analyses → store 写入(state=queued) → queue 发布 TaskMessage{analysis_id, tenant_id}
   → 同进程 Pipeline.Handle：
        acquiring_budget(10) → fetching(25) → analyzing(60) → generating_report(85) → completed(100)
        任一步失败 → failed（超时归为错误码 "timeout"）
```

**为什么必须在 server 进程内**：内存 store 与内存 queue 都是**进程私有**的。独立 worker 进程既收不到 server 发布的消息，也看不到 server 创建的任务 —— 这正是「任务永久停在 queued」的根因（实测 worker 收到任务数为 0）。接入 PostgreSQL store + 远程队列后可搬回独立 worker，管线自身无需改动。

其他约定：
- `TaskMessage` 必须携带 `tenant_id`：store 按租户分桶，仅凭 analysis_id 无法定位。`DecodeTaskMessage` 兼容历史的裸 ID 格式，但调用方会拒绝无 tenant_id 的消息并记 warn
- 管线对已终态任务幂等（重复投递直接跳过）
- 进度百分比常量定义在 `pipeline.go`，前端据此渲染进度条
- `analyzing` 调 `InsightAnalyzer`（情感/话题/摘要），`generating_report` 调 `ReportGenerator`（HTML 报告）。两者**失败不致命**：任务仍 completed，warning 记录降级原因（采集结果不能因分析失败丢弃）。引擎未配置（URL 为空）时对应步骤跳过并记 warning
- 洞察/报告经 `app/pipeline.go` 的 adapter 适配（engine → analysis 接口），business 层不 import engine 包
- **warning 语义**：warn 只描述降级程度，不决定「是否保存」—— 洞察结果非空（哪怕部分维度失败）即 SetInsight；`InsightAvailable` = 有产出而非零告警
- **引用保真**：维度「逐字原声」必须是源文档正文的归一化子串（去空白比较），编造引语丢弃并记 warning（`insight_engine._verify_quotes`）
- 维度材料包总预算 16k 字符（发布时间最新优先截断，显式标记）；维度调用 `max_tokens=8192`、并发闸门 3 + 瞬时错误重试一次
- **LLM 供应商可配置**（默认智谱 GLM `glm-5.3-flash`，DeepSeek 兼容回退）：三级来源 = 请求参数（后台三字段零重启透传）→ 环境变量 `LLM_API_KEY/LLM_BASE_URL/LLM_MODEL` → 代码默认。Admin UI「数据源配置」有 Key/端点/模型三字段
- ⚠️ GLM 思考型坑：`glm-5.3-flash` 先产 reasoning 再输出 content，max_tokens 是两者之和 —— 4096 在真实语料下被 reasoning 吃光致 content 空（4/5 维度失败，生产实测）；已配 8192 且 `finish_reason=length` 显式报「输出被截断」
- ⚠️ 传输超时坑：Go→insight/report 的 http.Client 超时 **420s**（GLM 思考型五维实测 266s，180s 会截断；改引擎侧配置时要同步核对）

## Layer Boundaries (critical)

```
internal/
├── platform/    ← 平台库 (tenants, users, billing, settings, apikey)
│   auth, tenant, user, usage, billing, settings, apikey
├── business/    ← 租户库 (analyses, documents, reports)
│   analysis, datasource, crawler, sentiment, report, dashboard, alert
├── engine/      ← Go 契约 + HTTP transports (real.go → Python /search)
├── api/         ← thin handlers: DTO 校验 → service → respondError 信封
│   middleware/  ← authn(JWT/ApiKey 双认证), tenant, RBAC, ratelimit, audit
└── pkg/         ← db, queue, llm, email(Resend/SMTP), sms(阿里/腾讯), storage, id, errors, observ
```

**Platform vs Business 双向禁止跨层 import**。已知例外仅 1 处：`business/report → platform/billing`（定价目录下沉 `pkg/` 是长期方案，当前用函数注入缓解，见 `docs/dev/reviews/REVIEW_REPORT.md` §8.4）。

## Engine 实现状态（勿假设"引擎可用"）

Python 引擎**不是**同等完成度。改动前先确认：

| 引擎 | 端口 | 状态 |
|------|------|------|
| `query_engine` | 8000 | ✅ **真实实现** — Bocha 搜索 + Scrapling 抓取，实测收集 19 条真实中文文档 |
| `insight_engine` | 8002 | ✅ **真实实现** — LLM 情感/话题（temp 0）+ **五维度独立分析**（专属人设×5 + 五段骨架，并发闸门 3）+ 批判重写摘要；确定性 trend + 引用保真校验 |
| `report_engine` | 8003 | ✅ **真实实现** — LLM 研判 + HTML 模板渲染；LLM 失败降级为纯数据报告 |
| `forum_engine` | 8004 | ⚠️ Mock — 预置"雅阁后排"4 Agent × 3 轮辩论 |
| `media_engine` | 8001 | ⚠️ Mock — 5 条预置多模态结果 |

`/analyses/:id/result` 返回 `summary`/`sentiments`(计数+明细)/`topics`/`dimensions`(五维研判)/`report`(HTML)/`warning`。前端详情页「五维研判」Tab 渲染维度结论（Collapse 折叠面板）。

## Key Design Patterns

### 数据采集链路（Scrapling + Bocha）

```
Go Pipeline (state=fetching)
  → engineFetcher → RealCrawlerEngine.Search()  [HTTP, bochaKeyFunc 注入]
  → Python query_engine /search
     1. Bocha API 搜索关键词 → URL 列表
     2. Scrapling 抓取正文 (Fetcher 静态 / DynamicFetcher JS / StealthyFetcher 反爬)
     3. adaptive=True 自适应选择器 + content_hash 去重
     4. 正文抓取失败 → 退回用 Bocha snippet 兜底，保证结果不丢
  → []Document 回传 Go → Service.AddDocuments() 存入 documentStore
```

- `engines/common/scraper.py` `PageScraper` — Scrapling 封装（错误降级、去重、source 路由）
- **Bocha 接口事实**（踩坑后确认）：端点 `https://api.bochaai.com/v1/web-search`（**不是** `/v1/ai/search`，那个返回 404）；响应结构 `data.webPages.value[]`，字段是 `name`/`url`/`snippet`（**不是** `title`）
- Bocha key 三级来源：环境变量 `BOCHA_API_KEY` → 平台 settings（`PUT /api/v1/admin/settings`，零重启生效）→ 请求参数。Admin UI 在「管理后台 → 数据源配置」
- 文档存在 `analysis.documentStore`（内存），`Documents()` 返回 `[]` 而非 nil —— 前端 `.map()` 遇 null 会崩

### 服务层（memory/postgres 双 store，TDD）

`store.driver: memory|postgres` 切换（config.yaml）。postgres 模式全部服务落平台库
（`yuqing_platform`，业务表带 tenant_id 列；database-per-tenant 物理隔离是长期演进），
重启不丢账号/任务/文档 —— 生产已启用。pgx store 契约测试用 `YUQING_TEST_PG_URL`
gate（未设置时 skip，本地无 PG 也能全绿）。

| 服务 | 核心职责 |
|------|---------|
| `auth.Service` | Register(User+Tenant+Member 三连+SetQuota 1M)/Login/Authenticate/Refresh |
| `tenant.Service` | Get/List/Suspend/Resume（状态机校验） |
| `analysis.Service` | Create/Get/List/Cancel/Rerun + `advance`/`markFailed`（管线写路径）；`Create` 发布 `TaskMessage` |
| `report.Service` | CreateFromAnalysis/DownloadURL — 套餐 format gating fail-closed |
| `dashboard.Service` | 实时计算 Overview/Trend（zero-value 数组防 ECharts null） |
| `alert.Service` | Create/Check（negPct ≥ threshold 触发 + sender.Send） |
| `apikey.Service` | CreateKey(pangu_+ULID, 只返回一次)/ValidateKey(fail-closed)/RevokeKey(跨租户 404) |
| `usage.Meter` | Record/BudgetStatus/Aggregate（全租户聚合供 /admin/usage） |
| `auth.Service`（用户中心） | ChangePassword/邮箱验证（token 一次性）/GetProfile/UpdateProfile/SendPhoneCode/BindPhone/UnbindPhone —— 依赖经 `EnableUserCenter()` 装配，未装配时全部方法 fail-closed |

存储实现为 memory/postgres 双轨（见上 store.driver）。**凡给 PG 写的新 store 方法必须同步补契约测试**（auth 的范式见 `usercenter_pg_test.go`，用 `YUQING_TEST_PG_URL` gate 在装了 PG 的机器/生产上跑）—— v0.1.1 连续 4 个生产 PG bug 全部源于 PG 路径零覆盖，此为血泪教训。

### LLM Metered Provider (API resale)

每次 LLM 调用：预算检查（hard_cap fail-closed）→ Chat → `Meter.Record`。成本**按 token 类别分档**（prompt × 输入价 + completion × 输出价），双字段 `cost_micro_cny` / `billed_micro_cny`。Free/Pro = hard_cap，Business = overage，Enterprise = 无 cap。`FakeProvider` 返回固定"雅阁后排"摘要（token 绕过，联调前勿动）。

### Analysis State Machine

```
draft → queued → acquiring_budget → fetching → analyzing → generating_report → completed
any active state → failed | canceled
```

`Transition` 是唯一写路径；终态不可跳转（`ErrConflict`）。

### SSE 实时推送

`GET /analyses/:id/events` — text/event-stream，`SSEPollInterval`（默认 1s）轮询状态机，state/progress 变化发 `event: progress`，终态发 `event: final` 后关闭。客户端断开（ctx.Done）停止。多实例部署时换 Redis pub/sub。nginx 需 `X-Accel-Buffering: no`。

### 认证双通道

`middleware.AuthAny`：`pangu_` 前缀分流到 ApiKey 验证（映射最小权限 `api_service` 角色），其余走 JWT。API Key 存 SHA-256 哈希，创建时仅返回一次原始值。

### 用户中心（auth.EnableUserCenter 装配，v0.1.1-beta 上线）

- **依赖注入语义**：`Service` 内的 userStore/verifications/smsSender/emailSender 全部可选，未装配时相关方法 fail-closed 返回 ErrInternal（绝不 panic）；组合根在 container.go 调 `EnableUserCenter()`
- **VerificationStore 语义化接口**（SaveEmailToken/LoadEmailToken/ConsumeEmailToken/SaveSMSCode/...）：内存版（开发）与 PG 版（生产，verification_tokens + sms_verification_codes 两表）双实现；生产长期可换 Redis。防刷语义 = 「同键覆盖」而非 EXCLUDE 约束（EXCLUDE 曾因 now() 非 IMMUTABLE 与重发场景双重问题被删除）
- **邮件/短信通道**：`app/usercenter.go` 的 settingsMailer/settingsSMS **每次发送时从 platform_settings 动态构建 Provider**（Resend/SMTP、阿里云/腾讯云，admin 后台「通知服务」改配置零重启生效）—— 项目定位独立部署产品，外部服务一律在线配置不硬编码；凭据未配置时 fail-closed 报 "not configured"
- **防刷**：Service 层进程内 senderThrottle（同目标 60s 一次 → 429 QUOTA_EXCEEDED）+ codeTries（验证码错 ≥5 次作废）；单实例语义，多实例部署须换 Redis
- **方案 B 试用**：邮箱未验证用户可创建 1 次分析（`trial_analysis_used` 0→1 原子扣减），验证后不限
- **唯一登录方式保护**：邮箱未验证时不允许解绑手机号（409）；解绑需登录密码（防会话劫持）
- ⚠️ 已知遗留：改密码不吊销旧 JWT（Y3，修法 = JWT iat 对比 PasswordChangedAt）；8 个端点未进契约测试（Y8）；腾讯云与阿里云共用 sms_access_key 键（Y6）

### 测试分层

| 层 | 位置 | 规模 |
|----|------|------|
| 单元 | 各包 `*_test.go` | TDD，表驱动 |
| 契约 | `api/v1/contract_test.go`、`result_test.go` | 锁 endpoint 响应结构 + RBAC 403 + gap registry |
| 集成 | `test/integration/` | 全链路/计费/隔离/并发 |
| Python | `engines/tests/`（4 个文件） | 54 用例：Scrapling + LLM 客户端 + 五维分析/引用保真/报告（FakeLLM 离线） |
| E2E | `web/e2e/production.spec.ts` | 27 用例，打生产站点 |

## MVP Scope

| # | Feature | Status |
|---|---------|--------|
| F01-F10 | 认证/任务/采集/看板/摘要/报告/计费/保留/后台/告警 | ✅ |
| F11 | 多 Agent 辩论 (ForumEngine) | ⚠️ Python mock（4Agent×3轮） |
| F12 | 多模态 (MediaEngine) | ⚠️ Python mock（5 预置结果） |
| F13 | SSE 实时推送 | ✅ 轮询实现 |
| F14 | API Key 管理 | ✅ pangu_ 格式 |
| F15 | /admin/usage 聚合 | ✅ Meter.Aggregate |
| F16 | 数据源在线配置（Admin UI） | ✅ Bocha + LLM 供应商（Key/端点/模型三字段）+ **通知服务**（邮件 Resend/SMTP、短信阿里/腾讯，凭据用户自备未配置时 fail-closed） |
| F17 | 情感分析 / 话题聚类 / 报告生成 | ✅ LLM 真实调用 + 五维研判，管线全链路已接入（生产实测 357s） |
| F18 | 收费体系（方案 B 渗透型） | ✅ beta 已实施**并部署生产**（2026-09-15，迁移 0006 已 applied）—— Lite 99·4次·quick / Pro 999·10次·full / Ent 4999·50次；加购 69/次（渗透定价）；credit 包（402/回补/防超卖）+ payment 包（三防核验：验签→金额→原子跃迁）；三渠道二维码（支付宝/微信/银联，`internal/platform/payment/`，admin 后台配置商户参数即时生效）；**支付渠道未真实联调（等商户账号）**。admin 账号策略：仅商务演示用，额度手工 SQL 发放（2026-09-22 已补至 500 次），无无限额度机制，发放走 grant 流水留痕 |
| F19 | 手机号注册 / 登录 | 📋 **下一版本迭代（用户 2026-09-15 指定）** —— **P0 基础已就绪（v0.1.1-beta）**：users.phone 列 + 短信验证码链路（阿里/腾讯可选）+ 绑定/解绑 + 防刷节流均已上线。剩余：手机号注册/登录/找回密码三路改造 + 登录态与验证码打通 |
| F20 | 企业实名认证 | 📋 **下一版本迭代规划（用户 2026-09-15 指定）** —— 现状：Enterprise 付费即开通，无认证流程。要做：认证表（营业执照/法人身份证/对公账户/凭证上传 + pending→approved→rejected 状态机）+ admin 审核界面 + Enterprise 购买联动；材料清单已给用户（执照/法人/对公打款或转账验证/经办人委托书/NDA 数据合规签署）；过渡期对公转账 + admin 人工开通 |
| F21 | 热榜聚合页（5 平台快照 + 跨平台搜索） | ✅ **已开发并部署生产**（2026-09-22，yuqing2.pangu-cloud.com）—— RSSHub 自建 unit（`LISTEN_INADDR_ANY=0` 只绑 127.0.0.1:1200 + Playwright Chromium）→ Go server 内存缓存（ticker 5min，三态 ok/stale/error）→ GET /api/v1/trends → 前端 `/trends` Tab + 分析预填钩子 + **跨平台关键字搜索框**（纯前端过滤，命中项带平台标记）。**平台清单 V2（2026-09-23）：微博 20 条/B站 10 条/知乎 20 条/新浪科技 20 条（/sina/rollnews）/36氪 20 条（/36kr/newsflashes）全 ok**——新平台准入须在生产 RSSHub 实测 3 连发；抖音/小红书未过准入线（需 Chromium+反爬）暂缓，IT之家/虎扑实测 503（路由在但上游失败）。代码审核 15 项发现全修复（cache 值语义消除数据竞争等） |
| F22 | Admin 成本计算器（LLM/爬虫单价统计换算） | 📋 方案已提待用户确认 —— 引擎响应透传 usage → analyses 表加 llm_in/out_tokens+bocha_calls 列（quick/full 分开）→ admin 单价配置（platform_settings）+ 实测单次报告成本 + 各套餐毛利换算。约 3-4 人天；历史分析无 usage 不可回填，从上线起积累 |
| — | PostgreSQL store（持久化） | ✅ pgx store 已接线（store.driver: postgres），生产重启不丢数据 |
| — | LLM 调用平台侧计量（MeteredProvider 接真实调用） | ❌ Python 引擎直连 LLM 供应商，Go 侧计量未接线（F22 是它的第一步） |

### 已知产品缺口（2026-09-23 三方评审定级，改动相关代码前必读）

| 缺口 | 代码真相 | 定级 |
|------|----------|------|
| **数据源标签错标** | `engines/common/scraper.py:104-107`：Bocha 请求只带关键词无来源参数，**全部结果贴 sources[0] 标签**；配额截断 bug 使勾选来源越多结果越少；6 选项中公众号/小红书/B站/抖音恒 0 结果 | P0 止血：按 URL 域名归类；P1：site: 限定（需生产准入实测） |
| **报告中心空白** | 管线只写 `analyses.report`（pipeline.go:269），**从不调** `report.Service.CreateFromAnalysis` → reports 表 0 行；`reports.go:62-82` 下载端点是回环桩；前端文案已承诺"自动生成"。契约测试曾把"空列表"断言为合法——半成品被固化为契约 | P0：接线 + HTML 下载（report_content 即存储）+ 存量回填 CLI |
| 分析类型无实质作用 | `analysis_type` 仅拼入 LLM 提示词（insight_engine main.py:167,438），维度只由 mode（quick/full）驱动；表单文案过度承诺 | P1：降为可选 tag + 类型注入五维人设；明确不做类型驱动维度 |
| 面板未绑用户 + 假数据 | dashboard 只按 tenantID 聚合（租户级是 ToB 正确默认，保留）；**Sources/Topics 是硬编码"雅阁后排"占位**（dashboard/service.go:14-27），换真需文档级聚合 | P2：created_by 筛选（迁移 0008）；换真 2-3 天 |
| analyses 无 user_id 列 | `CreateAnalysisRequest.UserID` 在 `Service.Create`（service.go:159-168）被丢弃，DTO→模型→store 三处未通；reports 归属（created_by）与"只看我的"筛选都依赖此列 | 迁移 0008 一次性补：analyses.created_by + reports.created_by |

修复方案全文：产品决策与工作量评估已评审定稿（P0 约 3-4 人天：报告闭环 + 来源标签止血）。**未拍板**：PDF 路线（PM 主张复用生产 Chromium 懒生成 vs 开发主张延后）、docx 排期、Rerun 报告覆盖策略。

## 部署与运维

```bash
sudo YUQING_DOMAIN=<域名> bash scripts/deploy.sh     # 幂等：已装组件 [SKIP]，已有配置不覆盖
```

- `scripts/deploy.sh` — Ubuntu 24.04，无 Docker。检测并跳过已装的 nginx/postgresql/redis/go/node；显式 `-o bin/yuqing-*` 生成二进制（`go build -o dir/ ./cmd/...` 会产出 `server`/`worker`/`cli`，与 unit 名不匹配导致服务静默启动失败）
- `scripts/nginx-ssl.conf` / `nginx-http.conf` — 有域名走 HTTPS，否则 HTTP-only。SSL 版含 `/.well-known/acme-challenge/` 直通location。nginx 1.24 用 `listen 443 ssl http2`（参数形式，`http2 on;` 指令 1.25 才有）
- `scripts/systemd/*.service` — 7 个 unit：`yuqing-{server,worker,query,media,insight,report,forum}`
- **证书**：acme.sh（Gitee 镜像安装，get.acme.sh 境内不通）。其 cron 每日检查，到期前 30 天自动续期并 reload nginx
- 迁移 0005：analyses.dimensions JSONB 列；迁移 0006：收费体系三表（report_credits/credit_transactions/orders）；**迁移 0007：用户中心（users 新列 + verification_tokens/sms_verification_codes/login_sessions 三表）** —— 部署顺序硬约束：先 `yuqing-cli migrate platform` 再起新 server
- ⚠️ **迁移三踩坑（v0.1.1 实测，详见 RUNBOOK §9.7）**：① yuqing-cli 用 embed FS 把迁移 SQL 编译进二进制——改服务器磁盘迁移文件无效，必须重编译 CLI；② PL/pgSQL `$$` 块必须加 `-- +goose StatementBegin/End`，否则 goose 分句报 42601（psql 试跑通过 ≠ goose 通过）；③ CLI 必须带 `YUQING_CONFIG=/opt/yuqing/config/config.yaml`。部署前用 psql 事务试跑（BEGIN...ROLLBACK）验语法
- server unit 已有 `public-base-url.conf` drop-in 注入 `YUQING_PUBLIC_BASE_URL`（邮箱验证链接基地址，防 Host 头伪造）
- 收费体系语义：每次分析 Create/Rerun 各扣 1 次额度，管线失败/取消自动回补；额度不足 HTTP 402 `NO_CREDITS`；新注册赠 1 次试用；beta 公测期额度不过期
- 分析模式：套餐裁剪 quick（Lite 3 维速览，尝试 thinking=disabled 压成本）/ full（5 维）；Go→Python 经 `InsightAnalyzeReq.Mode` 透传，Python 侧 400 时自动去掉 thinking 参数重试
- 支付回调路由 `/api/v1/callbacks/payment/:channel` 是唯一免鉴权业务端点 —— 安全完全依赖渠道验签，改动 payment 包时必须保持防线顺序：验签 → 金额核验 → pending→paid 原子跃迁（provider_txn_id 唯一）→ 幂等发放
- 引导管理员经 `yuqing-server.service.d/bootstrap-admin.conf` drop-in 注入，保证重建环境可复现
- **🔴 部署铁律（用户 2026-09-22 明令）：一切编译/构建只在本地完成后上传**（Go 交叉编译、前端 dist、RSSHub tarball 等）—— 服务器只做解压/配置/迁移/启停。生产服务器内存小，任何构建都可能打满内存打死 sshd（RSSHub tsc 构建实测打挂 1.7Gi 服务器）
- **唯一生产环境 = yuqing2.pangu-cloud.com**（101.96.209.90:22352，CentOS Stream 9 / 4C3.6Gi / oneinstack 源码 nginx / PG15 平台库 + 用户自有 MySQL 并存 / Redis 8.4 / Node 在 /usr/local/node/bin）。老机 47.120.20.10 的部署配置已废弃（用户 2026-09-22 确认丢弃，其上数据未迁移）
- **规范化部署手册 `docs/ops/DEPLOYMENT_RUNBOOK.md`**（新服务器/其他智能体照此执行，含全部踩坑）；运维手册 `docs/ops/OPS_MANUAL.html`；部署日志 `docs/ops/DEPLOYMENT_LOG.html`；CI/CD 规划 `docs/ops/CICD_PLAN.html`（文档归档：planning/user/ops/dev 四类）

## 凭据与安全

- **仓库公开**，历史上有过 Bocha key 泄露（`docs/DEPLOYMENT.html`，commit `00cf3d1` 之前）—— 已替换为占位符，但**该 key 需在 open.bochaai.com 轮换**
- 真实凭据只存在于 `credentials.local.md`（**已 gitignore，勿提交、勿读取进上下文再写出**）
- E2E spec 凭据只从 `E2E_EMAIL`/`E2E_PASSWORD` 读，**禁止**加硬编码兜底默认值
- 提交前自查：`git diff --cached` 里不得出现 `sk-`、密码、token

## Conventions

- IDs: ULID (`pkg/id`)
- Errors: `pkg/errors` sentinel + `Wrap()`，信封 `{code, message, details, request_id}`
- HTTP handlers: thin — DTO 校验 → service → `respondError`。v1 路由全部经 `v1.Services` 注入
- 套餐特性：`billing.DefaultPlans()[code]` + feature key，不硬编码
- TDD: 先写失败测试 RED → 最小实现 GREEN → 重构。**禁止先写实现再补测试**
- 测试规范升级（2026-09-23 漏测复盘后生效）：① **替身/真实路径必须对账**——凡有 fake/mock 降级路径的功能（Bocha/LLM/存储），每迭代用真实凭据跑最小对账清单（结果留档 reviews/），替身绿 ≠ 生产对；② **无验收标准不排测**——排测任务必须带"用户可感知的完成定义"，缺失时测试报告显式标注"按实现行为测试，不构成产品验收"；③ gap registry 条目带 registered_at，超一迭代未动自动升 severity，BLOCKER 项存在时"测试全绿"不构成发布口径
- Code review: 5 角度审查，报告留 `docs/dev/reviews/REVIEW_REPORT.md`（§5/§8 等章节记录已知遗留项）
- 前端: 页面数据经 `web/src/api/*.ts` 统一 axios（401 自动 refresh），错误信封经 ApiErrorHandler；图表色 负面 `#FF2442` / 中性 `#9ca3af` / 正面 `#02b940`
- 前端任务页带分阶段预估时长提示（实测 357s 校准：采集 1-2 分/五维分析 3-5 分/报告 1 分），RUNNING_HINTS 在 AnalysisDetailPage.tsx
- 项目品牌：**盘古舆情**（README/前端/demo/docs 均用此名；Go module 名 `yuqing` 保持内部标识不变）
- 面向人交付的文档用 **HTML**（`docs/*.html`），不用 Markdown —— 用户明确要求过
- 提交前把关：`make test` 全绿 + `npm run build` 通过 + `go vet` 无警告
