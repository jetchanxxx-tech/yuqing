# beta 0.2.3 测试用例与需求追踪矩阵

**基线：2026-09-28。** 本文是待实施测试设计，不代表通过或上线。需求来源：同目录 PRD §1.1/2/3、TECHNICAL_DESIGN §4–7、IMPLEMENTATION_PLAN Task 0–10；拟议 API/表名在实现后核对。本任务不修改测试/业务代码，不执行生产写操作。

## 执行规则及依赖

- 每例记录日期、commit、环境、输入、命令、脱敏响应及证据/流水 ID、通过/失败/跳过与缺陷号。`局部可测` 仅指有相邻测试，**不等于本例通过**；`待实现` 即尚不可执行；`待 PG/凭据` 为外部阻断。Skip 不得记通过。
- 隔离夹具：A/B 两租户、无权限用户、可销毁 platform PostgreSQL（事先核实 `YUQING_TEST_PG_URL` 非生产）、可审计余额/流水、独立 server/worker、受控时钟/故障注入队列；许可来源含可核实 URL、作者、发表时间、原文 ID/引用关系，并含重复、缺时间、无关系样本；真实测试收件箱。六模板各必填字段、频率、每轮费用、权限与渠道、标签阈值待 Task 0 产品签字，不能臆设默认。严禁以生产 PG 或 mock 冒充外部验收。
- 命令标记：`G` = `cd platform; go test ./internal/business/monitorplan ./internal/business/analysis ./internal/business/alert ./internal/business/report ./internal/engine -count=1`；`PG` = 核实测试库后执行 `cd platform; go test ./internal/business/monitorplan ./internal/business/analysis ./internal/business/alert ./internal/business/report -count=1`，必须核对 PG 用例确实运行而非 Skip；`PY` = `cd engines; python -m pytest tests/ -v`；`UI` = `cd web; npx playwright test --config e2e/playwright.config.ts`（本地/隔离站点）；`INT` = 隔离环境 HTTP+独立 worker+PG 取证，实施后补精确脚本/参数；`LIVE` = 已授权真实来源/通道联调，记录账号权限、来源覆盖和回执。列出命令不等于已经运行。
- 每项配置核对“建议→编辑→确认→运行快照→执行结果/失败原因”，不以保存 JSON 或 UI 勾选证明 enabled；核对 plan_id、revision、template_id、analysis_type、config_snapshot。未获明确确认，周期运行、通知与额外消费保持关闭。

## 需求追踪矩阵

| 需求 / 来源 | 用例 | 放行证据 |
|---|---|---|
| 六模板/四视角/旧入口：PRD §1.1/2；Task 1 | T01–T04 | 六 ID/映射；视角可改；手动四种与历史空值兼容 |
| 预览/保存/权限：PRD §2；设计 §4.2–4.3；Task 2 | T05–T08 | 七项可编辑，预览无副作用；revision 与租户/RBAC |
| 关键词、排除词、来源、周期：设计 §4.1；Task 3/4/6 | T09–T15 | 实际采集/调度结果、来源状态、失败记录和用量 |
| 标签、告警、报告：PRD §2.3；Task 5/7/8 | T16–T21 | 文档级命中、真实送达、按模板实际生成 |
| 幂等余额/跨进程/退款：设计 §4.2/6；Task 3/6 | T22–T28 | PG 并发、重启、重投不重扣、多退、丢消息 |
| 时间线/传播/Forum/租户：PRD §3；设计 §5；Task 10 | T29–T36 | 可追溯证据、无边空态、跨租户拒绝、引文核验 |
| 联调/迁移/发布：Task 0/9/10 | T37–T40 | 六模板逐一真实运行；灰度/生产另行审批 |

## 模板、视角与方案契约

