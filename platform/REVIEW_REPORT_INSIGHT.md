# 代码审核报告 — 情感分析 / 话题聚类 / AI 报告（DeepSeek 全链路）

- **审核范围**：`git diff 898e8a4..20feda0`（26 文件 / +1957 −78）
- **审核角度**：逐行、Go 陷阱、跨文件一致性、wrapper 边界、项目约定
- **审核日期**：2026-09-13
- **结论**：**有条件通过** —— 3 个 🔴 必须修复项已当场修复（RED→GREEN 补测试），
  修复后 Go 全量测试与 Python 27 用例全绿；仍有 6 个 🟡 建议项未在本轮改动，
  其中 2 项（D1/D2）建议合并到本次提交一并处理，理由是它们直接影响用户看到的降级信息。

---

## 1. 总体结论

实现质量整体是**上升**的：分层纪律干净（见 §4，grep 实测零越界）、
Go 侧 transport 的测试覆盖到位（含错误状态码转 error 的负例）、
Python 侧契约与 Go json tag 逐字段对齐（见 §5）、降级思路（分析失败不致命）方向正确。

但本轮引入了 3 个真实的 🔴：

1. **报告引擎把三类不可信内容原样拼进 HTML**（租户输入的分析名、抓取的第三方网页标题/URL、LLM 输出）
   —— 已实测可注入 `<script>`、`<img onerror>`、`javascript:` href。
2. **管线总预算沿用 `engines.query.timeout`（示例/生产配置 60s）**，而本轮新增了 3 次 LLM 调用，
   deadline 被分析阶段吃光后 `p.step` 会拿到 `ctx.Err()` 并把任务判为 `failed(timeout)`
   —— 这正好把「分析失败不致命」的设计反转成「任务失败」。
3. **报告引擎对 LLM 输出形状不设防**：合法 JSON 但 `topic_analyses` 是字符串数组即 500，
   整份报告丢失 —— 与文档承诺的「LLM 失败降级为纯数据报告」相矛盾。

三项均已修复并有回归测试锁住（§3）。

---

## 2. 审核方法

| 手段 | 说明 |
|------|------|
| 全量读 diff | `git diff 898e8a4..20feda0`，逐文件逐行 |
| 实测探针 | 用 `TestClient` 对 report/insight 引擎注入敌意输入与形状漂移的 LLM 输出，观察真实状态码与渲染结果 |
| grep 验证分层 | 见 §4，不依赖「读代码判断」 |
| 交叉核对契约 | Python Pydantic 模型 ↔ Go json tag ↔ 前端 TS 类型 三方比对（§5） |
| 全量测试 | `go test ./... -count=1` + `python -m pytest tests/ -q` + `npx tsc -b` |

---

## 3. 🔴 必须修复（已修复，含 RED→GREEN 证据）

### R1 报告引擎 HTML 注入 —— 三类不可信内容全部未转义

- **文件**：`engines/report_engine/main.py`（修复前 L129-192 `_render`）
- **问题**：动态内容直插 HTML，无一转义：

  | 来源 | 注入点 | 可控方 |
  |------|--------|--------|
  | `req.title`（分析名称） | `<title>$title</title>`、`<h1>$title</h1>` | 租户输入 |
  | `documents[].title` | `<td>{title}</td>` | **被抓取的第三方网页** |
  | `documents[].url` | `<a href="{url}">` | **被抓取的第三方网页**（`javascript:` 可用） |
  | LLM 输出 `executive_summary` / `topic_analyses` / `risk_points` / `recommendations` | `<p>` `<li>` `<b>` | 由被爬正文派生（提示词注入可达） |

- **实测**（修复前，探针输出）：
  ```
  closing </title><script> injected : True
  raw img tag injected        : True
  javascript: href            : True
  ```
- **为何不是「有 sandbox 就没事」**：前端 `AnalysisDetailPage.tsx:514` 的 `sandbox=""` 确实会阻止脚本执行，
  但它现在是**唯一防线**：报告 HTML 会经 API 返回、被保存/转发，未来加一个「新窗口打开」按钮即升级为存储型 XSS；
  且 `sandbox=""` 拦不住 `<meta http-equiv=refresh>` 把 iframe 自身导航到钓鱼页。
