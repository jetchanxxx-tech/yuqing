# beta 0.2.3 双层模板能力实施计划

**状态**：部分开发和本机验证（方案草稿、过滤、时间线）；未完成整体验收/未部署。下述任务与门槛仍是完整目标，不代表已逐项交付。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 六种监测模板映射既有四类分析视角，并让用户确认的七项配置真实驱动采集、周期执行、风险标签、预警和报告；不破坏旧分析任务。

**Architecture:** Go 平台库的方案服务提供版本化模板和保存快照；平台数据库驱动周期调度、幂等运行、额度流水与持久 outbox；Python 查询/洞察链路和报告引擎消费实际配置，React 双入口避免重复选择。原 `POST /analyses`、状态机和租户隔离保持不变。

**Tech Stack:** Go + Gin + pgx + goose；Python FastAPI；React/TypeScript；PostgreSQL；Go/pytest/Playwright 合约与端到端测试。

**Spec:** [PRD](./PRD.md) · [技术设计](./TECHNICAL_DESIGN.md)

## Global Constraints

- **用户决策**：两层都要兑现；不是只做预设 UI，七项配置需实际生效且有可见结果。
- `analysis_type` 继续使用 `event|brand|competitor|industry`；`template_id` 与之独立，保留旧任务和手动入口。
- 方案提交由已认证 principal 确定 `tenant_id`；未知套餐/不支持来源 fail closed；所有列表返回数组，不返回 null。
- 仅用户明确同意才能开启周期运行、额度消耗和通知；非启用态/不支持状态不得写成“已生效”。
- 创建、重复投递、失败/取消退款维持原状态机与可审计的幂等财务流水，数据源失败与洞察/报告非致命失败区别处理。
- 外部真实提供者不能用 mock 冒充，发送失败显示失败，来源缺口明确提示；**只写文档阶段不代表已实现**。
- 生产变更按当前 schema 与 runbook 核对；迁移先在 disposable PostgreSQL 测试，本地构建含新迁移的 CLI，发布时先迁移后启服务。

## 文件责任边界与依赖

| 工作包 | 文件或模块（计划，不等于已经存在） | 输入 → 输出 |
|---|---|---|
| 目录与存储 | `platform/internal/business/monitorplan/`、`platform/migrations/platform/` 下新编号迁移；`platform/internal/api/v1/monitor_plans.go` | 用户表单 → 版本化方案草稿/快照 |
| 分析实例 | `platform/internal/business/analysis/{service,pipeline,store_pg}.go`、`platform/internal/app/{container,pipeline}.go` | 方案 revision → 单次独立 analysis，字段快照 + 幂等键 |
| 采集与洞察 | `platform/internal/engine/real.go`、`engines/query_engine/main.py`、`engines/common/scraper.py`、`engines/insight_engine/main.py` | 排除词/时间窗/来源/标签规则 → 真实原文 + 标签证据 |
| 调度与投递 | `platform/internal/business/monitorplan/scheduler*.go`、平台 PG store、`platform/cmd/worker/main.go` 或明确选定的进程 | schedule → 原子领取、outbox 重放、每次执行记录 |
| 通知与报告 | `platform/internal/business/alert/`、`platform/internal/business/report/`、`engines/report_engine/main.py` | 触发器与模板 ID → 实际通知记录和对应报告 |
| 前端 | `web/src/pages/AnalysisNewPage.tsx`、`web/src/api/{analyses,monitorPlans}.ts`、方案页与详情页 | 模板入口、可编辑预览、状态/用量/结果展示 |

上述路径是落地候选；实施前先核对当前工作树与原有接口，避免重写现有逻辑或改动无关模块。旧分析数据无需重算、不批量回填新方案；历史空 `analysis_type` 继续通用提示词。

## Task 0：锁定契约与真实依赖（发布前置）

**Files:** 更新 `docs/planning/v0.2.3/{PRD,TECHNICAL_DESIGN}.md` 的开放问题；以当前 `platform/internal/app/container.go`、`platform/cmd/worker/main.go`、`alert.Service`、`query_engine` 及报告实际部署为证据。

