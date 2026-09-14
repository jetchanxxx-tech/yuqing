# Review Report — 五维度研判链路（P0/P1/P2/P3）

- 审核范围：`git diff 1845084..HEAD`（3 提交：c7f065b P0 / bb4b6d0 P1 / b4c4096 P2-P3）
- 审核角度：逐行 / Go 陷阱 / 跨文件一致性 / wrapper 边界 / 项目约定
- 审核 + 修复日期：2026-09-14
- 结论：**有保留通过（approved with fixes）** —— 发现 4 处需修复项，已全部 RED→GREEN 修复并回归全绿

---

## 一、总体结论

本轮三个提交的实现质量整体扎实：

- **分层铁律无违反**：`business/analysis` 无 engine import；engine↔analysis 的类型转换全部收敛在 `internal/app/pipeline.go` 的 adapter（`toAnalysisDimension`/`toEngineDimensions`），且 Quotes 切片恒非 nil（防前端 `.map()` 崩溃的约定被延续）。
- **契约对齐（人工逐位核对通过）**：Python `dimension` dict 键（id/name/findings/data_points/quotes{text,source}/deep_read/trend）↔ Go `engine.DimensionResult` ↔ `analysis.Dimension` ↔ TS `DimensionResult` 四层 json tag 完全一致；`ReportGenerateReq.dimensions`/`insight_available` 同样对齐。
- **迁移 0005 ↔ store_pg.go 列位（人工逐位核对通过）**：`dimensions` 在 SELECT 列清单、INSERT（`$18`）、UPDATE（`dimensions = $18`）、`analysisArgs`、`scanAnalysis` 五处均为第 18 位（共 20 列）。审核任务描述中的「$19」不准确，实际是 `$18`；`ADD COLUMN IF NOT EXISTS` 幂等，旧二进制读新库不受影响，新二进制读旧库会在所有 analyses 查询上报 column does not exist —— 迁移注释已如实写明部署顺序硬约束，deploy.sh 走 `yuqing-cli migrate platform`（goose 真实现，非占位）。
- **凭据扫描通过**：diff 内无 `sk-` 真实 key（测试仅用 `sk-x` 占位）。
- **管线 P0 修复方向正确**：warn 只描述降级程度、不决定「是否保存」，`insightAvailable` 改按「有产出」判定，并有 `TestPipeline_partialDimensionFailureKeepsInsight` 回归锚点。

但按 5 角度审查发现 4 处必须修复项（见下），已全部修复。

---

## 二、发现列表（按严重度）

### 🔴 R1 重跑（Rerun）不清空旧 warning 与旧结果 —— SetWarning 追加语义交互

- 位置：`platform/internal/business/analysis/service.go` `Rerun`（修复前）
- 问题：`Rerun` 只重置 State/Progress/ErrorCode/时间戳。warning 是**追加语义**（`SetWarning` 只增不删），P0 修复后部分维度失败**必然**写 warning —— 用户重跑一次成功任务，旧 warning「部分维度分析失败：…」原样保留（重跑成功时 `warn == ""`，`SetWarning` 根本不会被调用去覆盖）；旧洞察/报告挂在 queued 任务上同样与状态自相矛盾。该残留从此前偶发（insight 整体失败才写 warning）变成必现。
- 修法：`Rerun` 的 mutate 回调追加清空 `Summary/Warning/Sentiments/Topics/Dimensions/ReportID/ReportContent`。安全性已核实：`report.Service.CreateFromAnalysis` 独立存储报告记录，不读 analyses 行的 ReportID/Content；旧报告仍可在 reports 列表下载。
- 测试：`TestServiceRerun_clearsStaleWarningAndResults`（RED→GREEN）。

### 🔴 R2 引用保真：LLM 给引语套引号会成批误杀「真引语」

- 位置：`engines/insight_engine/main.py` `_normalize_for_match`（修复前）
- 问题：归一化只去空白。LLM 极常见地给「代表性声音」套 `「」`/`“”`/`'…'` —— 包裹符不在源文档正文里，子串匹配必失败 → 该维度引语被全部丢弃、warning 刷屏。反幻觉机制反而摧毁了维度的代表性声音（正是审核重点「误杀全部引语导致维度空」的最大现实触发源）。
- 已排除的绕过方向（维持严格，不放宽）：
  - **跨句拼接**：拼接后的字符串不是语料连续子串 → 照样丢弃 ✓
  - **标点改写**（，↔,、删句尾。）：归一化不动正文标点 → 照样丢弃 ✓（宁可误杀可疑引语，不放过改写）
  - **极短引语**（如「他说」）：子串几乎必命中，属弱校验 —— 暂不设最短长度（prompt 已约束引语为完整表达，设阈值误杀风险更高），记为遗留观察项。