| ID | 前置条件 | 步骤 | 预期 | 层级/命令 | 当前状态 |
|---|---|---|---|---|---|
| T01 六模板 | v1 目录，必填待签字 | 枚举 `brand_daily/product_launch/quality_complaint/competitor_update/crisis/campaign_review`；逐模板输入适用/缺必填/缺非必填字段并预览 | 映射依次 `brand/event/brand/competitor/event/event`；各有七项与依据；非必填不阻塞，未知 ID/版本拒绝 | Go `G`，API `INT` | **局部可测**：目录六映射已有测试；并行工作树已出现模板/预览草稿 API，七项真实执行 **未验证** |
| T02 四视角 | 模板及手动入口可用 | 投诉改 event、新品改 brand；手动分别选 event/brand/competitor/industry；读取历史空类型 | template_id 不变，快照/洞察用所选视角；旧 API/任务兼容，event 不自动造时间线节点 | `G`、`UI`、`INT` | **局部可测**：目录与前端测试存在；快照/真实洞察 **待实现** |
| T03 编辑保存 | A 可预览/保存 | 七项逐一编辑，重开，模板默认版本升级后再重开 | 编辑值保留；历史已确认配置不回写，版本及快照可查 | `INT`、`PG`、`UI` | **待实现/待 PG** |
| T04 旧入口 | 旧任务与额度基线 | 旧 `POST /analyses` 创建四视角，查列表/详情/SSE，断开再连 | 状态/终态正常，空数组非 null，无重复扣费、旧任务可见 | `G`、`INT`、`UI` | **局部可测**，本轮未跑全链路 |
| T05 预览零副作用 | PG 余额、任务、告警/outbox 计数已记 | 重复 preview 含歧义词/告警草稿，对账 | 零新增方案/任务/流水/通知；余额不变，不暗启 | `INT`、`PG` | **待实现/待 PG** |
| T06 revision/状态 | 可销毁 PG | 保存 draft/active；双客户端同 revision PATCH；重放 save；暂停后 run | 一次更新成功一次标准冲突；草稿/暂停不可入队扣费；active 仅可手动运行 | `PG`、`INT` | **待实现/待 PG** |
| T07 权限隔离 | A/B、无权/匿名用户，A 方案 ID | B 用 A ID GET/PATCH/run，body 伪造 tenant/owner；查空列表及分页 | 401/403/404 按 RBAC、防泄漏；标准 `{code,message,details,request_id}`；列表 `[]` | `INT`、`PG` | **待实现/待 PG** |
| T08 错误输入 | 长文本/HTML/SQL 字符、歧义品牌、未知套餐、不可用执行器 | preview 后确认，检查日志 | 长度/转义安全，不拼 SQL/泄密；未知套餐/来源 fail closed；失效项明示不可用且不建半成品 | `G`、`INT` | **待实现** |

## 七项配置：逐项验证真实执行