**Interfaces:** 确认六模板必填字段、手动模式行为；每次运行扣费规则与余额不足策略、周期最小频率/时区、用户选择通知通道及试用/套餐可用项。按产品定义“周期”= 有用户确认的真实调度，不是日历标签。

- [ ] 逐项演示当前单次采集、分析、报告、告警和独立进程队列，记录“支持/仅 API/未接线/真实源不可用”。
- [ ] 核实 PostgreSQL 当前平台迁移版本、tenant 模板用途、临时 PG 契约测试地址、独立服务与外部通道凭据，禁止拿生产库做测试。
- [ ] 逐项明确：模板哪些输入必填、频率/通知渠道、计划上限、每轮费用展示和错误反馈；签字后锁定字段，未签字不做“自动默认开启”。
- [ ] 列出实际采集源的查询参数/排除词/日期过滤能力；不能完全下推时定义应用层过滤与覆盖缺口，不宣称全平台能力。

**Gate:** 得到可执行的来源能力矩阵和签字的产品契约；若缺实际邮件通道/耐久队列方案，不承诺上线持续监测。

## Task 1：四类视角兼容 + 六模板目录（可独立评审）

**Files:** Create `platform/internal/business/monitorplan/{catalog,models,service}_test.go` 及对应 `.go`；Modify `web/src/lib/constants.ts`、`web/src/pages/AnalysisNewPage.tsx`；Test 既有分析 DTO/列表/insight 链路。

**Interfaces:** `template_id`（六个稳定 ID）→ 默认 `analysis_type`：brand_daily→brand、product_launch→event、quality_complaint→brand、competitor_update→competitor、crisis→event、campaign_review→event。`industry` 保留手动。用户可改默认视角；template ID 不随视角变化。

- [ ] 先为六映射、历史空类型、手动四类型及用户修改视角写失败的 Go/前端测试并运行确认红灯。
- [ ] 最小实现版本化目录与类型校验、来源解释；不创建额外六种状态或复制分析管线，运行测试转绿。
- [ ] 前端“按模板/手动”双入口，模板流程只选场景并显示可修改视角；手动入口保留四选一（可选）。
- [ ] 在 `analysis_type` 透传 Go→insight 的测试与 API 列表标签测试中验证旧任务值；无模板的旧请求不被拒绝。

**Gate:** 四类型含 industry 在旧页面仍能创建并正确展示；六模板不引入重复类型选择，且仅提示词依据视角变化，不改变 queued→completed/failed/canceled 状态转移。

## Task 2：方案预览、存储、来源权限与真实生效状态

**Files:** Create `monitorplan/store_pg.go`、`monitorplan/store_pg_test.go`、`platform/internal/api/v1/monitor_plans.go`、新编号 goose migration；Modify `platform/internal/app/container.go`、`platform/internal/api/v1/services.go` 和路由；Create `web/src/api/monitorPlans.ts`。

**Interfaces:** `preview` 不写库、不扣费、不排队；保存 `tenant_id,owner_id,template_id,template_version,analysis_type,inputs_json,config_json,revision,state`。配置分 `draft|configured|enabled|failed`，按具体字段给状态及失败原因；revision 冲突返回标准错误信封。

- [ ] 测试先行：六模板生成七项候选；来源运行时可用性与套餐 feature keys 联合判断；禁止覆盖用户编辑及跨租户读取。
- [ ] 针对空字段、来源健康不佳、未接通执行器写失败用例；预览返回 `proposed/unavailable`，不能返回“已启用”。
- [ ] 建库和 CRUD；新 store 方法使用独立 disposable PostgreSQL 的合约测试，确认并发 revision、键权限与新表索引。
- [ ] 接 `GET /monitor-plans/templates`、`POST /monitor-plans/preview`、`POST /monitor-plans`、`GET/PATCH /monitor-plans/:id`，核对 401/403/404/冲突、数组为 `[]`、分页与请求 ID。