- **修复**：新增 `_esc()`（全字段转义）、`_safe_url()`（只放行 http/https，其余退化为 `#`），
  渲染全部改走 helper；拆出 `_topic_rows()` / `_document_rows()` 便于审查。
- **回归测试**：`engines/tests/test_report.py::test_generate_escapes_html_from_title_documents_and_topics`、
  `::test_generate_escapes_llm_generated_markup`（先 RED：`assert '<script>' not in content` 失败）。
- **附带澄清（已实测）**：`string.Template.substitute` **不会**重扫替换进去的值，
  `Template('t=$v').substitute(v='$body')` → `'t=$body'`，因此不存在二阶模板注入，无需改动。

### R2 管线总预算取自单个引擎的超时，慢分析会被判为任务失败

- **文件**：`platform/internal/app/container.go:122-125`（修复前）+ `platform/internal/business/analysis/pipeline.go:117,159-181`
- **问题**：`pipeTimeout` 直接取 `cfg.Engines.Query.Timeout`。该值是**单个采集请求**的预算，
  而 `Handle` 用它作为整条管线（采集 → 洞察 2 次 LLM → 报告 1 次 LLM）的 deadline。
- **为什么这次会炸**：`platform/config.example.yaml:76` 为 `query.timeout: "60s"`，
  `scripts/deploy.sh:233` 首次部署时**原样复制**该文件（且「已存在的配置绝不覆盖」），
  故生产 `config.yaml` 也是 60s。`engines/common/scraper.py:119` 的抓取是**串行**的
  （`per_source = max(50/n_sources, 3)` 个候选、每个 30s 上限），采集本身就可能吃掉几十秒；
  再叠 3 次 DeepSeek 往返，60s 预算被击穿是常态而非边缘。
- **失败路径**：`ctx` 在分析阶段耗尽 → `runInsight` 返回 warning（符合设计）→
  但随后 `p.step(ctx, msg, StateGeneratingReport, ...)` 命中 `if err := ctx.Err(); err != nil { return err }`
  → `p.fail(msg, "pipeline_error", ctx.Err())` → 错误码归类为 `timeout` → **任务 failed**。
  即「分析失败不致命」只在「还有剩余时间」时成立。
- **修复**：新增 `pipelineBudget(cfg)` —— 已接线阶段（URL 非空）的预算之和；
  返回 0 时由 `NewPipeline` 兜底 3 分钟。示例配置下 = 60s + 120s + 300s = 480s。
- **回归测试**：`platform/internal/app/pipeline_budget_test.go`（新增 3 个测试 / 8 个子用例），
  含 `TestPipelineBudget_exceedsQueryBudgetWhenEnginesConfigured` 直接钉住「必须大于采集预算」。
- **遗留权衡**：`memoryQueue.dispatch` 是**单 goroutine 串行**（`pkg/queue/queue.go:107`），
  所以这个 ceiling 同时是「一个卡住的任务阻塞队列的时长」。480s 是 ceiling 不是常态，
  但更正确的长期方案是给管线独立配置项（如 `pipeline.timeout`）而不是派生自某引擎，
  并把消费者改为并发（见 §7 待办 T3）。

### R3 报告引擎对 LLM 输出形状不设防 —— 合法 JSON 即 500，违背降级承诺

- **文件**：`engines/report_engine/main.py`（修复前 `_render` L151-162）
- **问题**：`insight["topic_analyses"]` 假定为 `list[dict]`，`risk_points` / `recommendations` 假定为 `list[str]`。
  LLM 返回合法 JSON 但类型漂移（字符串数组、单字符串、整体为数组）即 `AttributeError`；
  且该异常发生在 `_llm_insight` 的 try **之外**，`generate` 未捕获 → FastAPI 500。
  Go 侧 `RealReportEngine.Generate` 把 500 转为 error → `runReport` 记 warning →
  **整份报告不产出** —— 而模块 docstring 承诺「LLM 失败时降级为纯数据报告」。
  即：降级只覆盖了「调用失败」，没覆盖「输出不可用」。