| ID/配置 | 前置条件 | 步骤 | 预期 | 层级/命令 | 当前状态 |
|---|---|---|---|---|---|
| T09 关键词 | 许可源含命中/未命中/同名歧义原文；A 已确认 | 编辑关键词两次运行，对照快照、Go→Python 请求、入库、洞察和报告 | 仅确认词实际参与检索；去重、限长；两轮结果与各自快照对应，无依据不自动扩词 | `G`、`PY`、`LIVE` | **局部可测**：旧关键词链路存在；方案快照/真实源 **待实现/待凭据** |
| T10 排除词 | 同一许可源有噪声与应保留原文 | 用户确认排除词后 run；核对传输/过滤前后计数、入库/标签/报告 | 噪声不进入下游，重要原文不被默认误排；无法平台下推时标注应用层过滤及漏检风险 | `G`、`PY`、`LIVE` | **局部可测**：HTTP 字段及 Python 过滤测试存在；全链路/真实源 **待凭据** |
| T11 数据源权限 | A 套餐 feature key、授权与运行时健康状态可控 | 分别选已授权、失效、无授权、不支持来源；运行前撤权 | 仅推荐可用∩授权∩套餐支持；无有效源阻止确认/执行；标 source_status/限制/更新时间；不伪造结果 | `INT`、`PG`、`LIVE` | **待实现/待凭据/PG** |
| T12 监测日期窗口 | 源有边界内外与无发表时间原文 | 输入首尾日与逆序日，run 核对落库/标签/报告 | 边界包含首尾；逆序拒绝；无 `published_at` 明示不精确，不以 fetched_at 冒充 | `G`、`PY`、`LIVE` | **局部可测**：Python 边界和 Go 传输有相邻测试；全链路 **待凭据** |
| T13 周期确认 | 隔离 PG、时钟、频率/时区/每轮费率签字 | 未勾周期越过时槽；确认后跨两个时槽及 DST/跨时区运行 | 未确认零任务；每时槽一个独立 analysis/正确 UTC 窗口；显示 next_run/每次费用，active 不自动定时 | `PG`、`INT` | **待实现/待 PG** |
| T14 周期暂停/失效 | T13 已运行、账本已记 | 暂停、改 revision、余额不足/来源撤权后越过新时槽并重启 worker | 暂停不产生新任务，不暗中取消在途；新 revision 仅作用新时槽；不足不扣费，失败和恢复可见 | `PG`、`INT`、`LIVE` | **待实现/待 PG/凭据** |
| T15 七项状态 | 用户已保存七项 | 逐项让执行器失效/恢复，查运行/错误/UI 并对照快照 | 每项有候选/已确认/已启用或失败的证据；未开启通知或周期不显示 enabled；每次运行结果可追踪 | `INT`、`UI` | **待实现** |
| T16 风险标签 | 含命中、歧义、零样本、重复及跨租户文档 | 改标签/规则版本、运行，核对文档 ID、标签、预警过滤与报告 | 仅本租户真实原文被标记，重复不双计；零文档不全命中；规则版本/未命中可查，标签影响结果/预警/报告 | `G`、`PG`、`INT` | **待实现/待 PG** |
| T17 告警评估/静默 | 用户确认阈值、标签、收件人/权限；有命中/不命中样本 | 重放同一 run、改变阈值/静默期；零样本/无收件人再测 | 仅有数据且匹配时触发；按 `(tenant,plan,rule,run)` 去重并遵守静默；无权/无地址不标已发 | `G`、`PG`、`INT` | **局部可测**：旧阈值 mock 测试有；方案规则/PG 去重 **待实现** |
| T18 真实送达/重试 | 安全配置真实测试通道/收件箱 | 启用规则，核对发信记录和收件/回执；注入超时、重复投递后恢复 | pending/sent/failed 有可审计回执，失败不虚报 sent，重试不重复通知，日志无密钥 | `LIVE`、`PG`、`INT` | **待实现/待凭据/PG**：当前 `alert.NewService(alertStore, nil)` 丢弃发信 |
| T19 未确认不通知 | 通道可用，只有告警草稿 | preview、save、只执行手动 run（未启用通知） | 零投递，UI 显示未启用 | `INT`、`PG` | **待实现** |
| T20 报告模板 | 同一许可数据、经确认 daily/weekly/event 模板及导出格式 | 分别用三模板 run/导出；核对模板 ID、段落/版式及引用；提交不支持 ID | 模板确实改变渲染、仅导出已实测格式；真实引用、不补造，未知 ID 显式失败 | `G`、`PY`、`INT` | **局部可测**：旧报告格式测试存在；方案模板驱动 **待实现** |
| T21 周期简报 | 用户明确确认简报周期/时区，测试收件箱 | 到期发简报、重放 slot；令 report 引擎失败 | 每期至多一份，关联实际分析；失败保留原文/有效洞察及 warning，不虚报已交付 | `INT`、`PG`、`LIVE` | **待实现/待凭据/PG** |

## 幂等余额与失败恢复

| ID | 前置条件 | 步骤 | 预期 | 层级/命令 | 当前状态 |
|---|---|---|---|---|---|
| T22 手动并发幂等 | 隔离 PG、足额余额、active 方案 | 双客户端同 `(tenant,action,key)` 同 body 同时 run、超时后重试、重启服务再重试 | 同一 analysis_id、只一任务/一次扣费/一次 outbox；持久键跨进程有效 | `PG`、`INT` | **待实现/待 PG**：旧 Create 非事务幂等 |
| T23 键冲突/租户 | T22 已执行，B 有同名 key | A 同 key 不同 body（含 plan/revision）；B 同 key 发请求 | A 冲突不产生新任务/扣费；B 租户独立且不泄 A ID | `PG`、`INT` | **待实现/待 PG** |
| T24 多副本时槽 | 两个独立 worker、同到期 slot | 并发抢占同 `(tenant,plan,scheduled_slot)`，重复触发及重投 | 仅一次独立分析/扣费；失败重试可审计，不漏期 | `PG`、`INT` | **待实现/待 PG** |
| T25 崩溃与 outbox | 独立 server/worker、持久 PG/outbox，禁进程内回退 | 在提交前后、领取后 ack 前依次注入断电/重启/重放 | 不会扣费无任务或永久丢消息；持久消息恢复，消费者按 ID 幂等；终态不重入 | `PG`、`INT` | **待实现/待 PG**：无持久 outbox 证明 |
| T26 失败/取消退款 | 余额、流水及已运行任务已记 | 致命 fetch 失败和用户取消各一任务；重复失败/取消事件并重启 | 各最多退一次、账本正确，终态拒非法转移；非致命洞察失败不走致命退款 | `G`、`PG`、`INT` | **局部可测**：旧入队失败退款单测；跨进程/PG **待实现** |
| T27 非致命降级 | 有可用原文及部分洞察 | 分别注入 insight/report 超时或 partial+warning，查账本与报告 | 原文及有效 partial 保留，警告可见；不假报成功或按抓取失败清空/退款 | `G`、`PY`、`INT` | **待实现**：须新增链路故障证据 |
| T28 额度或来源不足 | A 余额不足或来源失效 | 并发 run/retry，对比任务、余额、流水、失败原因；补足余额/权限后重试新 key | 不创建可执行任务、不扣费、不返回模拟数据；错误可见，恢复后才执行 | `PG`、`INT`、`LIVE` | **待实现/待 PG/凭据** |