**Gate:** 编辑保存可重复打开、保存不扣费；未授权来源无法生效。未完成后续执行链路的字段明确标“候选”，Task 2 不算整个 beta 完工。

## Task 3：运行快照、幂等、额度与跨进程可靠投递（关键路径）

**Files:** Modify `platform/internal/business/analysis/{service,store_pg,pipeline}.go`；Create 平台 PG 幂等/周期执行/outbox 新迁移与 store；Modify `platform/internal/app/container.go` 和 `platform/cmd/worker/main.go`；Test `analysis` 和 `monitorplan` PG 合约。

**Interfaces:** 每次运行绑定 `plan_id,plan_revision,template_id,analysis_type,config_snapshot`；唯一键 `(tenant_id,plan_id,scheduled_slot)` + 手动执行 `idempotency_key`（同 key 不同 body 应报冲突）；余额不足不排队不扣费。扣费、任务创建、待投递消息在同一可恢复事务/确定的补偿模型；outbox 后由独立进程领取与 ack/重试，重复投递按 analysis_id 幂等。

- [ ] 写同 key 双并发只建一任务/只扣一次、错误重试不重复扣费、断电/重启后消息可重放的红灯 PG 测试。
- [ ] 审核现有 `credit.Service` 的跨服务事务边界；若不能共享同一 PG tx，设计可恢复的 reservation/补偿状态并用测试证明不丢消息、不多扣、不多退。
- [ ] 最小实现新的方案运行事务与 outbox 消费。不得假设当前 Redis/RabbitMQ driver 已生效：worker 存在内存回退，必须在独立 server + worker 进程复现持久重试。
- [ ] 单次失败/取消只退款一次，重复投递只处理一次，终态任务不发生后续状态变化；保留现有直接 `POST /analyses` 的兼容测试。

**Gate:** 额度和状态机 PG 合约测试通过，断进程/重投恢复可验证；未通过前不提供真实“运行方案”按钮。

## Task 4：关键词、排除词、数据源与监测窗口实际贯通

**Files:** Modify `analysis.CreateAnalysisRequest`/持久快照/`Fetcher.FetchRequest`、`platform/internal/app/pipeline.go`、`platform/internal/engine/real.go`、`engines/query_engine/main.py`/`engines/common/scraper.py`；Test 各层契约及 `engines/tests/`。

**Interfaces:** `keywords`, `exclude_words`, `sources`, `date_from/date_to` 全链路保持一致。query_engine 已有 `exclude_words` 字段，但 Go 传输尚未完整透传；仅在数据源真有相应能力时称“平台下推”，否则原文级过滤并记录比例/漏检风险；过滤发生在入库、标签、报告前。

- [ ] 写 Go→HTTP→Python 表单字段的红灯测试：排除词生效、时间范围边界、来源不匹配被拒绝、老请求默认不改变。
- [ ] 做参数持久化与转换、Go 引擎请求透传、Python 过滤/返回结果统计；对原文 `published_at` 缺失时明确标注无法精确筛选，不能把抓取时间当发表时间。
- [ ] 对真实许可来源做至少一轮对照抓取，核对来源标签、更新频率、覆盖失败说明；任何未授权/失效源 fail closed。

**Gate:** 七配置中的关键词、排除词、来源和窗口对最终真实原文可验证生效；不能只在方案 JSON 中存在。

## Task 5：风险标签命中与规则评估

**Files:** Create `monitorplan/tagger.go`、`tagger_test.go`；Modify `analysis` 洞察/文档访问层、报告/告警服务契约与相应 PG 存储。

**Interfaces:** 版本化标签规则以可追溯文档 ID 为证据产生命中事件；标签 ID、规则版本、analysis_id、来源文档、空样本/不确定性可见。默认规则只按有依据的内容匹配，模型建议不能当原文事实；预警规则按“有数据且满足条件”评估，不因无数据误报。