- 修法：`_normalize_for_match` 增加剥两侧**成对**包裹引号（只影响匹配，不改写落库的引语原文）。
- 测试：`test_wrapped_quotes_not_mass_dropped`（RED→GREEN）。

### 🟡 R3 并发闸门重试：首次异常被静默吞掉、退避写死、无测试锚点

- 位置：`engines/insight_engine/main.py` `_run_dimensions.guarded`
- 问题：① `except Exception as exc:` 后 `exc` 未使用 —— 首次失败无任何日志，429 频发时无从排障；② `asyncio.sleep(2)` 魔法数字，测试无法加速；③ 重试语义（瞬时失败恢复 / 持久失败进 failed 列表）无任何测试。
- 已核实正确的部分：重试在 `async with sem` **持锁内**进行（不会绕过并发闸门）；重试仍失败时异常向上抛，由 `gather(return_exceptions=True)` 收进 `failed`（进 warning）；`CancelledError` 继承 BaseException 不会被误吞。
- 修法：提模块常量 `_RETRY_DELAY_SECONDS = 2`（测试 autouse fixture 置 0）+ `logging` 留痕首次失败；补两条测试：
  - `test_retry_recovers_transient_dimension_failure`：heat 第 1 次失败重试成功 → 维度保留、恰好 2 次调用、warning 为空
  - `test_persistent_dimension_failure_after_retry_lands_in_failed`：heat 恒失败 → 恰好 2 次调用（证明重试发生过）、维度缺席、原因进 warning
- 备注：永久性错误（如 key 无效）会白付一次重试 —— 换取瞬时错误的恢复，可接受（注释已说明）。

### 🟡 R4 报告概览卡：情感数据为空但 insight_available=True 时仍渲染误导性 0/0/0

- 位置：`engines/report_engine/main.py` `_overview_cards`
- 问题：旧判据 `if not sentiments and not insight_available`。P0 修复后 `insightAvailable` 按「有产出」判定 —— 五维结论在、情感分类缺失（step ① LLM 形状漂移返回空 sentiments 而非报错）时，`insight_available=True` 短路了 notice，概览卡渲染 0/0/0，与「全部中性」无法区分（正是历史审核 D1 要防的误导）。
- 修法：判据改为 `if not req.sentiments`（有无情感数据，而非有无告警）；notice 措辞改为「未产出或调用失败」。已有 D1 用例（`insight_available=False`）不受影响。
- 测试：`test_generate_insight_available_but_empty_sentiments_still_no_zero_stats`（RED→GREEN，并断言维度章节照常渲染）。

### 🟡 R5 迁移 0005 / store_pg 列位对齐**零自动化覆盖**

- 位置：`platform/internal/business/analysis/store_pg_test.go`
- 问题：`sampleAnalysis()` 不含 `Dimensions`，`assertAnalysisEqual` 不比对 —— 五处列位（SELECT/INSERT/UPDATE/args/scan）一旦漂移，本地测试（PG 用例 skip）全绿、上生产全炸。人工核对当前无漂移（$18，共 20 列），但无回归锚点，违反「TDD 锚点」约定。
- 修法：`sampleAnalysis()` 补全 Dimensions（含 DataPoints/Quotes/DeepRead/Trend），`assertAnalysisEqual` 增加 DeepEqual 比对，`TestAnalysisStore_putPreservesNilAndEmptySlices` 覆盖 dimensions 的 nil↔`null`、`[]`↔`[]` 往返。
- 备注：memory 实现本地即跑；pg 实现 `YUQING_TEST_PG_URL` gate，服务器全量跑时生效。

### 🟢 R6 测试真实性：`test_quotes_normalized_against_whitespace` 名不符实

- 位置：`engines/tests/test_insight_dimensions.py`
- 问题：用例名与注释声称「引语加了多余空白」，但引语文本本身没有任何空白 —— 去空白归一化路径实际**未被锻炼**，测试因普通子串匹配而通过（pass for the wrong reason）。
- 修法：引语改为 `'后排腿部空间局促，身高 178cm\n顶膝'`（真实含空格+换行），归一化路径被真实覆盖。

### 🟢 其他核对结论（无需修复）