- **实测**（修复前）：
  ```
  topic_analyses = ["后排空间", "油耗"]  → status = 500  Internal Server Error
  LLM 返回 JSON 数组                      → status = 500  Internal Server Error
  ```
- **修复**：新增类型防御 helper——`_str_list()`（字符串/数组统一）、`_topic_analyses()`
  （非 dict 项按话题名处理）、`_topic_rows()`/`_document_rows()` 跳过非 dict 项；
  `_llm_insight` 增加 `return data if isinstance(data, dict) else None`（`chat_json` 只保证「能解析成 JSON」）。
- **回归测试**：`::test_generate_tolerates_out_of_shape_llm_json`、`::test_generate_tolerates_non_dict_llm_payload`
  （用 `TestClient(app, raise_server_exceptions=False)` 断言真实状态码）。

---

## 4. 分层验证结论（grep 实测，非阅读判断）

```bash
$ grep -rn "internal/platform" platform/internal/business/     # business → platform
platform/internal/business/report/service.go:9:	"github.com/yuqing/platform/internal/platform/billing"
platform/internal/business/report/service_test.go:9

$ grep -rn "internal/engine" platform/internal/business/       # business → engine
(none)

$ grep -rn "internal/business" platform/internal/platform/     # platform → business
(none)

$ grep -rn "internal/engine" platform/internal/api/            # api → engine
(none)
```

**结论**：

- `platform ↔ business` **双向零越界**，唯一例外仍是已记录的
  `business/report → platform/billing`（`REVIEW_REPORT.md` §8.4 的已知项，本轮未新增）。
- `business → engine` **零越界** —— 本轮新增的 `analysis.InsightAnalyzer` / `analysis.ReportGenerator`
  是纯业务接口，HTTP transport 落在 `internal/engine/{insight,report}.go`，
  适配器 `engineInsightAdapter` / `engineReportAdapter` 落在 `internal/app/pipeline.go`，
  **正是 CLAUDE.md 规定的「适配在 app 层」**，符合项目既有模式。
- `api → engine` 零越界（handler 只依赖注入的 service）。

---

## 5. 跨文件一致性核对（Python ↔ Go ↔ 前端）

| 契约点 | Python 产出 | Go json tag | 前端类型 | 结论 |
|--------|-------------|-------------|----------|------|
| `sentiments[].document_id/sentiment/score` | ✅ | `Sentiment` 一致 | `SentimentItem` 一致 | ✅ |
| `sentiments[].emotions` | 提示词要求 LLM 输出 | **Go `Sentiment` 无此字段 → 静默丢弃** | 无 | 🟢 N4 |
| `topics[].id/name/keywords/doc_count/trend` | ✅ | `TopicResult` 一致 | `Topic` 仅声明 name/doc_count/trend | ✅ |
| 情感枚举 `positive\|negative\|neutral` | ✅ 提示词 | 注释一致 | Tag 映射一致 | ✅ |
| 趋势枚举 `rising\|stable\|falling` | ✅ 提示词 | 注释一致 | `trendArrow()` 已归一化 | ⚠️ → 已修，见 R4 |
| `summary` | ✅ | `InsightAnalyzeResp.summary` | `result.summary` | ✅ |
| `report_id/file_key/format/content` | ✅ | `ReportGenerateResp` 一致 | `AnalysisReport` 一致 | ✅ |
| `/result` → `report: {id,format,content}` | — | `gin.H` | `AnalysisReport \| null` | ✅ |
| `/result` → `sentiments.items` 恒为 `[]` | — | handler 里 nil 兜底 | `items?` | ✅ |
| `/result` → `topics` 恒为 `[]` | — | handler 里 nil 兜底 | `?? []` | ✅ |
| `AnalysisSummary`（列表） | — | 与结构体兼容 | 未声明新字段 | ✅（但见 §6 D4） |

**枚举一致性陷阱（已修，记 R4）**：`web/src/pages/AnalysisDetailPage.tsx:484`（修复前）旧代码用
`t === 'up' ? '上升' : t === 'down' ? '下降' : '平稳'` 判定文案，而引擎返回 `rising/stable/falling`
→ 箭头（走 `trendArrow`）正确，**文案永远是「平稳」**。已改为统一经 `trendArrow(t)` 归一化后再选文案。

