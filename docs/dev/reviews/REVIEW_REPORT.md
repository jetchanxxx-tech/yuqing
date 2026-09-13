# 开发总监最终代码审核报告（Round: 服务层 + 12 页面 + 契约测试）

- 日期：2026-09-07
- 范围：platform/ 后端新增 13 文件 + analysis 重构、web/ 前端（只读验收，未改动）、契约/集成测试
- 审核方式：5 角度（逐行 / Go 陷阱 / 跨文件 / Wrapper / 约定）+ 契约缺口 13 项逐一决策 + 修复后回归验证

## 一、总体结论

服务层代码质量良好：所有内存 store 均为 **RWMutex + 返回拷贝** 的读写模式，状态机迁移在写锁内完成，无 map 并发写、无 goroutine 泄漏；错误处理普遍走 sentinel + Wrap 链。**主要问题不在新服务本身，而在 v1 HTTP 层完全未接线**（13 项缺口中的 3 个 BLOCKER 根因相同：services 已实现且测试充分，handler 仍是 501 stub）。本轮已完成接线与守卫修复，`go test ./...`、`go vet ./...`、`npx tsc -b` 全部通过。

## 二、契约缺口 13 项决策清单

| # | 缺口 | 严重度 | 决策 | 结果 |
|---|------|--------|------|------|
| 1 | auth register/login 501 | BLOCKER | **修复**：接线 auth.Service 真实流程，HTTP 语义 201/200/400/401/409 | ✅ 已修 |
| 2 | dashboard 5 端点全 501 | BLOCKER | **修复**：接线 dashboard.Service + alert.Service.List（alerts） | ✅ 已修 |
| 3 | 缺 GET /auth/me | BLOCKER | **修复**：新增端点（AuthRequired 后返回 principal userDTO） | ✅ 已修 |
| 4 | auth refresh/logout 501 | MAJOR | **修复**：refresh 接线（Refresh 校验 + 重发对）；logout 为 stateless no-op 204（吊销机制见遗留 8.3） | ✅ 已修 |
| 5 | analyses/:id 硬编码 draft | MAJOR | **修复**：接线真实 store（Get/List/Create/Cancel/Rerun），未知 id 404，跨租户不泄漏 | ✅ 已修 |
| 6 | result 缺 topics 字段 | MAJOR | **部分修复**：响应加入 `topics: []`（结构对齐前端 AnalysisResult 避免渲染崩溃）；真实文档/情感/topic 聚合依赖 documents store，记录遗留 | 🔶 半修 |
| 7 | rerun/events 501 | MAJOR | **部分修复**：rerun 接线完成；events（SSE）契约未定义，保持 JSON 信封 501 并记录 | 🔶 半修 |
| 8 | billing/plans 字段名不匹配 | MAJOR | **修复**：改用 billing.DefaultPlans 真实数据（`price_monthly_cny`/`token_quota_m` 字段天然对齐前端） | ✅ 已修 |
| 9 | admin 缺 /admin/plans 等 | MAJOR | **修复**：tenants 列表/suspend/resume + GET plans 接线（共享租户 store）；POST plans 与 usage 保持 501（无 plan 写 store / 无用量汇总），挂 RBAC 守卫后记录 | ✅ 已修 |
| 10 | /admin/* 无 RBAC 守卫 | SECURITY | **修复**：全组逐路由 RequirePermission（tenants:list / tenants:suspend / plans:manage / billing:manage），viewer 一律 403 | ✅ 已修 |
| 11 | create 响应缺 progress | MINOR | **修复**：接线后返回完整 AnalysisResult（含 progress） | ✅ 已修 |
| 12 | reports 假数据无 404 | MINOR | **修复**：接线真实 report store，list/detail/download 未知 id → 404 | ✅ 已修 |
| 13 | 发票下载 501 | MINOR | 记录遗留：invoice 文件存储未实现（见 8.6） | 📋 遗留 |

## 三、代码审查发现（按严重度）

### 🔴 已修复（审查中发现并直接修复）

1. **meteredProvider 成本计算错误**（`internal/pkg/llm/provider.go`，原 Phase 2 遗留）
   - `costIn/costOut` 均用 `总token × 输入单价`，`OutputCostPerM` 与 `UserOutputPricePerM` 从未被使用 → 平台成本与计费金额系统性算错（输出 token 按输入价计）。
   - 修复：新增 `microCNY()`，prompt 按输入价、completion 按输出价分别计价；新增回归测试 `TestMeteredProvider_costsPriceByTokenClass`。

2. **dashboard Trend `scores` 为 null**（`internal/business/dashboard/service.go`）
   - nil slice 序列化为 `"scores": null`，前端 `data: trendQ.data.scores` 直接喂 ECharts，null 会致图表异常。
   - 修复：scores 始终输出与 dates 对齐的零值数组。

3. **租户数据双存储不一致**（`internal/app/container.go` + `internal/platform/auth/store_shared.go`）
   - auth.Register 写的租户行与 tenant.Service（admin 列表/suspend）读的 store 是**两个独立内存实例** → HTTP 上注册的租户在 admin 列表永远看不到。
   - 修复：引入 `auth.SharedTenantStore`，users/members 留在 auth 存储、tenants 行共享同一 `tenant.MemoryStore`（内存模式下等价于 PG 单张 platform tenants 表）。契约测试 `TestContract_admin_tenantLifecycle` 端到端验证 register → admin list → suspend → resume。

### 🟡 遗留（记录在案，见第八节）

4. **分层违规（字面）**：`business/report` import `platform/billing`（Plan 类型 + DefaultPlans）——违反 CLAUDE.md "business 不 import platform"。缓解：plan 解析已通过函数注入解耦，实际耦合仅是类型引用。建议将定价目录下沉 `pkg/`。
5. **auth.Service 自建 usage.Meter**：Register 设的免费额度写进服务私有 meter，与将来 MeteredProvider 用的 meter 非同一实例（内存模式无碍，Redis 模式必须经 container 共享同一 meter）。
6. **Refresh 不区分 token 类型**：refresh token 与 access token 同签名、无 type claim，access token 也可换新对（见遗留 8.3）。

### 💭 NIT（不阻塞）

- `analysis.AnalysisResult` 用 `time.Time` + `omitempty`（对 struct 无效）→ 未启动任务 JSON 会带 `"started_at":"0001-01-01T00:00:00Z"`。建议改 `*time.Time`。
- middleware 中 `WithPrincipal/PrincipalFromContext`（context key）与 gin `CtxPrincipal` 是两套键，目前只有 HTTP 路径在用，另一套为死代码。
- alert.Check 每次超阈值都发信（无冷却窗口），且 send 失败时 trigger 时间已更新。
- analyses 列表无分页参数（MVP 可接受）。

## 四、已修复项汇总（本次提交）

| 文件 | 修复内容 |
|------|----------|
| `internal/api/v1/auth.go` | register/login/refresh 真实接线 + `/me` `/logout`（RegisterSessionRoutes）+ userDTO/authResponse 契约 + 400 前置校验 |
| `internal/api/v1/dashboard.go` | 5 端点接线 dashboard.Service / alert.Service |
| `internal/api/v1/analyses.go` | create/list/get/cancel/rerun 接线真实状态机；result 补 `topics:[]`；404/409 语义；events 保留 501 信封 |
| `internal/api/v1/reports.go` | list/get/download 接线真实 store（404 语义 + plan gating）；templates 保留静态目录 |
| `internal/api/v1/admin.go` | 全组 RBAC 守卫；tenants 列表/suspend/resume + GET plans 接线；usage/POST plans 501 信封 |
| `internal/api/v1/billing.go` | /plans 用真实 DefaultPlans；下载 501 信封规范化 |
| `internal/api/v1/services.go` | `Services` 聚合类型 + 统一信封 helper（request_id） |
| `internal/api/router.go` | 注入 deps；/auth 鉴权子组挂 me/logout |
| `internal/app/container.go` | 新增组合根：共享 tenant store、全服务装配 |
| `internal/platform/auth/store_shared.go` | SharedTenantStore（租户行共享） |
| `internal/business/dashboard/service.go` | Trend scores 零值对齐数组 |
| `internal/pkg/llm/provider.go` (+test) | 成本按 token 类别分档计价 + 回归测试 |
| `cmd/server/main.go` | app.Build 装配后建路由 |
| `internal/api/v1/contract_test.go` | 契约测试从"锁 501"改写为"锁真实行为"（RED → GREEN） |

## 五、修复方式（TDD 说明）

契约测试先行改写（新行为断言：201 注册 + token pair + user、409 重复、401 错误凭据、/auth/me、dashboard 200 结构、admin viewer→403、analyses 全生命周期、plans 新字段、reports 404），首轮运行得到 RED（trend scores 为 null、stub 行为不符），随后完成 handler 接线与两个 service 修复，重跑转 GREEN。

## 六、分层验证结论

- business → platform 违规：**仅 1 处**（`business/report → platform/billing`），已通过函数注入缓解，见遗留 8.4。其余 business 包只依赖 business/pkg。
- platform → business：**0 处**（auth 仅引 platform 内部 + pkg/llm）。
- engine：contract + Fake 无业务依赖；api/v1 引 platform/business 属允许方向；app 为组合根。
- ID 全部走 `pkg/id`(ULID)；错误链全部 sentinel + Wrap；无裸 panic。

## 七、回归验证

```
go test ./... -count=1   → 19 个包全部 ok，0 FAIL（含 40+ 契约用例、56 集成用例、各服务单测）
go vet ./...             → 干净
cd web && npx tsc -b     → 通过（exit 0）
```

## 八、遗留项清单

| 项 | 说明 | 建议排期 |
|----|------|----------|
| 8.1 result/documents/sentiments/topics 真实聚合 | 返回空数组，真实数据依赖 documents store + 引擎管线（F03/F04） | P1 引擎实际实现时 |
| 8.2 GET /analyses/:id/events（SSE） | 契约未定义；前端用轮询，不阻塞 | 需要时再定义契约 |
| 8.3 token 吊销/类型区分 | refresh 与 access 同签名；logout 无服务端状态；suspend 后旧 token 在 TTL 内仍有效 | Redis 会话层落地时（建议 refresh 加 type claim + jti denylist） |
| 8.4 business/report → platform/billing 引用 | 建议把定价目录下沉 `pkg/`，business 侧只留函数注入签名 | 架构评审期 |
| 8.5 auth/usage Meter 实例 | 各服务自建 Meter，须在 container 统一共享（Redis 计数落地时） | Redis 接线时 |
| 8.6 /admin/usage、POST /admin/plans、发票下载 | 依赖 usage rollup consumer、plan 写 store、invoice 文件存储 | P1 计费深化 |
| 8.7 /billing/subscription、/usage 占位 | 真实值依赖 subscription store + 共享 meter | P1 |
| 8.8 reports/:id/download 只回 URL | 文件字节依赖 storage driver | storage driver 落地时 |
| 8.9 服务内 Create/Rerun"先落库后 Publish" | Publish 失败会留下无队列消息的 queued 任务（内存 driver 几乎不会失败） | 队列驱动切换时处理回滚/补偿 |
| 8.10 Register 非事务 | 极低概率产生孤儿 user（PG 版须事务化） | PG 落地时 |

## 九、前端验收说明

按任务约束未改动 `web/src/pages/*` 与 `web/src/api/*`；handler 响应形状（含 userDTO、AnalysisSummary、DashboardOverview、Plan、report 下载 URL 拼接规则）均按前端现有契约逐字段对齐，`tsc -b` 通过。
