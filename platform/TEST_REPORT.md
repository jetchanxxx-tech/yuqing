# 平台测试报告 — AI 原生舆情监测平台

| 项 | 值 |
|---|---|
| 报告日期 | 2026-09-07 |
| 分支 | worktree-1-nginx-mysql-2-3-llm-llm-workbuddy-trae-valiant-fiddle |
| 范围 | 契约测试(API Contract) + 服务层集成测试 + 前端 E2E smoke(待装 Playwright) |
| 结论 | **GO — 全量 254 个用例通过，可合并**（契约缺口 13 项为已知 backlog，非阻塞 stub 阶段） |

---

## 1. 本轮交付

| 交付物 | 路径 | 内容 |
|---|---|---|
| API 契约测试 | `platform/internal/api/v1/contract_test.go` | 15 个测试函数 / 40 个用例：对 v1 handlers 响应结构与前端 `web/src/api/*.ts` 契约逐端点比对，产出缺口清单（见 §4） |
| 服务层集成测试 | `platform/test/integration/`（7 个文件） | 15 个测试函数 / 56 个用例：全链路、计费、套餐 gating、多租户隔离、告警、并发安全；全部使用真实服务 + 真实内存 store |
| E2E smoke | `web/e2e/demo-smoke.spec.ts` + `playwright.config.ts` | 4 条用例：登录页渲染 / 未认证跳转 / 登录表单抗 501 / demo 离线包可打开 |
| 测试报告 | `platform/TEST_REPORT.md` | 本文 |

### 1.1 新增测试统计

| 层 | 顶层测试函数 | 子测试 | 用例合计 |
|---|---|---|---|
| 契约测试（新增包内 0 → 77.0% 覆盖率） | 15 | 25 | 40 |
| 集成测试（新增包，真实服务链） | 15 | 41 | 56 |
| E2E smoke（待装依赖，未执行） | — | — | 4（spec） |
| **新增合计** | **30** | **66** | **96（≥ 25 验收阈值 ✅）** |

### 1.2 全量回归

```text
go test ./... -count=1   → 全部 PASS（254 个 RUN 用例，0 FAIL，exit 0）
go vet ./...             → 干净
gofmt -l（新增文件）      → 干净
go test -race            → 本机不可用（Windows 无 cgo），见 §7 风险 R1
```

---

## 2. 覆盖率（`go test -cover`，2026-09-07）

| 包 | 覆盖率 | 说明 |
|---|---|---|
| business/alert | 91.8% | 含本轮真实 sender 链路 |
| business/analysis | 89.5% | 状态机全路径 |
| business/dashboard | 94.3% | |
| business/report | 88.1% | 套餐 gating 全档位 |
| platform/auth | 83.2% | argon2 注册/登录 |
| platform/tenant | 98.3% | |
| platform/usage | 86.2% | |
| pkg/llm | 86.2% | MeteredProvider |
| pkg/queue | 89.6% | |
| pkg/errors | 84.6% | |
| **api/v1（新增契约测试后）** | **77.0%** | 此前为 0%（无任何测试） |
| api/middleware | 50.6% | 既有 |
| pkg/db | 22.7% | PG 依赖，内存下低覆盖（既有） |

注：默认 `-cover` 只统计被测包自身语句；跨包集成路径需 `-coverpkg=./internal/...` 聚合（CI 建议项）。

---

## 3. 集成测试覆盖的业务链路（真实服务，零业务 mock）