---

## 6. 🟡 建议修复（本轮未改动）

### D1 报告生成用零值洞察 —— 分析失败时报告会显示 0/0/0（**建议本次一并修**）

- **文件**：`platform/internal/business/analysis/pipeline.go:175` + `:216-227`
- **问题**：`runInsight` 失败时 `insight` 是 `InsightResult{}`，却仍原样传给 `runReport`。
  报告引擎收到空的 `sentiments/topics` → 概览卡片渲染 **正面 0 / 负面 0 / 中性 0**、无话题聚类表。
  前端同时显示「分析已完成，洞察报告已生成」+「部分分析未完成」两个 Alert。
  对读者而言 0/0/0 与「全部中性」无法区分 —— 这是**误导性数据**而非明显缺失。
- **建议**：`runReport` 增加 `degraded bool`（或直接判断 `insight.Summary == "" && insight.Sentiments == nil`），
  在降级时把情感统计段落替换为「情感分析未完成」提示，而不是渲染 0/0/0。

### D2 后写的 warning 覆盖先前的 warning，降级原因丢失（**建议本次一并修**）

- **文件**：`platform/internal/business/analysis/pipeline.go:162-177`
- **问题**：`SetWarning` 是**整体覆盖**：
  - insight 失败 → `warning = "insight analysis failed: …"`
  - report 也失败 → `SetWarning` 覆盖为 `"report generation failed: …"`，**洞察失败原因丢失**。
- **建议**：改为「追加」语义（`SetWarning` 内 `if a.Warning != "" { a.Warning += "; " }`），
  或让管线把两条原因合并后一次写入。

### D3 `SetReport`/`SetInsight` 落库失败被吞，warning 为空

- **文件**：`platform/internal/business/analysis/pipeline.go:166-168`、`:233-236`
- **问题**：`runReport` 的文档注释写「warning 非空表示降级（报告未生成）」，
  但 `p.svc.SetReport` 失败时走 `return ""` —— 报告**确实没落库**，却不产生 warning，
  任务照样 completed，前端显示「报告已生成」但报告 Tab 是空的。
  同一文件 `:166` 的 `SetInsight` 失败也只 `log.Warn` 不设 warning。
- **建议**：两处落库失败都返回 warning（如 `"report generated but not stored: …"`），与契约保持一致。

### D4 报告正文曾内联进列表/详情端点（**已修，记 R5**）

- **文件**：`platform/internal/business/analysis/service.go:76-90`
- **问题**：`ReportContent`（KB 级 HTML）挂在**共享**的 `AnalysisResult` 上，
  于是 `GET /api/v1/analyses`（列表）与 `GET /api/v1/analyses/:id`（详情页按秒轮询）
  每一条/每一次都内联整份报告。实测响应（修复前）：
  ```
  {"analyses":[{"id":"…","report_id":"rep-body","report_content":"<html>…"}]}
  ```
- **修复**：`ReportContent` 改 `json:"-"`（Go 字段保留，`/result` 仍以 `report.content` 返回正文）。
  全仓 grep 确认无任何消费者读取该 JSON 字段（前端 `AnalysisSummary`/`AnalysisStateResponse` 均未声明）。
- **回归测试**：`platform/internal/api/v1/result_test.go::TestContract_analyses_lifecycleOmitsReportBody`
  （先 RED，失败输出即上面那段内联正文；同时断言 `/result` 仍返回正文）。

### D5 `_llm_insight` 静默吞异常，生产无法定位「AI 研判不可用」

- **文件**：`engines/report_engine/main.py:126-127`
- **问题**：`except Exception: return None` 连日志都没有。生产上用户只看到「AI 研判不可用（未配置或调用失败）」，
  运维无法区分「key 未配置」「DeepSeek 401」「响应超时」「JSON 非法」。
  对比 `insight_engine` 至少把原因包进 502 的 detail。
- **建议**：加 `logging.getLogger(__name__).warning("report: LLM insight failed", exc_info=True)`。