## 事件时间线、传播与多租户

| ID | 前置条件 | 步骤 | 预期 | 层级/命令 | 当前状态 |
|---|---|---|---|---|---|
| T29 时间线证据 | A 一个 analysis 含许可原文/发布时间/来源/URL/作者 | `GET /api/v1/analyses/:id/timeline` 分页，点击 document_id/URL 对照原文 | 按实际时间排序；“最早”仅指采集范围；事实/推断可区分，证据可追溯 | `PG`、`UI`、`LIVE` | **待 PG/凭据/联调**：并行工作树已出现 timeline 路由与分析时间线文件；真实原文证据及全契约 **未验证** |
| T30 去重/缺时间 | 重复原文、跨平台同文、同时间文、无 published_at、误标源 | 重算时间线并翻页对照原文 | 同一原文不重算首次；并列按文档 ID 稳序；无时间进入未定位原文，不用抓取时间冒充；不删跨平台可验证关系 | `PG`、`LIVE` | **待实现/待 PG** |
| T31 无关系空态 | 多原文只有相近时间/相同 hash，无引用 ID | 查时间线及 UI 传播图 | `edges: []` 与 `insufficient_evidence`/覆盖缺口；不画假链，缺节点与缺边分开说明 | `INT`、`UI` | **待实现** |
| T32 真实关系边 | 许可源两端带可复核转发/回复/引用 ID；跨平台凭据单独备齐 | 校验原始关系，建边；移除 ID 只留同内容再重建 | 仅有证据的边入图；端点均属于授权租户/analysis；无法验证的跨平台结论隐藏 | `PG`、`LIVE` | **待实现/待凭据/PG** |
| T33 官方回应 | 经认证官方链接、未认证账号、回应前后充分/不足样本 | 建回应/变化节点并查审核人、窗口、样本数 | 仅已核验官方回应出现；无回应不补造；仅陈述前后对照，样本不足示缺口，不称回应导致变化 | `INT`、`PG`、`LIVE` | **待实现/待凭据**：官方认证流程待确认 |
| T34 跨租户隔离 | A/B 有各自分析、原文、边、缓存、报告 | A 取 B 的 timeline、证据链接、分页 cursor、报告、Forum 引用；反向重试 | 不泄文档/账号/URL/边/报告；tenant+analysis 双重授权，标准错误、空集合 `[]` | `INT`、`PG` | **待实现/待 PG** |
| T35 Forum 引文 | 证据包含 tenant、analysis、document_id、摘要、时间、来源及警告 | 请求引本租户和跨租户/不存在文档；篡改引文；使 Forum 超时 | 仅本 analysis 且与原文一致的引用可保留；错引拒绝；超时保留原文/时间线并 warning | `G`、`PY`、`PG` | **待实现/待 PG** |
| T36 缓存与源故障 | 已有时间线；可追加/修正原文和关系 | 修改证据并重算，删除可失效缓存；模拟部分采集失败 | 指纹/版本更新或按需重算得新结果；原文权威不变，源失败保留已有证据并提示缺口，不生成假节点 | `PG`、`INT` | **待实现/待 PG**：缓存是否采用待决定 |

## 联调、迁移与上线门槛