- [ ] 写标签命中、同名词歧义、零文档、重复文档、权限隔离的红灯测试。
- [ ] 实施确定性匹配/可编辑规则并保留证据与“未命中”；前端可从标签回看真实原文。
- [ ] 测试已确认预警规则按该标签过滤，标签未配置/不生效不能默认为全命中。

**Gate:** 风险标签同时影响可见分析结果、预警条件和报告内容，而不是只保存标签名。

## Task 6：可恢复的周期调度与用量显示

**Files:** Create `platform/internal/business/monitorplan/{scheduler,store_pg}_test.go` 与对应实现；Modify `platform/cmd/worker/main.go`、`web/src/api/monitorPlans.ts` 及方案页。

**Interfaces:** 单次或周期由用户确认；保存时区与 `next_run_at`、频率、启停、每次执行唯一时槽、上次执行状态。调度器从 PG 抢占到期时槽，失败有有界重试/告警，防多副本重复调度；生成本轮 UTC 日期窗口及不可修改快照；暂停后不再产生新任务，运行中的任务保留原状态机，不暗中取消。

- [ ] 先写并发 worker、重启恢复、跨时区/DST、余额不足、方案修改、暂停与缺失来源的红灯测试。
- [ ] 实现 PG 原子领取和 outbox 接 Task 3，按时槽创建本轮分析并记录实际扣费/余额不足；同一时槽补偿运行不会重复扣费。
- [ ] 前端展示下一次执行、每次费用/额度影响、最近运行与失败原因；只有用户显式确认才能开启。

**Gate:** 在独立 server/worker 和重启场景能持续执行、不会静默停止或重复收费；日报/预警不以“提交方案”冒充已运行。

## Task 7：真实预警通道与送达审计

**Files:** Modify `platform/internal/business/alert/{service,store_pg}.go`、`platform/internal/app/container.go`、相关配置/迁移；Create 预警投递记录 store/测试。

**Interfaces:** 当前 `alert.NewService(alertStore,nil)` 会丢弃邮件；必须注入真正的 EmailSender 或产品批准的其他真实通道。预警评估读取方案所确认的条件、标签和分析结果；通过 `(tenant_id,plan_id,rule_id,run_id)` 防重复告警。记录 `pending|sent|failed`、可诊断错误、重试与静默期。无配置/失败时显示未启用或失败，不显示已发送。

- [ ] 对负面比例/标签规则、未配置收件人、重复事件、发送失败、跨租户读取和终态任务写红灯测试。
- [ ] 在实际发送通道上实现可验证的发送状态与限流，机密信息从安全配置读取，日志不暴露地址/令牌原文。
- [ ] 用受控测试邮箱/沙箱完成真实发送及回执/日志验证；mock send 只算单元测试。

**Gate:** 告警可以实发且可审计；发送失败与引擎降级不会伪装成功，也不会触发风暴重复送达。

## Task 8：实际报告模板选择、输出与自动简报

**Files:** Modify `platform/internal/business/report/`、`platform/internal/app/pipeline.go`、`platform/internal/engine/report.go`、`engines/report_engine/main.py`、`platform/internal/api/v1/reports.go` 及相应测试。

**Interfaces:** `report_template_id` 映射现有 `daily|weekly|event` 或新增经产品批准的模板；选择影响实际生成的段落/渲染样式并写入报告记录/分析快照。`GET /reports/templates` 静态列表只是目录，不构成已执行功能。周期简报须绑定可用数据、发送者确认的时区与订阅偏好；报告失败保留来源和有效洞察并警告，不能空白/伪造。

- [ ] 写三个模板实际输出差异/不支持模板报错/缺失数据警告的红灯 Go+Python 测试。
- [ ] 透传 template_id 并让模板控制真实引擎渲染和导出；保持原无 template_id 的报告调用兼容。
- [ ] 如用户确认周期报告，再将计划调度生成的分析与简报投递绑定；重复调度/引擎失败不得发两次或误称已交付。
- [ ] 校验关键论断引用真实来源文档且列表不为 null；实际导出类型只标记通过实测的格式。

