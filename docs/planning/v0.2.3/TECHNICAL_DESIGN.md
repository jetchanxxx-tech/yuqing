# beta 0.2.3 技术方向与技术设计（评审稿）

**日期**：2026-09-28<br>
**状态**：技术方案待评审；仅规划，未编码、未测试、未部署<br>
**依据**：[beta 0.2.3 PRD](./PRD.md)<br>
**范围**：监测方案模板化；事件时间线与传播链路。

## 1. 研发结论

选择 **Go 平台层编排和持久化 + 现有 Python 引擎采集/研判 + React 展示**，不新增专用微服务。分阶段交付：先完成六类模板的“生成草稿—编辑确认—保存—按现有分析任务执行”，再上线“有证据的事件节点/时间线”，最后在真实来源具备引用或转发元数据时打开传播链路。不能把时间先后或同文去重误当传播因果；缺少证据的阶段保持缺失。

**已确认的交付门槛**：模板与四类分析视角兼容，且七项配置必须有真实执行链路。用户明确选择周期执行时，方案可持久调度并按周期生成独立分析任务；明确启用规则/报告时，须实际评估、送达并用选定模板生成报告。不能仅保存草稿或使用虚假发信器就称为交付。未获有效外部数据时不得以模拟结果冒充。P0 时间线不以可构建完整传播图为上线前提，需在 UI 清楚标示“传播关系不可用”。

## 2. 当前实现与关键缺口（代码核对，不代表生产环境已验证）

| 领域 | 仓库可见实现 | beta 0.2.3 缺口 |
|---|---|---|
| 分析任务 | `analysis.CreateAnalysisRequest` 有 `name/analysis_type/keywords/sources/date_from/date_to`，`Service.Create` 扣报告额度、保存 `queued` 任务再发布队列；路由在 `api/v1/analyses.go` | 无可版本化的方案草稿/排除词/标签/模板关联；创建请求重试可能重复扣费，需先解决幂等与入队一致性 |
| 前端 | `AnalysisNewPage.tsx` 为类型/关键词/来源/确认四步向导，`web/src/api/analyses.ts` 只发送现有字段 | 新模板配置与方案确认需要独立 API、数据契约，不得仅前端生成假配置 |
| 采集记录 | `analysis.Document` 有 URL、作者、来源类型/名称、可为空的发布时间和内容哈希；平台库 `raw_documents` 同构 | 无抓取时间、原始帖 ID、父帖/引用/转发 ID、官方账号认证状态，不能可靠画出全网传播网络 |
| 告警与报告 | `alert.Service` 现有阈值规则为负面比例+邮件；`GET /reports/templates` 是静态日报/周报/事件模板目录 | PRD 的多维预警与按场景报告模板尚无可直接复用的执行契约，不能把草稿标为已生效 |
| 存储 | 运行中的 PG 业务数据先落 platform 库并以 `tenant_id` 隔离，另有不同结构的 tenant 初始化迁移模板 | 新迁移必须匹配真正接线的库；不能根据 DB-per-tenant 路线图假定生产已经物理隔离 |
| Forum | 存在 Python Forum 引擎及 Go 管线调用路径 | 尚未见事件节点证据契约，接入前必须增量验证引文、租户上下文和降级路径 |

**前置核验**：开发开始前对照当前迁移版本、当前数据库部署拓扑与生产数据源，检查实际 `raw_documents` 质量；上述为工作树事实，不等于线上已上线能力。

## 3. 方案选择及分层