| ID | 前置条件 | 步骤 | 预期 | 层级/命令 | 当前状态 |
|---|---|---|---|---|---|
| T37 六模板逐一验收 | 上述接口、许可真实来源、隔离 PG、真实测试收件箱、额度均就绪 | 每种模板独立预览/编辑/确认七项并运行一轮；经用户确认周期及通知后观察一时槽；核对采集→洞察→标签→规则→送达→指定报告 | 六条完整证据链各有运行结果/失败状态、用量与报告；任一必需配置无执行能力阻断发布；mock/目录绿不代替真实结果 | `INT`、`LIVE` | **待实现/待 PG/凭据**；本任务不执行 |
| T38 隔离迁移/回归 | 可销毁 PG、CLI 重新本地构建并含嵌入 migration | 测升级/回滚及 schema；跑 Go 全测/PG/Python/web build/lint/本地 Playwright，记录 Skip | 迁移匹配真正接线的 platform 库；无回归；跳过 PG 或缺外部源单列未验证 | `cd platform; go test ./... -count=1`、`PG`、`PY`、`cd web; npm run build; npm run lint`、`UI`；CLI 迁移按 runbook | **待执行/待 PG**；绝不测试生产库 |
| T39 独立进程及灰度 | 审批/备份、核对 live schema 和 systemd 路径；本地构建 server/worker/CLI/web | 先隔离演练多进程/重投；正式上线需另行审批，先迁移后启服务，核对健康、积压、送达/失败和费用 | 不丢单、不重扣；监控可溯，生产不得编译；未批准不推送/部署 | 隔离 `INT`；生产单独审批取证 | **尚不可执行**：本任务禁止 push/deploy，未核对 live 环境 |
| T40 关闭与回退 | 隔离灰度有在途任务和账本 | 分开关闭调度与通知，保留单次/旧手动运行，演练恢复 | 无新时槽或新通知，旧任务/原文/账本可用；异常可诊断且可恢复 | 隔离 `INT`；生产另审批 | **待实现/待环境/审批** |

## 本轮只读核对与未验证清单

- 工作树已有 `platform/internal/business/monitorplan/{catalog,service}_test.go` 六映射/视角测试；`web/e2e/analysis-entry.spec.ts` 使用路由拦截验证只发送旧 `/analyses` 字段，并明确不发 `template_id/schedule`，**不能证明方案保存、调度或真实来源**。`platform/internal/engine/real_test.go` 用 `httptest` 检查过滤字段，`engines/tests/test_query_search.py` 用 monkeypatch 覆盖排除词/窗口/来源，均不等于实际授权来源链路。alert/report 单测亦不能证明方案规则已真实送达或模板实际应用。
- 环境只读核对：`YUQING_TEST_PG_URL` **未设置**；当前 `platform/internal/app/container.go` 使用 `alert.NewService(alertStore, nil)`。真实通知通道凭据、许可来源账号/关系字段、官方回应验证身份、独立 PG 和产品签字未提供；未核验生产 schema、实际部署或服务状态。**所有待实现/待 PG/凭据案例均未验收，不把 mock 绿色当上线**。
- 待实现完成后依矩阵逐例添加 Go 单元/API、真实 PG 契约、Go↔Python、pytest、Playwright 及许可来源/测试通道自动或半自动回归，填实精确测试名、脚本和执行证据。本任务只写 TEST_CASES.md。只读核对过程中发现工作树另有并行未提交变更（包括方案草稿 API 与 timeline 路由）；未修改、清理或归因这些用户文件，实际落地状态须在后续测试时重新核查。

### 2026-09-28 本轮实际执行记录（非验收）

- 初次误在仓库根运行 `go test ./internal/business/monitorplan ./internal/engine -count=1`：退出 1，提示 `cannot find main module`；改在 `platform/` 运行相同命令：两个包均 `ok`（monitorplan 2.860s、engine 3.318s）。仅证实局部目录/HTTP 适配器单测，未执行 PG 或真实来源。
- 在 `engines/` 运行 `python -m pytest tests/test_query_search.py -q`：**退出 1，11 passed、7 failed、1 warning（5.77s）**。失败用例：`test_all_distinct_nonempty_keywords_search_and_deduplicate_before_filters`、`test_bounded_per_keyword_results_and_response_truncation_are_visible`、`test_partial_keyword_failure_returns_data_and_explicit_warning`、`test_all_keyword_failures_never_report_successful_zero_results`、`test_single_keyword_failure_is_not_a_successful_empty_search`、`test_too_many_nonempty_keywords_rejected_not_silently_skipped`、`test_large_result_request_is_capped_and_reports_the_cap`。例如空白关键词被传入而未优先搜索有效词，超出关键词数量返回 200 而非预期 422，单词结果上限请求 500 未限制到 100。测试均使用夹具/monkeypatch，不涉及真实源；这些失败影响 T09/T10/T11/T27/T37，**需实现方修复与复测后才能推进发布门槛**。本轮不修改测试或业务代码。

- **并行修复后的复测**：后端完成多关键词/有界结果与显式失败处理后，在 `engines/` 执行 `python -m pytest tests/ -q`，退出 **0，102 passed、1 条第三方弃用警告（8.70s）**。上述七项失败为实现过程中的红灯，已在当前工作树的模拟测试中修复；仍无真实 Bocha 数据源或生产效果证据，不代表 T09–T11/T37 完成验收。