### D6 LLM 调用未设 `max_tokens`，文档数无上限 → 输出 JSON 可能被截断

- **文件**：`engines/insight_engine/main.py:89-129`（`/analyze` 把最多 50 篇 × 800 字符塞进一次调用）
- **问题**：prompt 要求 `sentiments` 覆盖**每一篇**文档；50 篇时输出 JSON 可能触及模型默认上限被截断
  → `_parse_json_content` 抛 `ValueError` → 502 → 整个洞察降级。
  当前 `_doc_briefs` 只截断单篇长度（`MAX_CONTENT_CHARS=800`），没有文档条数上限，也没有 `max_tokens`。
- **建议**：显式设置 `max_tokens`；对 `documents` 条数设上限（如 30）并在超限时截断 +
  prompt 中说明「已抽样」，让模型不要在输出里补全被省略的文档。

---

## 7. 🟢 NIT

| # | 位置 | 说明 |
|---|------|------|
| N1 | `analysis/pipeline.go:204,223,241-256` | `analysisType()` / `analysisName()` 各多一次 `svc.Get`（`Handle` 开头已取过 `a`），可一次读取后传入，省两次 store 往返 |
| N2 | `engine/insight.go:33-35`、`engine/report.go:32-34` | transport 就地改写入参 `req.APIKey`；当前每次调用都新建 req 所以安全，但复用同一 req 对象会成竞态。建议写局部副本 |
| N3 | `analyze` 空文档早返回 | `/analyze` 空文档返回零值且**不产生 warning**（`runInsight` 拿到的是「成功」）→ 采集到 0 篇时用户看到「无摘要」但没有任何解释。可考虑在管线侧对 `len(docs)==0` 直接给 warning |
| N4 | `analysis/insight.go:11-15` | `Sentiment` 无 `Emotions`，而 insight prompt 要求 LLM 输出 `emotions` → 字段白算。要么前端展示，要么从 prompt 里去掉省 token |
| N5 | `analysis/insight.go:19-25` | `Topic.Keywords` 带 `omitempty`，空关键词时字段从 JSON 消失；前端 `Topic` 类型也未声明该字段。当前不崩，但契约不稳定 |
| N6 | `AnalysisDetailPage.tsx:445,748` | 话题表 `rowKey="name"`、情感明细表 `rowKey="document_id"`：重名话题/空 document_id 会产生重复 React key |
| N7 | `engines/insight_engine/main.py:143-169` | `/sentiment` 复用了情感+话题的联合 prompt（`_SENTIMENT_TOPIC_PROMPT`），会顺带让 LLM 产出 topics（浪费 token），且返回体只用 `sentiments`。可拆一个纯情感 prompt |
| N8 | `scripts/deploy.sh:280` | 只把 `BOCHA_API_KEY` 写进 `engines.env`，没有 `DEEPSEEK_API_KEY`。当前靠平台逐请求注入 key（可接受），但引擎 `/health` 的 `llm` 字段会**永远为 false**，排查时误导 |
| N9 | `api/v1/admin.go:174-181` | `GET /admin/settings` 把密钥**明文**回传浏览器（前端用 `maskKey` 遮挡，但网络面板可见）。Bocha 已如此，本轮把第二个密钥纳入同一暴露面 —— 建议后续改为只回 `*_configured: true` |
| N10 | `web/e2e/production.spec.ts:337` | `expect(body).not.toContain('失败')` 过于宽泛：采集到的文档标题/正文里出现「失败」即误报（中文舆情语料里这个词很常见）。建议断言状态徽标文案而非整页 innerText |
| N11 | `api/v1/contract_test.go:1332` | `contractGap.status` 注释列出 `STUB\|MISMATCH\|SECURITY\|MISSING_FIELDS\|PENDING`，但历史条目用了 `PARTIAL`；测试只校验 severity，不校验 status |
| N12 | `engines/common/llm_client.py:18-21` | `chat()` 每次调用新建 `httpx.AsyncClient`，无连接复用（一个任务 2-3 次调用，可接受）。另 `DEEPSEEK_BASE_URL/MODEL` 在 import 期读环境变量，测试无法通过 `monkeypatch.setenv` 覆盖 |
| N13 | `api/v1/analyses.go:145` | 降级时把上游引擎错误原文（含内网 URL/host）经 `warning` 展示给租户。已实测**不泄漏 API key**（FastAPI 422 只回显出错字段的取值），但建议只暴露「哪一步降级」而非完整 error 串 |
| N14 | 仓库级 | `gofmt -l ./internal/` 列出 20 个文件（`middleware.go`/`state.go`/`config.go`/`contract_test.go` 等）在 HEAD 就已不满足 gofmt，因此 CI 目前**无法**以 `gofmt -l` 为门禁。本次未批量重排（避免制造无关 diff），建议后续单独一个「gofmt 全仓」提交，之后再把 `gofmt -l` 加进 `make lint` |