- **方案生成**：首版用受版本控制的六套确定性场景配置 + 有边界的品牌/产品/竞品词展开；不依赖 LLM 自动创建规则。复杂歧义可给用户候选提示，不自动排除可能有价值的原文。选择此方案是为了可解释、可测试、成本可控。未来可增加 LLM 候选生成，但必须经过用户预览确认。
- **时间线**：由 Go 侧读取本租户已采集原文，按事件/分析范围构建证据节点，保存可追溯证据 ID、算法版本和数据窗口。AI 仅用于观点归并/风险标签候选，不可生成“事实节点”或虚构引文；按原文重算时必须可重复、可审计。
- **传播链路**：只有原平台转发/回复/引用 ID 或可解析的明确来源引用允许生成“已证实关系”边；链接内容相同、发布时间接近等只能列为“可能相关”的候选并与正式传播图隔离。首版仅保证时间线；关系证据不足就返回空边集与原因。
- **代码边界**：在 `platform/internal/business/` 新增 `monitorplan`、`eventtimeline` 模块，各自通过接口注入数据存取与服务依赖，不从 business 层直接 import `internal/platform`；`platform/internal/app/container.go` 装配，`platform/internal/api/v1/` 添加薄处理器，`v1.Services` 承载依赖。采集字段扩展沿 `engines/common/scraper.py` → `engines/query_engine/main.py` → `analysis.Document` → PG store 演进；前端请求放在 `web/src/api/`。

## 4. 模块 A：监测方案模板化

### 4.1 数据契约与生成策略

**视角与场景解耦**：保留 `analysis_type=event|brand|competitor|industry` 及手动入口；六个模板各有独立 `template_id`，默认映射为 `brand_daily→brand`、`product_launch→event`、`quality_complaint→brand`、`competitor_update→competitor`、`crisis→event`、`campaign_review→event`；用户可修改分析视角，`industry` 继续在手动入口。`analysis_type` 仍传给洞察提示词，不用模板 ID 覆盖其含义；旧任务、API 与列表显示保持兼容。事件时间线不根据 `analysis_type` 自动编造节点。`template_id` 与实际执行所用 `analysis_type` 均存入运行快照，渲染时以快照为准。`web/src/lib/constants.ts`、`AnalysisNewPage.tsx`、分析结果契约分别添加回归验证。`quality_complaint` 为持续口碑默认 brand，单次危机可由用户改为 event。

版本化静态目录定义六个 `template_id`（`brand_daily`, `product_launch`, `quality_complaint`, `competitor_update`, `crisis`, `campaign_review`）、显示名、适用输入、关键词/排除词规则、来源候选、周期、风险标签、预警规则候选及报告模板 ID；JSON 与 Go 类型化校验同源。模板 ID 和版本存档，更新默认值不回写用户已确认的方案。

用户输入：品牌名、产品名、竞品名、行业、关注场景；每个模板只要求其必需字段。生成结果区分 `proposed`/`edited`：

- 关键词、排除词：源自明确输入和版本化规则；去重、限制长度和数量，排除词默认须经过用户确认。
- 来源：基于**运行时**可用性、当前授权与套餐 feature keys 交集建议；若无可用来源，阻止确认并说明原因，不能仅依赖前端静态来源列表或硬编码套餐级别。
- 周期：用户可选单次或周期执行；周期需持久化计划的时区/下次执行/窗口和启停状态，以平台数据库的唯一执行键防并发重复生成任务。未接通实际调度前此项不能标为“已生效”。
- 风险标签：保存预置/编辑标签及判定规则，将命中的真实原文 ID 和规则版本写入分析结果并用于预警过滤、报告分类；不得仅保存或展示而不计算。
- 预警规则/报告模板：规则必须有真实评估与通知送达记录，失败重试、去重与静默期明确；需将当前 `alert.NewService(alertStore, nil)` 的 discard sender 替换为可验证的真实通道并配套运行时配置。报告模板 ID 必须决定实际渲染版式/段落，并产生对应导出；仅列静态目录不够。若选择周期简报，按用户确认的周期和时区生成，失败可见且不虚报成功。

### 4.2 存储与生命周期

建议新增 platform goose 迁移：`monitor_plans(id, tenant_id, owner_id, template_id, template_version, name, inputs_json, config_json, revision, state, created_at, updated_at)`；`state` 初期仅 `draft|active|paused`，`active` 含义是**已确认可手动运行**，并不等于定时抓取。新增唯一索引 `(tenant_id,id)` 与 `(tenant_id,owner_id,created_at)` 查询索引。表中的 `config_json` 是用户确认后的配置快照，演进时走版本迁移或兼容读取。完整保留草稿但草稿不可入队、不能扣报告额度。