**Gate:** 选择不同模板能在真实报告中观察差异，生成失败有告警；周期简报按用户选项运行而非静态目录。

## Task 9：用户体验、真实验收、上线发布

**Files:** Modify `web/src/pages/AnalysisNewPage.tsx`、方案管理与详情页面、`web/src/api/monitorPlans.ts`、`web/e2e/`、用户文档及 `docs/planning/v0.2.3/`；发布脚本/配置按实际环境核对。

- [ ] 先写 UI/E2E：六模板的输入、编辑、类型覆盖、来源不可用、保存、单次与周期确认、费用提示、预警发送状态、报告样式及原手动入口不变。
- [ ] 分别跑 Go 单测与 PG 合约测试（只用 disposable PG）、Python 测试、web build/lint、Playwright；Go/Python 跨进程和真实来源/测试收件箱另列证据，缺项明确为 blocked/unverified。
- [ ] 本地构建 server/worker/CLI 和前端；迁移先在测试库验证，生产前核对 live schema/备份与当前 systemd 路径。生产先迁移，再灰度启用模板/调度/通知；不得在生产服务器编译。
- [ ] 灰度监测告警送达、调度积压、模板失败、报告可用率和费用对账；给出按模块关闭调度/通知且不丢现有任务的回退方案。

## Task 10：事件时间线与传播链路（同版本独立工作包）

**Files:** Create `platform/internal/business/eventtimeline/`（纯证据模型、store、计算、测试）、`platform/internal/api/v1/event_timeline.go`、相关平台 goose 迁移；Modify `analysis.Document`、`documents_pg.go`、`platform/internal/engine/real.go`、`engines/query_engine/main.py`/`engines/common/scraper.py` 与 `web/src/pages/AnalysisDetailPage.tsx`。

**Interfaces:** 事件先绑定同一 `tenant_id + analysis_id`。事实节点只用可核验原文与发布时间；观点分化等推断节点带证据/不确定性。关系边必须有平台引用/转发/回复 ID 或可核验引用，不靠同内容/时间先后推断；无证据时 `edges: []` 且告知覆盖缺口。`analysis_type=event` 不是自动补造节点的开关。

- [ ] 写真实来源字段审计：发表时间、作者、原始帖 ID、关系 ID、URL；确认哪些来源只够做时间线、哪些可以提供关系证据。不可用源不得编造传播网。
- [ ] 先写 PG/Go 红灯测试：去重、缺发布时间、同时间并列、首次出现仅采集范围、无边空态、跨租户证据不可见、论坛引用反查；再实现最小数据模型与构建器。
- [ ] `GET /api/v1/analyses/:id/timeline` 带权限/分页和空数组契约；前端事实/推断分离、证据回溯、缺数据与缺关系分别展示。
- [ ] 存在可核验来源关系时生成边；没有时不得渲染假关系；品牌回应仅有验证的官方来源才上时间线，回应前后变化只陈述对照不推断因果。
- [ ] Forum 仅消费带 tenant/analysis/doc ID 的证据包；错误/超时保留可用原文与时间线并写 warning；带真实许可数据跑采集→Go→Forum→UI 联合验证。

**Release gate:** beta 0.2.3 至少展示可追溯的真实事件时间线和明确的数据覆盖；传播链路仅对有真实关系证据的来源开放。若不能拿到任何可靠传播证据，必须展示关系不可用并明确对外不能宣称“完整传播链路已交付”。模板工作完成不能替代本工作包验收。

## beta 0.2.3 整体验收门槛

**Release gate:** 六个模板各跑完真实采集→分析→标签→规则→告警→指定报告；周期调度至少经历重启和多副本/重投演练；旧四类分析与账单不回退，租户隔离测试通过，且同版本事件时间线通过 Task 10 的证据门槛。未达成时只称“部分开发”，**不得宣称 beta 0.2.3 双层已兑现**。