- **前端空值防御**：`AnalysisDetailPage.tsx` 的 `DimensionsView` 对 `data_points ?? []`、`quotes ?? []` 全部空安全；`dimensions.length === 0` 走 EmptyBlock；`web/src/api/analyses.ts` 类型与 Go json tag 一致（`data_points`/`quotes`/`trend` 可选 ↔ Go omitempty）。`npm run build` 通过。
- **`/result` 契约**：`api/v1/analyses.go` 对 nil dimensions 归一为 `[]`，`result_test.go` 锁了数组形状与空数组序列化。
- **材料预算截断**：首篇文档无条件保留（`kept > 0` 守卫）、超预算条目不写入、截断显式标记；`_verify_quotes` 语料用**全量**文档（含被截断剔除的），引语保真不因截断漏判。
- **SSE/进度/状态机**：本轮未触及，无回归。
- **deploy.sh 迁移失败兜底提示**仍只列 0001（💭 遗留：建议补列 0005 或改为「执行 yuqing-cli migrate platform」）；CLI 主路径是 goose 真实现，不构成阻断。

### 💭 记录不修（低优先）

- `pipeline.go` 两处「有产出」判定已收敛为 `insightHasOutput` 谓词（本轮顺手修复，防两处条件漂移）。
- `_overview_cards` notice 措辞、`report_engine` 冗余 f-string（`f'<div class="dimension">'`）已顺手清理。
- `_verify_quotes` 极短引语弱校验、`deploy.sh` 兜底提示过时 —— 见上，遗留观察。

---

## 三、已修复项清单

| # | 文件 | 修复内容 | 测试锚点 |
|---|------|---------|---------|
| R1 | `platform/internal/business/analysis/service.go` | Rerun 清空上一轮 warning/摘要/情感/话题/维度/报告 | `TestServiceRerun_clearsStaleWarningAndResults` |
| R2 | `engines/insight_engine/main.py` | `_normalize_for_match` 剥两侧成对包裹引号（仅匹配侧） | `test_wrapped_quotes_not_mass_dropped` |
| R3 | `engines/insight_engine/main.py` | `_RETRY_DELAY_SECONDS` 常量化 + 首次失败留日志 | `test_retry_recovers_transient_dimension_failure`、`test_persistent_dimension_failure_after_retry_lands_in_failed` |
| R4 | `engines/report_engine/main.py` | `_overview_cards` 按「有无情感数据」判 0/0/0 防误导 | `test_generate_insight_available_but_empty_sentiments_still_no_zero_stats` |
| R5 | `platform/internal/business/analysis/store_pg_test.go` | Dimensions 全字段往返 + nil/empty 往返覆盖 | `TestAnalysisStore_putGetRoundTrip`、`TestAnalysisStore_putPreservesNilAndEmptySlices` |
| R6 | `engines/tests/test_insight_dimensions.py` | 空白归一化用例引入真实空白 | `test_quotes_normalized_against_whitespace`（强化） |
| — | `platform/internal/business/analysis/pipeline.go` | 两处「有产出」判定收敛为 `insightHasOutput` 谓词 | 既有 `TestPipeline_partialDimensionFailureKeepsInsight` 守护 |

## 四、验证结果（修复后）

```
cd engines  && python -m pytest tests/ -q
  → 54 passed（原 50 + 新增 4，重试退避置 0 后全速）

cd platform && go vet ./internal/business/analysis/ ./internal/engine/ ./internal/api/v1/
  → 无警告
cd platform && go test ./internal/business/analysis/ ./internal/engine/ ./internal/api/v1/ -count=1
  → 3 包全 ok
cd platform && go test ./... -count=1
  → 全量通过（exit 0）

cd web && npm run build
  → tsc -b && vite build ✓ built in 21.52s
```

凭据自查：本轮全部改动 `git diff` 无 `sk-`、密码、token；测试只用 `sk-x` 占位。

## 五、遗留事项（不阻断合并）

1. `_verify_quotes` 对极短引语（1-3 字）子串近乎必命中 —— 待真实语料观察误判率再决定是否设最短长度。
2. `deploy.sh` 迁移失败的手工提示只列 0001，建议改为指向 `yuqing-cli migrate platform`（或在提示中列出 0005）。
3. insight 引擎 step ① 返回的 `sentiments` 未做 list 形状校验（`topics` 有）：若 LLM 返回 dict 形状，Go 侧 json.Unmarshal 失败会丢弃整份洞察 —— 历史遗留（非本轮引入），建议下轮对齐 `_str_list` 式防御。