建议三个明确动作：`preview` 为纯计算、不落库、不扣费、不触发告警；`save` 保存/更新方案并加 `revision` 乐观并发检查；`run` 显式创建**一次**分析任务，并在任务上记录 `plan_id/plan_revision` 快照；`schedule` 经用户确认后持续触发运行并记录每期结果、用量与失败状态。请求含租户 principal，客户端提交的 tenant_id/owner_id 一律忽略。报告/告警如未有可执行映射，呈现不可用并阻止“已生效”字样。

**计费/幂等关键阻断项**：当前 `Service.Create` 消耗额度 → `store.put` → `queue.Publish` 不在单一事务，重试可能重复创建/扣费，入队失败也可能遗留任务。方案运行不得直接调用它多次后在 API 层“补丁去重”。先引入持久化 `(tenant_id, action, idempotency_key)` + 请求摘要与返回任务 ID，使用可靠事务/持久队列 outbox 保证扣费、任务记录及待投递消息一致；消费者按任务 ID 幂等处理、重复投递不二次扣费；失败/取消退款复用现有流水并核验幂等。若无法完成事务/outbox、真实通知与持续调度，beta 0.2.3 不得宣称两层能力已交付；未完成部分仅作内部预览，不作为已验收功能。

### 4.3 拟议 API 与前端

以下为拟议契约，最终字段需与现有错误信封 `{code,message,details,request_id}` 和权限配置对齐：

| 方法 | 路径 | 返回/行为 |
|---|---|---|
| GET | `/api/v1/monitor-plans/templates` | 六个模板及字段要求；`templates: []` 而非 null |
| POST | `/api/v1/monitor-plans/preview` | 输入模板/品牌等，返回七项配置候选、每项来源/支持状态与 warnings，不落库 |
| POST | `/api/v1/monitor-plans` | 保存用户确认快照；返回 `plan_id/revision/state`，不扣报告额度 |
| GET/PATCH | `/api/v1/monitor-plans/:id` | 仅本租户且按 RBAC 查看/修改；PATCH 要求 revision 防覆盖 |
| POST | `/api/v1/monitor-plans/:id/run` | 请求幂等键；仅生效方案，返回同一 analysis_id；入队和额度满足上节条件后开放 |

前端在 `AnalysisNewPage.tsx` 提供“按场景创建”与原手动入口并行，预览页可逐项编辑并明确“建议/支持/未启用”，禁止预览即执行；任务详情链接已确认的方案。新接口封装在 `web/src/api/monitorPlans.ts`。原 `POST /analyses` 保持兼容。

## 4.4 全量配置落地与关联实施计划

实施顺序、具体文件责任、红绿测试、验收门槛详见 [beta 0.2.3 实施计划](./IMPLEMENTATION_PLAN.md)。特别注意：现有 query_engine 已接受 `exclude_words`，但 Go `Fetcher` 请求与 `RealCrawlerEngine` 适配未透传；`date_from/date_to` 存在于创建 DTO，但执行链路未见完整透传；告警 `Check` 的存在不表示已接线至分析管线，且 nil sender 会静默丢弃。worker 的 Redis/RabbitMQ 分支目前回退到进程内队列：周期执行、幂等入队和跨进程处理必须在发布前具备独立进程下的可恢复机制。缺一个环节就不能把模板配置标成已生效。

## 5. 模块 B：事件时间线与传播链路

### 5.1 输入证据与事件边界

首版事件绑定单次 analysis（`analysis_id`），不自动把同词的多个任务误合并成同一事件；需要跨分析归并时另开评审。平台表 `raw_documents` 按 `(tenant_id,analysis_id)` 取数；采集至少记录 `fetched_at` 与可为空的 `published_at`，保留原始 URL、来源、作者、hash，明确原文 ID 映射；是否增加 `external_post_id`, `parent_post_id`, `relation_type` 取决于有凭据的来源字段核验。原数据不能核对或无可解析时间的文档放在“未定位原文”列表，不赋予“最早”或推断发布时间；爬取时间不能伪装为发表时间。

