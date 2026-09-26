# ForumEngine 架构设计文档

**项目**: 多 Agent 辩论系统（ForumEngine）  
**版本**: v1.0  
**日期**: 2026-09-25  
**状态**: CEO 已批准，立即实施  
**标准**: ⚠️ 生产环境标准（禁止 Mock 数据）

---

## 目录

1. [系统架构](#系统架构)
2. [数据库设计](#数据库设计)
3. [LLM 调用流程](#llm-调用流程)
4. [降级方案](#降级方案)
5. [API 接口设计](#api-接口设计)
6. [安全与监控](#安全与监控)

---

## 系统架构

### 全局架构图

```
┌─────────────────────────────────────────────────────────────┐
│  Go Platform (yuqing-server, port 8080)                     │
├─────────────────────────────────────────────────────────────┤
│  POST /api/v1/analyses                                       │
│    ↓                                                         │
│  analysis.Pipeline.Handle()                                  │
│    ├─ acquiring_budget (10%)                                │
│    ├─ fetching (25%)                                        │
│    ├─ analyzing (60%) → InsightAnalyzer                     │
│    ├─ debating (75%) → 🔥 ForumDebater (新增)              │
│    ├─ generating_report (85%)                               │
│    └─ completed (100%)                                       │
│                                                              │
│  新增接口：                                                   │
│    GET /api/v1/analyses/:id/debate                          │
│    - 返回完整辩论记录                                         │
│    - 包含 rounds/verdict/confidence                          │
└─────────────┬────────────────────────────────────────────────┘
              │ HTTP POST
              ▼
┌─────────────────────────────────────────────────────────────┐
│  ForumEngine (Python FastAPI, port 8004)                    │
├─────────────────────────────────────────────────────────────┤
│  POST /debate                                                │
│    ↓                                                         │
│  DebateOrchestrator.run_debate()                            │
│    │                                                         │
│    ├─ Round 1 (10-15s)                                      │
│    │   ├─ Moderator.set_agenda() → LLM call (512 tokens)   │
│    │   └─ 4 × Agent.statement() → LLM call (1024 tokens)   │
│    │      [并发执行]                                         │
│    │                                                         │
│    ├─ detect_similarity(round1)                             │
│    │   └─ if > 0.7: retry with higher temperature          │
│    │                                                         │
│    ├─ Round 2 (10-15s)                                      │
│    │   ├─ Moderator.question() → LLM call                  │
│    │   └─ 4 × Agent.response() → LLM call                  │
│    │      [并发执行]                                         │
│    │                                                         │
│    ├─ Round 3 (8-12s)                                       │
│    │   ├─ Moderator.summarize() → LLM call                 │
│    │   └─ 4 × Agent.consensus() → LLM call                 │
│    │      [串行执行：处置建议官最后发言]                     │
│    │                                                         │
│    └─ 返回: {rounds[], verdict, confidence}                 │
│                                                              │
│  健康检查：GET /health                                       │
│  指标采集：GET /metrics (Prometheus)                         │
└─────────────┬────────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│  LLM Provider (GLM-4-flash / DeepSeek)                      │
│    - 端点：可配置（环境变量 LLM_BASE_URL）                   │
│    - 模型：glm-4-flash（默认）/ deepseek-chat（备用）       │
│    - 超时：30s/次调用，90s/整体辩论                          │
│    - 重试：瞬时错误重试 1 次，429 限流等待 5s 重试           │
└─────────────────────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│  PostgreSQL (yuqing_platform)                               │
│    - debates 表：辩论记录（完整 JSON）                       │
│    - debate_metrics 表：性能指标（耗时/成本/相似度）         │
│    - llm_call_logs 表：LLM 调用日志（审计 + 成本分析）       │
└─────────────────────────────────────────────────────────────┘
```

### 组件职责

| 组件 | 职责 | 语言 | 位置 |
|------|------|------|------|
| **Go Pipeline** | 分析管线协调，在 debating 步骤调用 ForumEngine | Go | `platform/internal/business/analysis/pipeline.go` |
| **ForumDebater** | Go → Python HTTP 适配器 | Go | `platform/internal/engine/forum.go` (新增) |
| **DebateOrchestrator** | 辩论协调器，控制 3 轮流程 | Python | `engines/forum_engine/orchestrator.py` (新增) |
| **AgentManager** | Agent 角色管理 + Prompt 构建 | Python | `engines/forum_engine/agents.py` (新增) |
| **LLMClient** | 异步 LLM 调用 + 并发控制 | Python | `engines/common/llm_client.py` (扩展) |
| **DebateStore** | 辩论记录存储（PG） | Go | `platform/internal/business/debate/store_pg.go` (新增) |

---

## 数据库设计

### 表1: `debates`（辩论记录）

```sql
-- 迁移: platform/migrations/platform/0011_debates.sql
CREATE TABLE debates (
    id TEXT PRIMARY KEY,                     -- ULID
    tenant_id TEXT NOT NULL,
    analysis_id TEXT NOT NULL,               -- 关联的舆情分析
    
    -- 辩论输入
    topic TEXT NOT NULL,                     -- 辩论议题
    input_summary JSONB NOT NULL,            -- 输入的舆情摘要
    
    -- 辩论输出
    rounds JSONB NOT NULL DEFAULT '[]',      -- 完整辩论记录（数组）
    verdict TEXT NOT NULL DEFAULT '',        -- 最终结论
    confidence FLOAT NOT NULL DEFAULT 0,     -- 可行性评分 (0-1)
    
    -- 元数据
    status TEXT NOT NULL DEFAULT 'pending',  -- pending | running | completed | failed
    error_message TEXT DEFAULT '',           -- 失败原因
    
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    
    -- 索引优化
    CONSTRAINT fk_analysis FOREIGN KEY (analysis_id) REFERENCES analyses(id) ON DELETE CASCADE
);

CREATE INDEX idx_debates_tenant ON debates(tenant_id, created_at DESC);
CREATE INDEX idx_debates_analysis ON debates(analysis_id);
CREATE INDEX idx_debates_status ON debates(tenant_id, status);

COMMENT ON TABLE debates IS '多 Agent 辩论记录';
COMMENT ON COLUMN debates.rounds IS '格式: [{round, agent, agent_role, statement, evidence}]';
COMMENT ON COLUMN debates.input_summary IS '格式: {keywords, sentiment, topics, documents}';
```

**`rounds` 字段结构**：
```json
[
  {
    "round": 1,
    "agent": "事实核查员",
    "agent_role": "证据与数据核验",
    "statement": "核验核心事实：引发事件的视频发布于 5 月 8 日...",
    "evidence": ["太平洋汽车实测数据", "视频原始链接"],
    "llm_call_id": "01JCABC123...",
    "tokens_used": 1024
  },
  // ... 共 12 条记录（4 Agent × 3 Round）
]
```

### 表2: `debate_metrics`（性能指标）

```sql
CREATE TABLE debate_metrics (
    id TEXT PRIMARY KEY,
    debate_id TEXT NOT NULL REFERENCES debates(id) ON DELETE CASCADE,
    tenant_id TEXT NOT NULL,
    
    -- 性能指标
    total_duration_ms INT NOT NULL,          -- 总耗时（毫秒）
    round1_duration_ms INT,
    round2_duration_ms INT,
    round3_duration_ms INT,
    
    -- 成本指标
    total_tokens INT NOT NULL,               -- 总 token 消耗
    total_cost_cny DECIMAL(10,4) NOT NULL,   -- 总成本（元）
    
    -- 质量指标
    similarity_score FLOAT,                  -- Round 1 观点相似度
    retry_count INT DEFAULT 0,               -- 重试次数
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_debate_metrics_debate ON debate_metrics(debate_id);
CREATE INDEX idx_debate_metrics_tenant_date ON debate_metrics(tenant_id, created_at DESC);

COMMENT ON TABLE debate_metrics IS '辩论性能与成本指标（用于运营分析）';
```

### 表3: `llm_call_logs`（LLM 调用日志）

```sql
CREATE TABLE llm_call_logs (
    id TEXT PRIMARY KEY,
    debate_id TEXT REFERENCES debates(id) ON DELETE CASCADE,
    tenant_id TEXT NOT NULL,
    
    -- 调用信息
    agent_name TEXT NOT NULL,                -- "事实核查员" / "主持人"
    round_num INT NOT NULL,                  -- 1/2/3
    prompt TEXT NOT NULL,                    -- 完整 Prompt（审计用）
    
    -- LLM 响应
    model TEXT NOT NULL,                     -- "glm-4-flash"
    temperature FLOAT NOT NULL,
    max_tokens INT NOT NULL,
    response TEXT NOT NULL,                  -- LLM 输出
    
    -- 统计
    prompt_tokens INT NOT NULL,
    completion_tokens INT NOT NULL,
    total_tokens INT NOT NULL,
    cost_cny DECIMAL(10,6) NOT NULL,
    
    -- 性能
    duration_ms INT NOT NULL,
    status TEXT NOT NULL,                    -- success | error | timeout
    error_message TEXT,
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_llm_logs_debate ON llm_call_logs(debate_id);
CREATE INDEX idx_llm_logs_tenant_date ON llm_call_logs(tenant_id, created_at DESC);
CREATE INDEX idx_llm_logs_status ON llm_call_logs(status) WHERE status != 'success';

COMMENT ON TABLE llm_call_logs IS 'LLM 调用日志（审计 + 成本分析 + Debug）';
```

**用途**：
1. **审计**：记录完整 Prompt 和响应，便于排查观点雷同问题
2. **成本分析**：按租户/时间统计 LLM 成本
3. **Debug**：失败调用的错误信息和重试链路

---

## LLM 调用流程

### 流程图

```
┌────────────────────────────────────────────────────────────┐
│ DebateOrchestrator.run_debate()                            │
└────────────┬───────────────────────────────────────────────┘
             │
             ▼
┌────────────────────────────────────────────────────────────┐
│ Round 1: 主持人设定议题                                     │
├────────────────────────────────────────────────────────────┤
│ 1. 构建 Moderator Prompt（从 input_summary 提取）          │
│ 2. LLMClient.chat_async(prompt, temp=0.3, max_tokens=512)  │
│ 3. 记录到 llm_call_logs（agent_name="主持人", round=1）    │
│ 4. 解析议题 → moderator_brief                              │
└────────────┬───────────────────────────────────────────────┘
             │
             ▼
┌────────────────────────────────────────────────────────────┐
│ Round 1: 4 个专家并发陈述                                   │
├────────────────────────────────────────────────────────────┤
│ for agent in [事实核查员, 情绪分析师, 传播专家, 处置建议官]: │
│   1. 构建 Agent Prompt (persona + focus_areas + context)   │
│   2. 并发调用 LLM (temperature=agent.temperature)          │
│   3. 超时保护 30s                                           │
│   4. 记录到 llm_call_logs                                  │
│                                                             │
│ 并发执行 → asyncio.gather(*tasks)                          │
│ 预计耗时: 10-15s（取决于 LLM 延迟）                        │
└────────────┬───────────────────────────────────────────────┘
             │
             ▼
┌────────────────────────────────────────────────────────────┐
│ 观点相似度检测（防雷同）                                     │
├────────────────────────────────────────────────────────────┤
│ 1. 提取 4 个 Agent 的 statement                             │
│ 2. TF-IDF 向量化 + 余弦相似度                               │
│ 3. 计算平均相似度 similarity_score                          │
│ 4. 记录到 debate_metrics                                   │
│                                                             │
│ if similarity_score > 0.7:                                  │
│   logger.warning("观点相似度过高，提高温度重试")             │
│   for agent in agents:                                      │
│       agent.temperature += 0.2                              │
│   重新执行 Round 1（仅重试一次）                            │
│   retry_count += 1                                          │
└────────────┬───────────────────────────────────────────────┘
             │
             ▼
┌────────────────────────────────────────────────────────────┐
│ Round 2: 交叉质询（同 Round 1 流程）                        │
│ Round 3: 共识形成（串行执行）                               │
└────────────┬───────────────────────────────────────────────┘
             │
             ▼
┌────────────────────────────────────────────────────────────┐
│ 返回结果 + 存储                                              │
├────────────────────────────────────────────────────────────┤
│ 1. 整合 rounds[] (12 条记录)                                │
│ 2. 提取 verdict（处置建议官 Round 3 陈述）                  │
│ 3. 计算 confidence（基于专家共识度）                        │
│ 4. 存储到 debates 表                                        │
│ 5. 存储到 debate_metrics 表                                │
│ 6. 返回给 Go Pipeline                                       │
└────────────────────────────────────────────────────────────┘
```

### LLM 调用规范

**超时设置**：
```python
# 单次调用超时
SINGLE_CALL_TIMEOUT = 30  # 秒

# 整体辩论超时
DEBATE_TIMEOUT = 90  # 秒（3 轮 × 30s）

async def chat_async(self, prompt: str, temperature: float, max_tokens: int):
    try:
        response = await asyncio.wait_for(
            self._call_llm(prompt, temperature, max_tokens),
            timeout=SINGLE_CALL_TIMEOUT
        )
        return response
    except asyncio.TimeoutError:
        logger.error(f"LLM call timeout after {SINGLE_CALL_TIMEOUT}s")
        raise
```

**重试策略**：
```python
@retry(
    stop=stop_after_attempt(2),           # 最多重试 1 次（总共 2 次）
    wait=wait_fixed(5),                   # 等待 5s
    retry=retry_if_exception_type((
        httpx.TimeoutException,           # 超时重试
        httpx.HTTPStatusError,            # 429/500/502/503 重试
    )),
    before_sleep=before_sleep_log(logger, logging.WARNING)
)
async def _call_llm(self, prompt, temperature, max_tokens):
    response = await self.client.chat.completions.create(...)
    return response
```

**成本计算**：
```python
def calculate_cost(prompt_tokens: int, completion_tokens: int, model: str) -> Decimal:
    """计算 LLM 调用成本（人民币元）"""
    
    # GLM-4-flash 定价（2026-09-25）
    if model == "glm-4-flash":
        input_price = Decimal("0.001")   # ¥0.001 / 1k tokens
        output_price = Decimal("0.002")  # ¥0.002 / 1k tokens
    elif model == "deepseek-chat":
        input_price = Decimal("0.0014")
        output_price = Decimal("0.0028")
    else:
        input_price = Decimal("0.002")
        output_price = Decimal("0.004")
    
    cost = (
        Decimal(prompt_tokens) / 1000 * input_price +
        Decimal(completion_tokens) / 1000 * output_price
    )
    return cost.quantize(Decimal("0.000001"))  # 保留 6 位小数
```

---

## 降级方案

### 三级降级策略

#### Level 1: 单个 Agent 调用失败

**触发条件**：某个 Agent 的 LLM 调用超时/失败

**处理**：
```python
async def _agent_round(self, round_num, context, agents):
    results = []
    for agent in agents:
        try:
            statement = await self.llm.chat_async(prompt, ...)
            results.append({
                "round": round_num,
                "agent": agent.name,
                "statement": statement,
                "evidence": self._extract_evidence(statement),
            })
        except Exception as e:
            logger.error(f"Agent {agent.name} failed: {e}")
            # 降级：标记失败，继续执行
            results.append({
                "round": round_num,
                "agent": agent.name,
                "statement": f"[技术故障：{agent.name} 暂时无法参与]",
                "evidence": [],
                "error": str(e),
            })
    
    # 只要有 >= 2 个 Agent 成功，辩论继续
    success_count = sum(1 for r in results if "error" not in r)
    if success_count < 2:
        raise DebateFailedError("Too many agents failed")
    
    return results
```

**用户体验**：
- 前端显示："[技术故障：情绪分析师暂时无法参与]"
- 其他 3 个专家继续辩论

#### Level 2: 整体辩论超时

**触发条件**：3 轮辩论总耗时 > 90s

**处理**：
```python
async def run_debate_with_timeout(self, topic, summary):
    try:
        return await asyncio.wait_for(
            self.run_debate(topic, summary),
            timeout=90.0
        )
    except asyncio.TimeoutError:
        logger.error("Debate timeout after 90s")
        # 返回已完成的轮次
        return {
            "rounds": self.history,  # 已完成的 Round 1/2
            "verdict": "辩论未完成（超时），建议稍后重试。",
            "confidence": 0.5,
            "status": "timeout",
        }
```

**用户体验**：
- 前端显示："辩论进行中，已完成 2 轮，请稍后查看完整结果"
- 提供"重新生成"按钮

#### Level 3: LLM 服务完全不可用

**触发条件**：
- LLM API 持续返回 500/503
- 网络不可达
- API Key 失效

**处理**：
```python
@app.post("/debate")
async def debate_forum(req: ForumRequest):
    try:
        orchestrator = DebateOrchestrator(llm_client, AGENTS)
        result = await orchestrator.run_debate(req.topic, req.summary)
        return ForumResponse(**result)
    
    except LLMServiceUnavailable as e:
        logger.error(f"LLM service unavailable: {e}")
        
        # ⚠️ CEO要求：不返回Mock数据
        # 改为：返回错误，前端显示"服务暂时不可用"
        raise HTTPException(
            status_code=503,
            detail={
                "error": "DEBATE_SERVICE_UNAVAILABLE",
                "message": "专家辩论服务暂时不可用，请稍后重试",
                "retry_after": 60,  # 建议 60s 后重试
            }
        )
```

**Go 层处理**：
```go
// engine/forum.go
func (e *HTTPForumEngine) Debate(ctx context.Context, req DebateRequest) (*DebateResponse, error) {
    resp, err := e.client.Post(ctx, "/debate", req)
    if err != nil {
        return nil, err
    }
    
    if resp.StatusCode == 503 {
        // 服务不可用，跳过 debating 步骤，继续后续流程
        logger.Warn("forum engine unavailable, skipping debate step")
        return nil, ErrServiceUnavailable
    }
    
    // ... 正常处理
}

// pipeline.go (debating 步骤)
debateResp, err := p.forumEng.Debate(ctx, debateReq)
if err == engine.ErrServiceUnavailable {
    // 辩论服务不可用，记录 warning，继续生成报告
    logger.Warn("debate skipped due to service unavailable", "analysis_id", analysisID)
    // 不影响任务状态，仍然 completed
} else if err != nil {
    // 其他错误，标记为 warning（不致命）
    logger.Error("debate failed", "analysis_id", analysisID, "err", err)
}
```

**用户体验**：
- 分析任务正常完成（completed）
- 辩论 Tab 显示："专家辩论服务暂时不可用，已为您保留完整舆情分析结果"
- 不阻塞用户查看其他分析结果（情感/话题/报告）

---

## API 接口设计

### 1. ForumEngine API（Python）

#### POST /debate

**请求**：
```json
{
  "topic": "雅阁后排空间舆情应对",
  "analysis_id": "01JCABCD...",
  "input_summary": {
    "keywords": ["雅阁", "后排空间"],
    "sentiment": {"positive": 22, "negative": 78, "neutral": 0},
    "topics": [
      {"name": "后排空间", "count": 128, "trend": "rising"}
    ],
    "documents": [
      {"title": "...", "content": "...", "source_type": "weibo"}
    ]
  },
  "max_rounds": 3,
  "tenant_id": "01JCTENANT..."
}
```

**响应**（200 OK）：
```json
{
  "debate_id": "01JCDEBATE...",
  "rounds": [
    {
      "round": 1,
      "agent": "事实核查员",
      "agent_role": "证据与数据核验",
      "statement": "核验核心事实：引发事件的视频发布于 5 月 8 日...",
      "evidence": ["太平洋汽车实测数据（2026 年 3 月）"],
      "tokens_used": 1024
    }
    // ... 共 12 条
  ],
  "verdict": "形成最终方案：① 24h 内官方号发布自黑式图文...",
  "confidence": 0.85,
  "metrics": {
    "total_duration_ms": 42000,
    "total_tokens": 13824,
    "total_cost_cny": 0.021,
    "similarity_score": 0.52,
    "retry_count": 0
  }
}
```

**错误响应**（503 Service Unavailable）：
```json
{
  "error": "DEBATE_SERVICE_UNAVAILABLE",
  "message": "专家辩论服务暂时不可用，请稍后重试",
  "retry_after": 60
}
```

#### GET /health

**响应**：
```json
{
  "status": "ok",
  "engine": "forum",
  "llm_provider": "glm-4-flash",
  "llm_available": true,
  "version": "0.2.0"
}
```

#### GET /metrics (Prometheus)

**暴露指标**：
```
# 辩论总数
forum_debates_total{status="completed"} 123
forum_debates_total{status="failed"} 5

# 耗时分布（直方图）
forum_debate_duration_seconds_bucket{le="30"} 50
forum_debate_duration_seconds_bucket{le="60"} 120
forum_debate_duration_seconds_bucket{le="90"} 128

# LLM 调用
forum_llm_calls_total{agent="事实核查员",status="success"} 456
forum_llm_calls_total{agent="情绪分析师",status="timeout"} 3

# 观点相似度
forum_similarity_score_avg 0.54
```

### 2. Go Platform API（新增）

#### GET /api/v1/analyses/:id/debate

**响应**（200 OK）：
```json
{
  "debate_id": "01JCDEBATE...",
  "analysis_id": "01JCANALYSIS...",
  "topic": "雅阁后排空间舆情应对",
  "rounds": [...],  // 同 ForumEngine 响应
  "verdict": "...",
  "confidence": 0.85,
  "status": "completed",
  "created_at": "2026-09-25T10:00:00Z",
  "completed_at": "2026-09-25T10:00:45Z"
}
```

**错误响应**（404 Not Found）：
```json
{
  "code": "NOT_FOUND",
  "message": "该分析任务未生成专家辩论",
  "details": "辩论服务在分析时不可用，或套餐不包含辩论功能"
}
```

---

## 安全与监控

### 安全措施

1. **API Key 保护**：
   - LLM API Key 存储在环境变量或 `platform_settings` 表
   - 绝不记录到日志文件
   - `llm_call_logs` 表只记录 Prompt/Response，不记录 API Key

2. **Prompt 注入防护**：
   ```python
   def sanitize_user_input(text: str) -> str:
       """清理用户输入，防止 Prompt 注入"""
       # 移除可能的注入指令
       forbidden = [
           "ignore previous instructions",
           "disregard above",
           "你现在是",
           "forget everything",
       ]
       text_lower = text.lower()
       for pattern in forbidden:
           if pattern in text_lower:
               logger.warning(f"Potential prompt injection detected: {text[:50]}")
               text = text.replace(pattern, "[已过滤]")
       
       # 截断过长输入
       if len(text) > 10000:
           text = text[:10000] + "..."
       
       return text
   ```

3. **租户隔离**：
   - 所有数据库表带 `tenant_id` 列
   - API 层经 `middleware.TenantContext()` 校验
   - 辩论记录只能由创建者租户访问

4. **速率限制**：
   ```go
   // api/v1/analyses.go
   // 辩论功能限流：每租户 10 次/小时
   r.GET("/analyses/:id/debate", 
       middleware.RateLimit("debate", 10, time.Hour),
       s.getDebate,
   )
   ```

### 监控指标

1. **性能监控**：
   - Grafana 面板：辩论耗时分布（P50/P95/P99）
   - Alert：P95 > 60s 告警

2. **成本监控**：
   - 每日 LLM 成本汇总（按租户）
   - Alert：日成本 > ¥100 告警

3. **质量监控**：
   - 观点相似度平均值（目标 < 0.6）
   - Alert：相似度 > 0.7 频率 > 20% 告警

4. **可用性监控**：
   - LLM 调用成功率（目标 > 95%）
   - Alert：成功率 < 90% 告警

---

**文档版本**: v1.0  
**状态**: 已评审，立即实施  
**下一步**: 开发任务分解（见 `DEVELOPMENT_TASKS.md`）