| 链路 | 用例 | 验证点 |
|---|---|---|
| 全链路 | `TestIntegration_analysisLifecycleFullChain` 等 | Register → Login → Create(queued) → 队列收到任务 → 5 次 Transition → completed；终态拒绝再流转；Rerun 重新入队；Dashboard Overview/Trend/Sources/Topics 反映数据 |
| 注册/登录 | `TestIntegration_registerLoginTenantFlow` | 租户+tenant_admin 角色+free 套餐配额；重复邮箱 ErrConflict；邮箱归一化；错误密码 ErrUnauthorized |
| 计费 | `TestIntegration_meteredProvider_hardCapChain` 等 | 1000 配额：600→ok → 1200→exceeded+Warning → 第三次 BudgetError 且不记账；overage/none 模式对照 |
| 套餐 gating | `TestIntegration_reportPlanGating` | free: html✅ pdf❌ docx❌；pro: markdown✅ pdf❌；business/enterprise: 全格式✅；未知租户按 free 限制；格式不匹配 ErrConflict；跨租户 ErrNotFound |
| 多租户隔离 | `TestIntegration_tenantIsolation` | List/Get/Cancel 全部按租户隔离；跨租户操作 ErrNotFound 且不污染他方状态；Dashboard 各看各的 |
| 告警 | `TestIntegration_alertChain` 等 | 0.7 阈值：0.85 触发→1 封邮件（收件人/主题断言）+ LastTriggeredAt 落库；0.3 静默；重复触发再发；多规则只触发匹配项；非法规则 5 例全拒 |
| 并发安全 | `TestIntegration_concurrent*` 3 例 | 50 goroutine×25 次 List/Get 无 panic 无脏读；8 写者+8 读者混跑；10 并发 Cancel 同一分析 → 恰好 1 成功 9 Conflict |

---

## 4. 契约缺口清单（供开发总监决策）

来源：`contract_test.go` 的 `contractGapRegistry` + 断言输出（`go test -v ./internal/api/v1/` 可直接复现）。
P0/P1 为影响真实用户主路径的项；stub 阶段不阻塞合并，但决定 **Phase 1 剩余开发顺序**。