关系生成仅在有可核验的原平台引用/回复/转发标识，且两端位于同一租户且权限允许时入图。内容哈希仅用于去重，不足以证明转载。若需要区分跨平台同内容复用，应保留每个来源记录而非简单全局删除副本；采集环节的去重作用域需先评估，缺失的传播节点不应由 LLM 猜测补齐。

### 5.2 节点类型与可信等级

| 节点/关系 | 输入与判定 | 无证据时 |
|---|---|---|
| 首次出现、首次传播 | 在该分析采集集合中发布时间最早的有效原文；传播需有独立关系证据 | 标注“采集范围内最早可验证记录”或缺失，不称全网首发 |
| 关键账号扩散 | 有来源可校验的账号及转发/引用记录；影响力指标需明示数据来源 | 只列原文与作者，不计算虚构影响力 |
| 观点分化、负面爆发 | 多条原文的明确聚合口径、观测窗口、阈值/版本以及原文引用；AI 输出是推断 | 保留事实时间线，推断节点不出现 |
| 品牌回应 | 用户录入或采集到可核验官方链接并经过身份确认；记录审核人和验证状态 | 无“品牌回应”节点 |
| 回应后的变化 | 回复时点前/后可比较的原文集合及观察窗口，样本量和覆盖变化 | 显示“数据不足”；不得推断回应导致变化 |
| 传播关系边 | 明确的原平台转发/回复/引用 ID 或可解析证据 | `edges: []` 且 `reason: insufficient_evidence` |

节点统一携带 `event_time`（必须来自可信原文/证据）、`evidence_document_ids`、`evidence_urls`、`kind`、`basis=fact|inference`、`confidence/algorithm_version`（仅推断）、`limitations`；排序用可用事件时间和确定性的文档 ID 打破并列。确认“最早”必须先过滤缺失时间、重复内容，并明确样本范围。

### 5.3 计算、存储与读取

初期使用**按需计算 + 可失效缓存**避免为每次采集写不可逆阶段：新增 `event_timeline_cache(tenant_id,analysis_id,source_fingerprint,algorithm_version,payload_json,computed_at)`，索引 `(tenant_id,analysis_id)`；`source_fingerprint` 包含原文 ID/时间/关系版本，新增或修正来源时失效。原始证据仍是 `raw_documents`；缓存删除即可重建，不反向改写原文。若首次迭代样本很小，可先不建缓存表、直接限量分页按需计算，性能压测后再决定迁移；不得仅把关键引用存在缓存 JSON 中。

拟议 `GET /api/v1/analyses/:id/timeline?cursor=...&limit=...`：响应 `{nodes: [], edges: [], coverage, warnings, next_cursor}`；未验证传播时给空 edges 与原因，访问前验证 tenant principal 和 `analyses:read`，证据链接不跨租户，分页/限制避免大分析任务拖垮 API。可选 `POST /api/v1/analyses/:id/official-response` 需经 PM 确定权限与认证流程后再开放。前端 `AnalysisDetailPage.tsx` 添加时间轴，按证据/推断样式区分；“关系链不可用”独立说明，不能渲染演示节点冒充真实数据。

Forum 接入第二阶段仅传**带 document_id、原文摘要、时间戳、来源和告警的证据包**；输出引用必须验证 document_id 实存并属于当前 tenant/analysis，校验引文与原文一致；缺少节点或 Forum 引擎失败保留时间线和已采集文档，并给出 warning，不生成空想节点。

## 6. 安全、迁移与测试策略

### 6.1 安全和数据质量

- 所有新表/查询/索引带 `tenant_id`；读写以认证主体的 tenant 为准，权限按现有 RBAC 新增最小功能键并纳入 `v1.Services`；未知套餐/不支持来源 fail closed。分享或跨客户视图本版本不做。
- 自由文本输入长度限制、去 HTML 注入；原文链接仅允许安全协议并在前端转义；日志不输出原文敏感信息或密钥。用户编辑的关键词与排除词不得直接拼 SQL。
- 采集结果标明 `source_status`、发布时间缺失、平台来源推断与否；对于来源标签可能误判的历史数据，先纠偏或隐藏不可靠跨平台结论。