---

## 8. 并发 / 安全专项结论

### 并发（逐项实测）

| 关注点 | 结论 |
|--------|------|
| 内存 store 的 mutate 竞态 | ✅ 无。`memoryStore.mutate` 全程持写锁后才执行 `fn`（`store_memory.go:80-93`）；`put` 写入前做 `cp := *a` 深拷贝结构体 |
| `get`/`list` 的浅拷贝 | ⚠️ 注释写「callers cannot mutate stored state」，但 `cp := *a` 是**浅拷贝**：`Keywords/Sentiments/Topics` 的底层数组与 store 共享。本轮所有写入都是**替换切片头**（`a.Sentiments = r.Sentiments`，且适配器每次都 `make` 新切片），没有任何「就地改元素」的路径，故**当前无竞态**；但若将来有人写 `a.Sentiments[i].Score = …` 就是 RLock 下的写 —— 建议在注释里点明这条约束 |
| `http.Client` 复用 | ✅ 每个引擎实例一个 client 并在请求间复用（`engine/insight.go:29`、`engine/report.go:26`），`*http.Client` 并发安全 |
| pipeline 超时上下文 | 🔴 见 R2（已修）。`fail()` 用独立 5s 上下文写库是正确的（`pipeline.go:270-271`），原 ctx 超时后仍能落状态 |
| 队列消费者并发度 | ⚠️ `memoryQueue.dispatch` 单 goroutine 串行（`pkg/queue/queue.go:107-121`），一个任务阻塞全部任务。R2 把 ceiling 抬到 480s 后这个特性更值得关注（§7 T3） |
| `Queue.Handler` 返回 error 被丢弃 | 🟡 既有行为：`dispatch` 里 `if err := handler(...); err != nil {}` 空分支，注释说明 MVP 直接丢弃（无重试/DLQ） |

### 安全

| 关注点 | 结论 |
|--------|------|
| 凭据泄漏进提交 | ✅ `git diff 898e8a4..20feda0 \| grep "sk-[A-Za-z0-9]"` 只命中测试里的 `sk-x` / `sk-test` 占位符（虚构值，可接受）。**无真实 key** |
| key 只出现在环境变量/平台 settings | ✅ `container.go:70-73` 从 `os.Getenv("DEEPSEEK_API_KEY")` 种子；`keyFor("deepseek_api_key")` 闭包注入 transport；Admin UI 可在线覆盖（`PUT /admin/settings`）。提交内无硬编码凭据 |
| key 进日志 | ✅ 未发现。`keyFor` 的返回值只进请求体；transport 的 error 拼的是**响应体**（`engine/insight.go:92`、`engine/report.go:60`），实测引擎端错误 detail 不含 key |
| key 出现在错误信息 | ✅ 实测：FastAPI 422 只回显出错的单个字段（`{"loc":["body","documents"],"input":"not-a-list"}`），不回显 `api_key`。参见 N13 的边界提示 |
| HTML 报告 XSS | 🔴 见 R1（已修）。现在全部转义 + `javascript:` 伪协议退化为 `#` |
| 报告在 iframe 中的沙箱 | ✅ `AnalysisDetailPage.tsx:514` 用 `sandbox=""`（最严格：无脚本、无同源、无表单、无弹窗），当前是本轮唯一的脚本执行防线 |
| iframe `sandbox=""` 的语义 | ✅ 已确认：空值 = 空令牌集 = 施加全部限制（非「不沙箱」）。React 以 `sandbox=""` 渲染该属性 |
| 提示词注入 | ⚠️ 固有风险（被爬正文进 prompt）。本轮修掉了「注入内容变成可执行标记」，但「注入内容变成伪造结论」仍需人工复核机制 —— 记录为长期项 |