| # | 严重度 | 端点 | 状态 | 缺口描述 |
|---|---|---|---|---|
| 1 | **BLOCKER** | POST /api/v1/auth/register | STUB 501 | 前端 `stores/auth.tsx` 依赖 `{access_token, refresh_token, user}`；服务层已实现(Register)，仅差 handler 接线 |
| 2 | **BLOCKER** | POST /api/v1/auth/login | STUB 501 | 同上，登录完全不可用 |
| 3 | **BLOCKER** | GET /api/v1/dashboard/{overview,trend,sources,topics,alerts} | STUB 501 | DashboardPage 4 图 + 卡片全部取这些接口 → 页面不可用；服务层已实现(Overview/Trend/Sources/Topics)，仅差接线 |
| 4 | **MAJOR** | POST /api/v1/auth/refresh、/auth/logout | STUB 501 | refresh 是 axios 401 自动重试的命脉（`api/client.ts`） |
| 5 | **MAJOR** | GET /api/v1/analyses/:id | STUB（硬编码 draft） | AnalysisDetailPage 轮询会拿到假状态；缺 name/progress/error_message |
| 6 | **MAJOR** | GET /api/v1/analyses/:id/result | 缺字段 | 缺 `topics`（AnalysisResult.topics 必需） |
| 7 | **MAJOR** | POST /api/v1/analyses/:id/rerun、GET /:id/events | STUB 501 | Rerun 服务层已实现(Rerun)未接线；events(SSE)契约未定义 |
| 8 | **MAJOR** | GET /api/v1/billing/plans | 字段不匹配 | stub 返回 `{price, quota:string}`；前端需要 `price_monthly_cny`/`token_quota_m`（后端 `billing.Plan` JSON 恰好已匹配前端，建议直接接真实数据） |
| 9 | **MAJOR** | GET /api/v1/auth/me | 缺失 | principal 仅信任 localStorage；无接口可在刷新后重新校验会话 |
| 10 | **SECURITY** | GET/POST /api/v1/admin/* | 无 RBAC | admin 组未挂 RequirePermission/RequireRole；viewer token 可达 handler（现被 501 掩盖）。接入真实实现时必须先加 `platform_admin` 守卫（契约测试已留回归位） |
| 11 | MINOR | POST /api/v1/analyses | 缺字段 | 创建响应缺 `progress`（AnalysisSummary 需要） |
| 12 | MINOR | GET /api/v1/reports/:id、/:id/download | 假数据 | 任意 id 返回罐头行，无 404 |
| 13 | MINOR | GET /api/v1/billing/invoices/:id/download | STUB 501 | 发票下载入口失效 |

**已通过（与前端契约一致）**：`GET /health`、`GET /analyses`（analyses/total/tenant_id）、`GET /analyses/:id/result` 主干字段（documents/sentiments）、`GET /billing/usage`、`GET /billing/invoices`、`GET /reports` + `/templates`、错误信封 `{code,message}`、401 `UNAUTHORIZED`、403 `FORBIDDEN`（RBAC 中间件链路：viewer 建分析被拒、viewer 可列表）。

---

## 5. 失败项

无。`go test ./... -count=1` 全绿（254/254）。

---

## 6. E2E 状态（C 部分）

- `web/e2e/demo-smoke.spec.ts`（4 用例）与 `playwright.config.ts` 已就位；
- **@playwright/test 未安装**（`web/package.json` 无此依赖，node_modules 亦无）。按规范只写 spec 不执行；
- 安装并运行：
  ```bash
  cd web
  npm i -D @playwright/test
  npx playwright install chromium
  npx playwright test --config e2e/playwright.config.ts   # 自动拉起 vite dev server(:5173)
  ```
- E2E 只测页面渲染/路由守卫/demo 包，不测业务流 —— 等 v1 handlers 落地（§4 缺口修复）后补业务用例。

---

## 7. 风险项

| # | 风险 | 等级 | 说明 / 缓解 |
|---|---|---|---|
| R1 | `-race` 未能本地执行 | 中 | Windows 无 cgo（go: -race requires cgo）。并发 3 例已通过普通运行；**CI(Linux) 必须补 `go test -race ./test/integration/`** |
| R2 | admin 端点无 RBAC 守卫 | **高** | 契约缺口 #10。当前靠 501 挡住；一旦接线真实 handler 而忘加守卫即越权。契约测试 `TestContract_adminGroup_missingRBACGuard` 会在补上 403 前持续记录 |
| R3 | Dashboard/登录/注册 handler 全部 stub，前端主路径不可演示 | 高 | 服务层已 100% 就绪（本轮集成测试证明），属于接线工作量而非设计风险 |
| R4 | demo 包文件名漂移 | 低 | 工作树中 `demo/index.html` 已被删除、新文件为 `demo/index1.html`（git 未跟踪）；demo/README.md 仍指向 index.html。E2E 与发布打包需统一命名（建议定稿后改回 index.html 或同步 README） |
| R5 | 集成测试硬编码队列 topic `"analysis.tasks"` | 低 | topic 常量未导出，测试复用了字面量；若改名需同步（已注释出处 analysis/service.go） |
| R6 | 注册 argon2 成本 | 低 | 集成测试含真实密码哈希，`test/integration` 全量约 3.7s，可接受 |
| R7 | 覆盖率统计口径 | 低 | 默认 cover 不聚合跨包集成路径（见 §2 注），建议 CI 用 `-coverpkg` |

---

## 8. 命令速查

```bash
# 平台全量（含本轮新增 96 个用例）
cd platform && go test ./... -count=1

# 只看契约缺口输出（缺口清单 + 401/403/RBAC 断言）
go test ./internal/api/v1/ -count=1 -v

# 集成测试（真实服务链路 + 并发）
go test ./test/integration/ -count=1 -v

# E2E（先装 @playwright/test）
cd web && npx playwright test --config e2e/playwright.config.ts
```

## 9. 建议的下一步（按 ROI）

1. 接线 auth register/login（缺口 #1/#2）+ dashboard（#3）→ 解锁前端主路径，E2E 业务用例可补；
2. 实现 admin RBAC 守卫（#10）→ 消除唯一安全缺口；
3. CI 补 `-race` + `-coverpkg` 聚合；
4. demo 包命名定稿（R4）。