### 6.2 数据库与部署门槛

只为**当前运行时接线的 platform 数据库**准备按顺序编号的 goose migration，避免照搬 `migrations/tenant/0001_init.sql` 的独立模板；是否补齐 tenant 模板需单独迁移设计评审。新平台表和列支持向后兼容，先加表/可空字段后切流；大表索引/回填用分批策略。PL/pgSQL 用 goose `StatementBegin/StatementEnd`，不手改 goose 版本号。以一次性 disposable PostgreSQL 校验升级和回滚；本地重编译内嵌迁移的 CLI，备份并校对 live schema 后再考虑生产，部署前后检查健康及关键查询。

### 6.3 验收与回归

| 类别 | 最少覆盖 |
|---|---|
| 模板单测 | 六类模板、必填项、去重/排除词、无授权来源、无可用报告/告警、预览不写不扣费 |
| Go API 契约 | 默认数组为 `[]`、401/403/404、跨租户 ID、revision 并发、现有分析创建兼容、错误信封和 SSE 不受影响 |
| PostgreSQL 契约 | 新表 CRUD/租户隔离、并发同幂等键 run 只创建一次、额度仅扣一次、取消/失败只退一次、outbox 重投幂等；须使用 `YUQING_TEST_PG_URL` 指向可销毁测试库 |
| 时间线单测 | 原文去重、缺时间/来源误标、时间并列排序、无转发证据空边、官方回应缺失、前后窗口样本不足、带凭据跨平台传播 |
| 引擎与前端 | Go→Python 原文字段真实映射、源数据失败显式 warning、Forum 引文反查；页面六模板选择/修改/确认与事件空态/证据跳转/跨租户不可见 |
| 实际来源验收 | 用获得许可的真实数据检查发布时间、作者、URL、引用 ID 与样本覆盖；用真实账号验证模糊查询和无法构图时的展示；模拟源测试不算通过 |

开发阶段执行 `cd platform && go test ./... -count=1`、带独立 PG 的合约测试、`cd engines && python -m pytest tests/ -v`、`cd web && npm run build && npm run lint` 及相关 Playwright；若缺编译器、测试库或真实来源，明确标注“未验证”，不称功能已可用。

## 7. 实施阶段与决策门槛

| 阶段 | 交付与验收条件 | 阻断项 |
|---|---|---|
| A：数据与契约核验 | 验证运行拓扑、采集字段、可用数据源、现有计费/预警/报告真实契约；锁定模板必填项与方案持续性语义 | 已确认周期监测必须定义调度/失败补偿、通知和套餐边界 |
| B：模板与保存 | 六种确定性模板、无副作用预览、确认保存/编辑、运行时来源状态、向导和接口回归 | 未完成幂等+事务/outbox 前不得开放“运行”按钮；模板的“配置完成”不等于两层交付 |
| C：可运行方案 | 方案运行持久幂等、额度/入队一致、可重复交付；保留原分析手动入口 | 支付/计费、队列重投、租户隔离的 PG 合约测试未通过不可发布 |
| D：有据时间线 | 原文时间/URL 质量核验、事实节点、推断节点、数据缺口 UI；无关系证据时空边 | 发布时不能宣称完整传播链路已实现 |
| E：证据传播与 Forum | 有真实来源引用关系才打开边集；输出证据包、引文校验、降级提示 | 无真实转发/引用数据、官方身份核验不能宣称完整传播图或官方回应闭环 |

**已确认**：六模板及四类分析视角同时保留，七项配置均要有真实执行能力；按用户选择的周期执行、预警与报告需明确确认方可启用。**待定细节**：六模板必填字段及默认频率、套餐支持的最小频率和每次执行计费展示、通知通道凭据、官方回应认证、事件是否跨分析合并及无可验证传播边时的 beta 验收口径。以上决策不允许通过静默跳过功能来代替。