---

## 9. 修复清单（本轮改动）

| 文件 | 改动 |
|------|------|
| `engines/report_engine/main.py` | 新增 `_esc` / `_safe_url` / `_str_list` / `_topic_analyses` / `_topic_rows` / `_document_rows`；`_render` 全量转义；`_llm_insight` 增加非 dict 兜底 |
| `engines/tests/test_report.py` | +4 用例（2 注入、2 形状漂移）；`FakeLLM` 增加可选 `payload`（默认行为不变，不改动既有断言） |
| `platform/internal/app/pipeline.go` | 新增 `pipelineBudget(cfg)` |
| `platform/internal/app/container.go` | 改用 `pipelineBudget(cfg)`；移除不再使用的 `time` import |
| `platform/internal/app/pipeline_budget_test.go` | 新增：3 测试 / 8 子用例 |
| `platform/internal/business/analysis/service.go` | `ReportContent` → `json:"-"` |
| `platform/internal/api/v1/result_test.go` | 新增 `TestContract_analyses_lifecycleOmitsReportBody` |
| `platform/internal/api/v1/contract_test.go` | 移除已闭合的 `/analyses/:id/result` gap 条目（sentiments/topics 已接真实引擎） |
| `web/src/pages/AnalysisDetailPage.tsx` | 话题趋势文案改用 `trendArrow()` 归一化结果 |

**未改动的 🟡**：D1 / D2 / D3 / D5 / D6 见 §6；
其中 **D1、D2 建议并入本次提交**（降级信息直接影响用户判断）。

---

## 10. 验证命令与结果

```bash
# Go —— 受影响的重点包
$ cd platform && go test ./internal/engine/ ./internal/business/analysis/ ./internal/api/v1/ -count=1
ok  github.com/yuqing/platform/internal/engine            2.117s
ok  github.com/yuqing/platform/internal/business/analysis 1.432s
ok  github.com/yuqing/platform/internal/api/v1            1.869s

# Go —— 全量（未带 -race，按审核要求）
$ go test ./... -count=1          # exit 0，无失败包
$ go vet ./internal/...           # 无输出

# Python
$ cd engines && python -m pytest tests/ -q
27 passed, 1 warning in 1.68s      # 修复前 23 passed

# 前端类型检查
$ cd web && npx tsc -b             # exit 0

# 注入向量独立复测（修复后）
closing </title><script> injected : False
raw img tag injected        : False
javascript: href            : False
escaped script present      : True
href degraded to #          : True
```

> 说明：未运行 `-race` 全量（按审核要求）；§8 的并发结论基于加锁路径逐一核对，
> 如需 CI 固化建议在 `make test` 全量跑一次 `-race`。

---

## 11. 后续待办

| # | 事项 | 优先级 |
|---|------|--------|
| T1 | D1：降级时不渲染 0/0/0 情感统计 | 高（建议并入本提交） |
| T2 | D2：warning 改追加语义，保留多个降级原因 | 高（建议并入本提交） |
| T3 | 给管线独立配置项 `pipeline.timeout` 并把 `memoryQueue` 消费者改为并发 | 中 |
| T4 | D6：给 LLM 调用设 `max_tokens` + 文档条数上限 | 中 |
| T5 | D3：落库失败返回 warning，与 `runReport` 契约一致 | 中 |
| T6 | D5：报告引擎 LLM 失败补日志 | 中 |
| T7 | 生产 `config.yaml` 核对：确认 `engines.insight.url`/`engines.report.url` 已配置、且 `DEEPSEEK_API_KEY` 已写入平台 settings | 部署前必做 |
| T8 | 报告正文已在 iframe 内受 `sandbox=""` 保护；若后续增加「新窗口打开报告」按钮，必须同步评估存储型 XSS | 长期 |
