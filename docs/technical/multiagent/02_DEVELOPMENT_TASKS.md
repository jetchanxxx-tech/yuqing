# ForumEngine 开发任务分解

**项目**: 多 Agent 辩论系统  
**版本**: v1.0  
**日期**: 2026-09-25  
**总工期**: 10 个工作日  
**标准**: ⚠️ 生产环境标准（禁止 Mock 数据）

---

## 资源分配

| 角色 | 姓名 | 工作量 | 备注 |
|------|------|--------|------|
| **后端工程师** | 待分配 | 10 天全职 | Python + Go 双栈 |
| **前端工程师** | 待分配 | 1.5 天 | React + TypeScript |
| **架构师** | 技术总监 | 0.5 天评审 | Day 2 评审 Prompt |
| **测试工程师** | 后端兼任 | 1 天 | Day 9 集成测试 |
| **运维工程师** | 待协调 | 0.5 天 | Day 10 部署支持 |

---

## 开发任务清单

### Phase 1: Prompt 工程（Day 1-2）⚠️ 关键

#### Task 1.1: Agent 角色定义
**责任人**: 后端工程师  
**工时**: 0.5 天  
**交付物**: `engines/forum_engine/agents.py`

```python
# agents.py
from dataclasses import dataclass
from typing import List

@dataclass
class AgentRole:
    name: str
    role: str
    persona: str              # 200-300 字人设
    focus_areas: List[str]    # 3-5 个关注维度
    temperature: float        # 0.2-0.5
    
    def get_system_prompt(self) -> str:
        """生成系统 Prompt 前缀"""
        return f"""你是{self.name}，角色定位：{self.role}。

**你的人设**：
{self.persona}

**你的关注维度**：
{", ".join(self.focus_areas)}

⚠️ 重要约束：
1. 你只需要从你的专业角度分析，不要试图覆盖其他专家的职责
2. 你的观点必须基于证据，引用具体文档或数据
3. 保持你的独特视角，避免与其他专家雷同
"""

# 定义 4 个专家
AGENTS = [
    AgentRole(
        name="事实核查员",
        role="证据与数据核验",
        persona="""你是资深调查记者，有 15 年事实核查经验。
你相信「证据先行」原则，对任何未经证实的传言保持怀疑态度。
你的职责是确保辩论建立在可靠证据之上，而非猜测和传言。
你擅长：时间线还原、数据真实性验证、信息源溯源、区分事实与观点。""",
        focus_areas=["事件时间线核查", "数据真实性验证", "信息源可信度评估", "事实与观点分离"],
        temperature=0.2,
    ),
    AgentRole(
        name="情绪分析师",
        role="公众情绪与心理分析",
        persona="""你是心理学博士 + 传播学专家，擅长解读群体情绪动态。
你认为「情绪比事实更重要」——舆情的破坏力取决于情绪烈度，而非事实真伪。
你能准确区分：真实愤怒、戏谑调侃、从众跟风、理性讨论等不同情绪类型。
你关注公众心理动机，预测情绪演化趋势。""",
        focus_areas=["情感细分（真怒 vs 调侃 vs 跟风）", "群体心理分析", "情绪演化趋势预测", "舆情破坏力评估"],
        temperature=0.5,
    ),
    AgentRole(
        name="传播路径专家",
        role="渠道与扩散机制分析",
        persona="""你是社交媒体分析专家，精通各平台传播机制（抖音/微博/B站/小红书）。
你关注「谁在说、在哪说、怎么传」——追踪信息从源头到扩散的完整链条。
你能识别关键传播节点（KOL/媒体/二创者），预判传播趋势。
你还关注竞品动态，警惕竞品借势。""",
        focus_areas=["传播链条追踪", "平台特性差异", "二次创作监测", "竞品动态预警"],
        temperature=0.3,
    ),
    AgentRole(
        name="处置建议官",
        role="公关策略与行动方案",
        persona="""你是危机公关顾问，有 20+ 品牌危机处置经验。
你必须基于前三位专家的证据和分析，给出可执行方案。
你的方案包含：时间节点、具体动作、资源投入、预期效果。
你相信「快速行动胜过完美方案」，同时警惕过度反应。""",
        focus_areas=["策略类型选择（辟谣/自黑/沉默/反击）", "时间窗口把握", "资源投入评估", "预期效果量化"],
        temperature=0.4,
    ),
]
```

**验收标准**：
- ✅ 4 个 Agent 定义完整
- ✅ 人设字数 200-300 字
- ✅ 每个 Agent 有 4-5 个 focus_areas
- ✅ Temperature 参数差异化（0.2-0.5）

---

#### Task 1.2: Prompt 模板库
**责任人**: 后端工程师  
**工时**: 1 天  
**交付物**: `engines/forum_engine/prompts.py`

**模板清单**（17 个）：

| 模板 ID | 名称 | 用途 | 长度 |
|---------|------|------|------|
| `MODERATOR_ROUND1` | 主持人议题设定 | Round 1 开场 | ~300 字 |
| `MODERATOR_ROUND2` | 主持人质询引导 | Round 2 开场 | ~250 字 |
| `MODERATOR_ROUND3` | 主持人共识推动 | Round 3 开场 | ~250 字 |
| `AGENT_ROUND1_TEMPLATE` | Agent Round 1 模板 | 各专家陈述 | ~400 字 |
| `AGENT_ROUND2_TEMPLATE` | Agent Round 2 模板 | 交叉质询 | ~450 字 |
| `AGENT_ROUND3_TEMPLATE` | Agent Round 3 模板 | 共识形成 | ~400 字 |

**示例（完整版）**：

```python
# prompts.py

MODERATOR_ROUND1 = """你是舆情辩论主持人，负责设定清晰的辩论议题并分配专家发言方向。

**舆情摘要**：
- 关键词：{keywords}
- 情感分布：正面 {positive}% / 负面 {negative}% / 中性 {neutral}%
- 核心话题：{topics}
- 文档数量：{doc_count} 条
- 时间跨度：{time_span}
- 争议焦点：{controversies}

**你的任务**：
1. 提炼核心议题（1 句话，清晰具体，避免泛泛而谈）
2. 为 4 位专家分配发言方向（每位专家关注不同维度，避免重复）

**输出格式**（纯文本，不要 JSON）：
议题：[用 1 句话描述本次辩论的核心问题，例如："如何在 48 小时内应对雅阁后排空间的网络玩梗潮"]

专家发言分配：
- 事实核查员：请核验 [具体事实/数据/时间线，例如："核验视频发布时间、空间数据真实性"]
- 情绪分析师：请分析 [公众情绪的哪个维度，例如："区分真实不满与戏谑调侃的占比"]
- 传播路径专家：请追踪 [传播链条的哪个环节，例如："追踪从抖音到微博的扩散路径"]
- 处置建议官：请给出 [初步策略方向，例如："初步评估辟谣 vs 自黑式回应的适用性"]

注意：
- 议题要具体到行动，不要"如何应对 XX 舆情"这种空话
- 每位专家的方向要有明确差异，不要让他们做同样的事
- 如果某个维度证据不足，明确告知专家"如果证据不足，请说明"
"""

AGENT_ROUND1_TEMPLATE = """你是 {agent_name}，角色定位：{agent_role}。

**你的人设**：
{persona}

**你的关注维度**：
{focus_areas}

**当前轮次**：Round 1（第一轮陈述）

**主持人指示**：
{moderator_brief}

**可用证据**：
- 舆情摘要：{keywords} / 情感分布 正面{positive}% 负面{negative}% 中性{neutral}%
- 核心话题：{topics}
- 文档数量：{doc_count} 条
- 典型文档片段（前 5 条）：
{document_samples}

**你的任务**（Round 1 陈述）：
1. 从你的专业角度，陈述核心观点（2-3 句话）
2. 必须引用具体证据（来自文档或数据，不要编造）
3. 给出初步判断

⚠️ 重要约束：
- 你只需要关注{focus_areas_emphasis}，不要关注其他专家的职责
- 你的陈述必须有具体证据支撑，不要空泛地说"建议加强宣传"
- 保持你的独特视角，避免与其他专家雷同

**输出格式**（纯文本，2-3 句话）：
[你的核心观点陈述，必须引用具体证据，例如：
"根据 5 月 8 日发布的视频实测数据（来源：太平洋汽车），雅阁后排空间确实在同级车中处于中游偏下（腿部空间 920mm vs 凯美瑞 950mm），但网络传播存在夸大（「无法坐人」说法不实）。建议处置策略基于「部分事实+夸大传播」这一判断。"]
"""

AGENT_ROUND2_TEMPLATE = """你是 {agent_name}，角色定位：{agent_role}。

**当前轮次**：Round 2（交叉质询）

**主持人质询点**：
{moderator_questions}

**Round 1 其他专家陈述摘要**：
{round1_summary}

**你的任务**（Round 2 质询/补充）：
1. 针对其他专家的观点，提出质疑或补充
2. 或者支持某个观点并提供额外证据
3. 或者指出观点之间的矛盾

⚠️ 要求：
- 必须针对具体专家的具体观点，不要泛泛而谈
- 避免重复 Round 1 的内容
- 必须提出至少 1 个新观点或质疑

**输出格式**（2-3 句话）：
[针对 XX 专家的观点，我认为... / 我补充... / 我质疑...]
"""

AGENT_ROUND3_TEMPLATE = """你是 {agent_name}，角色定位：{agent_role}。

**当前轮次**：Round 3（共识形成）

**主持人共识总结**：
{moderator_summary}

**你的任务**（Round 3 最终陈述）：
{task_description}

⚠️ 要求：
- 如果你是处置建议官，给出最终方案（包含时间节点、具体动作、预期效果）
- 如果你是其他专家，表态支持或提出最后的保留意见
- 方案必须可执行，不要空话套话

**输出格式**：
{output_format}
"""
```

**验收标准**：
- ✅ 17 个模板全部完成
- ✅ 每个模板包含完整的输入变量说明
- ✅ 包含"⚠️ 要求"约束条件
- ✅ 包含输出格式示例

---

#### Task 1.3: Prompt 测试与优化
**责任人**: 后端工程师 + 架构师  
**工时**: 0.5 天  
**交付物**: `engines/tests/test_prompts.py` + 测试报告

**测试用例**（10 个真实舆情）：

| ID | 话题 | 情感分布 | 预期观点差异点 |
|----|------|---------|---------------|
| TC-1 | 雅阁后排空间 | 负 78% | 事实核查 vs 情绪分析（玩梗 vs 真怒） |
| TC-2 | 特斯拉降价 | 负 65% | 传播路径（老车主愤怒）vs 处置建议（补偿方案） |
| TC-3 | 喜茶降价 | 正 45% / 负 35% | 情绪分析（分化）vs 策略（品牌定位） |
| TC-4 | 某品牌食品安全 | 负 92% | 事实核查（真伪）vs 处置（危机公关） |
| TC-5 | 明星代言翻车 | 负 85% | 传播路径（粉丝 vs 路人）vs 策略（切割 vs 维护） |
| TC-6 | 产品功能虚假宣传 | 负 88% | 事实核查（证据链）vs 情绪（信任崩塌） |
| TC-7 | 企业公益作秀 | 中性 55% / 负 30% | 情绪（真伪判断）vs 传播（舆论反转） |
| TC-8 | 竞品恶意营销 | 负 70% | 传播（黑公关）vs 策略（反击 vs 沉默） |
| TC-9 | 客服态度恶劣 | 负 80% | 事实（录音证据）vs 情绪（用户愤怒） |
| TC-10 | 产品价格争议 | 正 40% / 负 40% | 情绪（分化）vs 策略（定价调整） |

**测试脚本**：
```python
# tests/test_prompts.py
import pytest
from engines.forum_engine.orchestrator import DebateOrchestrator
from engines.forum_engine.agents import AGENTS
from engines.common.llm_client import LLMClient
from engines.forum_engine.similarity import detect_similarity

@pytest.mark.parametrize("test_case", TEST_CASES)
async def test_prompt_quality(test_case):
    """测试 Prompt 生成的观点差异化"""
    
    llm = LLMClient(
        api_key=os.getenv("LLM_API_KEY"),
        model="glm-4-flash"
    )
    orchestrator = DebateOrchestrator(llm, AGENTS)
    
    # 只测试 Round 1（最关键）
    moderator_brief = await orchestrator._moderator_round1(
        test_case["topic"],
        test_case["summary"]
    )
    round1 = await orchestrator._agent_round(1, moderator_brief, test_case["summary"])
    
    # 提取陈述
    statements = [r["statement"] for r in round1]
    
    # 计算相似度
    similarity = detect_similarity(statements)
    
    # 断言
    assert similarity < 0.6, f"观点相似度过高: {similarity:.2f}"
    assert len(round1) == 4, "应该有 4 个专家陈述"
    assert all(len(s["statement"]) > 50 for s in round1), "陈述长度不足"
    
    print(f"\n=== Test Case: {test_case['id']} ===")
    print(f"相似度: {similarity:.2f}")
    for r in round1:
        print(f"\n{r['agent']}: {r['statement'][:100]}...")
```

**验收标准**：
- ✅ 10 个测试用例全部通过
- ✅ 相似度平均 < 0.6（最差不超过 0.7）
- ✅ 每个 Agent 陈述长度 > 50 字
- ✅ 每个 Agent 至少引用 1 条证据

**⚠️ 关键决策点（Day 2 EOD）**：
- **如果测试通过**：继续 Phase 2 开发
- **如果测试失败**（相似度 > 0.7）：
  1. 立即上报 CEO
  2. 优化 Prompt（加强对抗性提示）
  3. 重新测试（最多 1 次）
  4. 如仍失败，启动 Plan B（延后上线）

---

### Phase 2: LLM 集成与并发（Day 3-4）

#### Task 2.1: LLM Client 异步调用
**责任人**: 后端工程师  
**工时**: 1 天  
**交付物**: `engines/common/llm_client.py`（扩展）

**新增方法**：
```python
# llm_client.py
import asyncio
import httpx
from typing import List, Tuple
from tenacity import retry, stop_after_attempt, wait_fixed

class LLMClient:
    async def chat_async(
        self,
        prompt: str,
        temperature: float = 0.7,
        max_tokens: int = 1024,
        timeout: float = 30.0
    ) -> str:
        """异步 LLM 调用（支持并发）"""
        try:
            response = await asyncio.wait_for(
                self._call_api(prompt, temperature, max_tokens),
                timeout=timeout
            )
            return response.choices[0].message.content
        except asyncio.TimeoutError:
            logger.error(f"LLM call timeout after {timeout}s")
            raise LLMTimeoutError(f"Timeout after {timeout}s")
        except httpx.HTTPStatusError as e:
            if e.response.status_code == 429:
                logger.warning("Rate limited, will retry")
                raise
            logger.error(f"LLM API error: {e}")
            raise LLMAPIError(str(e))
    
    @retry(
        stop=stop_after_attempt(2),
        wait=wait_fixed(5),
        retry=retry_if_exception_type((httpx.TimeoutException, httpx.HTTPStatusError))
    )
    async def _call_api(self, prompt, temperature, max_tokens):
        """底层 API 调用（带重试）"""
        response = await self.async_client.post(
            f"{self.base_url}/chat/completions",
            json={
                "model": self.model,
                "messages": [{"role": "user", "content": prompt}],
                "temperature": temperature,
                "max_tokens": max_tokens,
            },
            timeout=30.0
        )
        response.raise_for_status()
        return response.json()
    
    async def chat_batch(
        self,
        prompts: List[Tuple[str, float, int]],
        max_concurrency: int = 4
    ) -> List[str]:
        """批量并发调用（限制并发数）"""
        semaphore = asyncio.Semaphore(max_concurrency)
        
        async def _call_with_sem(prompt, temp, max_tok):
            async with semaphore:
                return await self.chat_async(prompt, temp, max_tok)
        
        tasks = [_call_with_sem(p, t, m) for p, t, m in prompts]
        results = await asyncio.gather(*tasks, return_exceptions=True)
        
        # 处理异常
        for i, result in enumerate(results):
            if isinstance(result, Exception):
                logger.error(f"Task {i} failed: {result}")
                results[i] = f"[调用失败: {str(result)}]"
        
        return results
```

**单元测试**：
```python
# tests/test_llm_client.py
@pytest.mark.asyncio
async def test_chat_async():
    client = LLMClient(api_key="sk-test", model="glm-4-flash")
    response = await client.chat_async("测试", temperature=0.5, max_tokens=100)
    assert len(response) > 0

@pytest.mark.asyncio
async def test_chat_batch_concurrency():
    client = LLMClient(api_key="sk-test", model="glm-4-flash")
    prompts = [("测试" + str(i), 0.5, 100) for i in range(4)]
    
    start = time.time()
    results = await client.chat_batch(prompts, max_concurrency=4)
    duration = time.time() - start
    
    assert len(results) == 4
    assert all(len(r) > 0 for r in results)
    assert duration < 15  # 并发应该 < 15s（串行需 40s）
```

**验收标准**：
- ✅ 异步调用正常工作
- ✅ 超时保护生效（30s）
- ✅ 429 限流自动重试
- ✅ 并发调用耗时 < 15s（4 个并发）

---

#### Task 2.2: DebateOrchestrator 核心逻辑
**责任人**: 后端工程师  
**工时**: 1 天  
**交付物**: `engines/forum_engine/orchestrator.py`

**核心类**：
```python
# orchestrator.py
class DebateOrchestrator:
    def __init__(self, llm_client: LLMClient, agents: List[AgentRole]):
        self.llm = llm_client
        self.agents = agents
        self.history: List[Dict] = []
    
    async def run_debate(
        self,
        topic: str,
        analysis_summary: Dict
    ) -> Dict:
        """运行完整辩论（3 轮）"""
        
        start_time = time.time()
        
        try:
            # Round 1
            moderator_brief = await self._moderator_round1(topic, analysis_summary)
            round1 = await self._agent_round(1, moderator_brief, analysis_summary)
            self.history.extend(round1)
            
            # 检测相似度
            similarity = detect_similarity([r["statement"] for r in round1])
            if similarity > 0.7:
                logger.warning(f"Round 1 similarity too high: {similarity:.2f}, retrying")
                # 提高温度重试
                for agent in self.agents:
                    agent.temperature = min(0.9, agent.temperature + 0.2)
                round1 = await self._agent_round(1, moderator_brief, analysis_summary)
                self.history = round1  # 替换
                similarity = detect_similarity([r["statement"] for r in round1])
            
            # Round 2
            moderator_questions = await self._moderator_round2(round1)
            round2 = await self._agent_round(2, moderator_questions, analysis_summary)
            self.history.extend(round2)
            
            # Round 3
            moderator_summary = await self._moderator_round3(round1 + round2)
            round3 = await self._agent_round(3, moderator_summary, analysis_summary)
            self.history.extend(round3)
            
            duration = int((time.time() - start_time) * 1000)
            
            return {
                "rounds": self.history,
                "verdict": self._extract_verdict(round3),
                "confidence": self._calculate_confidence(round3),
                "metrics": {
                    "total_duration_ms": duration,
                    "similarity_score": similarity,
                },
            }
        
        except Exception as e:
            logger.error(f"Debate failed: {e}", exc_info=True)
            raise DebateFailedError(str(e))
    
    async def _agent_round(
        self,
        round_num: int,
        context: str,
        summary: Dict
    ) -> List[Dict]:
        """并发执行 4 个 Agent"""
        
        prompts = []
        for agent in self.agents:
            prompt = self._build_agent_prompt(agent, round_num, context, summary)
            prompts.append((prompt, agent.temperature, 1024))
        
        # 并发调用
        responses = await self.llm.chat_batch(prompts, max_concurrency=4)
        
        # 组装结果
        results = []
        for i, agent in enumerate(self.agents):
            results.append({
                "round": round_num,
                "agent": agent.name,
                "agent_role": agent.role,
                "statement": responses[i],
                "evidence": self._extract_evidence(responses[i], summary["documents"]),
            })
        
        return results
```

**单元测试**：
```python
@pytest.mark.asyncio
async def test_run_debate_full_flow():
    llm = LLMClient(api_key=os.getenv("LLM_API_KEY"), model="glm-4-flash")
    orch = DebateOrchestrator(llm, AGENTS)
    
    summary = {
        "keywords": ["雅阁", "后排空间"],
        "sentiment": {"positive": 22, "negative": 78, "neutral": 0},
        "topics": [{"name": "后排空间", "count": 128}],
        "documents": [...],
    }
    
    result = await orch.run_debate("雅阁后排空间舆情", summary)
    
    assert len(result["rounds"]) >= 12  # 4 Agent × 3 Round
    assert result["verdict"] != ""
    assert 0 <= result["confidence"] <= 1
    assert result["metrics"]["total_duration_ms"] < 60000  # < 60s
```

---

### Phase 3: Go 层集成（Day 5-6）

#### Task 3.1: Engine 契约定义
**责任人**: 后端工程师  
**工时**: 0.5 天  
**交付物**: `platform/internal/engine/forum.go`

```go
// engine/forum.go
package engine

import (
	"context"
	"encoding/json"
)

type ForumEngine interface {
	Debate(ctx context.Context, req DebateRequest) (*DebateResponse, error)
	Health(ctx context.Context) error
}

type DebateRequest struct {
	Topic        string                 `json:"topic"`
	AnalysisID   string                 `json:"analysis_id"`
	InputSummary map[string]interface{} `json:"input_summary"`
	MaxRounds    int                    `json:"max_rounds"`
	TenantID     string                 `json:"tenant_id"`
}

type DebateResponse struct {
	DebateID   string                 `json:"debate_id"`
	Rounds     []DebateRound          `json:"rounds"`
	Verdict    string                 `json:"verdict"`
	Confidence float64                `json:"confidence"`
	Metrics    map[string]interface{} `json:"metrics"`
}

type DebateRound struct {
	Round      int      `json:"round"`
	Agent      string   `json:"agent"`
	AgentRole  string   `json:"agent_role"`
	Statement  string   `json:"statement"`
	Evidence   []string `json:"evidence"`
	TokensUsed int      `json:"tokens_used,omitempty"`
}

// HTTP 实现
type HTTPForumEngine struct {
	baseURL string
	client  *http.Client
}

func NewHTTPForumEngine(baseURL string) *HTTPForumEngine {
	return &HTTPForumEngine{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 120 * time.Second}, // 整体超时 2 分钟
	}
}

func (e *HTTPForumEngine) Debate(ctx context.Context, req DebateRequest) (*DebateResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/debate", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("forum engine request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode == 503 {
		return nil, ErrServiceUnavailable
	}
	
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("forum engine error %d: %s", resp.StatusCode, body)
	}
	
	var result DebateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
```

---

#### Task 3.2: Pipeline 集成
**责任人**: 后端工程师  
**工时**: 0.5 天  
**交付物**: `platform/internal/business/analysis/pipeline.go`（修改）

**集成点**（analyzing 步骤后，generating_report 步骤前）：

```go
// pipeline.go:L120（在 analyzing 完成后）

// debating 步骤（新增）—— 75%
if err := p.advance(ctx, tenantID, analysisID, StateDebating, 75); err != nil {
	return err
}

if p.forumEng != nil {
	debateReq := engine.DebateRequest{
		Topic:        fmt.Sprintf("%s 舆情应对", strings.Join(a.Keywords, "、")),
		AnalysisID:   analysisID,
		InputSummary: buildDebateSummary(a),
		MaxRounds:    3,
		TenantID:     tenantID,
	}
	
	debateResp, err := p.forumEng.Debate(ctx, debateReq)
	if err == engine.ErrServiceUnavailable {
		// 辩论服务不可用，记录 warning，继续后续流程
		p.logger.Warn("debate skipped due to service unavailable", "analysis_id", analysisID)
	} else if err != nil {
		// 其他错误，记录 warning（不致命）
		p.logger.Error("debate failed", "analysis_id", analysisID, "err", err)
	} else {
		// 成功，存储辩论结果
		if err := p.debateStore.Create(ctx, tenantID, &debate.Debate{
			ID:           debateResp.DebateID,
			AnalysisID:   analysisID,
			Topic:        debateReq.Topic,
			InputSummary: debateReq.InputSummary,
			Rounds:       debateResp.Rounds,
			Verdict:      debateResp.Verdict,
			Confidence:   debateResp.Confidence,
			Status:       "completed",
			CreatedBy:    a.CreatedBy,
		}); err != nil {
			p.logger.Error("failed to store debate", "analysis_id", analysisID, "err", err)
		}
	}
}

func buildDebateSummary(a *AnalysisResult) map[string]interface{} {
	return map[string]interface{}{
		"keywords":  a.Keywords,
		"sentiment": a.Sentiment,
		"topics":    a.Topics,
		"documents": a.Documents(),
	}
}
```

---

#### Task 3.3: 数据库迁移 + Store
**责任人**: 后端工程师  
**工时**: 0.5 天  
**交付物**: 
- `platform/migrations/platform/0011_debates.sql`
- `platform/internal/business/debate/store_pg.go`

**迁移脚本**（完整版，见架构文档）

**Store 实现**：
```go
// debate/store_pg.go
package debate

import (
	"context"
	"encoding/json"
	
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

func (s *PGStore) Create(ctx context.Context, tenantID string, d *Debate) error {
	roundsJSON, _ := json.Marshal(d.Rounds)
	summaryJSON, _ := json.Marshal(d.InputSummary)
	
	_, err := s.pool.Exec(ctx, `
		INSERT INTO debates (
			id, tenant_id, analysis_id, topic, input_summary,
			rounds, verdict, confidence, status, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, d.ID, tenantID, d.AnalysisID, d.Topic, summaryJSON,
		roundsJSON, d.Verdict, d.Confidence, d.Status, d.CreatedBy)
	
	return err
}

func (s *PGStore) GetByAnalysisID(ctx context.Context, tenantID, analysisID string) (*Debate, error) {
	var d Debate
	var roundsJSON, summaryJSON []byte
	
	err := s.pool.QueryRow(ctx, `
		SELECT id, analysis_id, topic, input_summary, rounds, verdict, confidence, status, created_at
		FROM debates
		WHERE tenant_id = $1 AND analysis_id = $2
	`, tenantID, analysisID).Scan(
		&d.ID, &d.AnalysisID, &d.Topic, &summaryJSON, &roundsJSON,
		&d.Verdict, &d.Confidence, &d.Status, &d.CreatedAt,
	)
	
	if err != nil {
		return nil, err
	}
	
	json.Unmarshal(roundsJSON, &d.Rounds)
	json.Unmarshal(summaryJSON, &d.InputSummary)
	
	return &d, nil
}
```

---

### Phase 4: 前端展示（Day 8）

#### Task 4.1: 辩论时间线组件
**责任人**: 前端工程师  
**工时**: 1 天  
**交付物**: 
- `web/src/components/DebateTimeline.tsx`
- `web/src/api/debates.ts`

**API 客户端**：
```typescript
// api/debates.ts
import { apiClient } from './client';

export interface Debate {
  debate_id: string;
  analysis_id: string;
  topic: string;
  rounds: DebateRound[];
  verdict: string;
  confidence: number;
  status: string;
  created_at: string;
}

export interface DebateRound {
  round: number;
  agent: string;
  agent_role: string;
  statement: string;
  evidence: string[];
}

export const debatesApi = {
  getByAnalysisId: (analysisId: string) =>
    apiClient.get<Debate>(`/analyses/${analysisId}/debate`),
};
```

**组件**（完整代码见架构文档 Phase 5）

**集成到详情页**：
```tsx
// AnalysisDetailPage.tsx
<Tabs.TabPane tab="专家辩论" key="debate">
  {debate ? (
    <>
      <Alert
        message="最终方案"
        description={debate.verdict}
        type="success"
        showIcon
        style={{ marginBottom: 16 }}
      />
      <Statistic
        title="可行性评分"
        value={debate.confidence}
        precision={2}
        suffix="/ 1.0"
      />
      <DebateTimeline rounds={debate.rounds} />
    </>
  ) : (
    <Empty description="辩论数据生成中或该分析未包含辩论功能" />
  )}
</Tabs.TabPane>
```

---

### Phase 5: 测试与部署（Day 9-10）

#### Task 5.1: 集成测试
**责任人**: 后端工程师  
**工时**: 0.5 天  
**交付物**: `platform/test/integration/debate_test.go`

**测试场景**：
```go
func TestDebateFullFlow(t *testing.T) {
	// 1. 创建分析
	analysisID := createAnalysis(t, tenantID, []string{"雅阁", "后排空间"})
	
	// 2. 等待分析完成（包含 debating 步骤）
	waitForState(t, analysisID, analysis.StateCompleted, 120*time.Second)
	
	// 3. 获取辩论结果
	debate, err := debateStore.GetByAnalysisID(context.Background(), tenantID, analysisID)
	assert.NoError(t, err)
	assert.NotNil(t, debate)
	
	// 4. 验证结构
	assert.Equal(t, 12, len(debate.Rounds), "应该有 12 轮（4×3）")
	assert.NotEmpty(t, debate.Verdict)
	assert.True(t, debate.Confidence > 0 && debate.Confidence <= 1)
	
	// 5. 验证观点差异化
	round1 := filterRounds(debate.Rounds, 1)
	statements := extractStatements(round1)
	similarity := calculateSimilarity(statements)
	assert.Less(t, similarity, 0.7, "观点相似度过高")
}
```

---

#### Task 5.2: 性能测试
**责任人**: 后端工程师  
**工时**: 0.5 天  
**交付物**: 性能测试报告

**测试指标**：
- ✅ 辩论耗时 P50 < 40s
- ✅ 辩论耗时 P95 < 60s
- ✅ 并发 10 个辩论，无超时
- ✅ LLM 调用成功率 > 95%

---

#### Task 5.3: 部署上线
**责任人**: 运维工程师 + 后端工程师  
**工时**: 0.5 天  
**交付物**: 部署文档 + 运行日志

**部署步骤**（详见单独部署文档）：
1. 数据库迁移（`yuqing-cli migrate platform`）
2. Python 依赖安装（无新增）
3. 环境变量配置（`LLM_API_KEY`）
4. 重启服务（`systemctl restart yuqing-server yuqing-forum`）
5. 健康检查（`curl http://127.0.0.1:8004/health`）
6. 冒烟测试（创建 1 个分析任务，验证辩论生成）

---

## 关键里程碑

| 日期 | 里程碑 | 验收标准 | 失败处理 |
|------|--------|---------|---------|
| **Day 2 EOD** | Prompt 工程通过 | 10 个测试用例，相似度 < 0.6 | 上报 CEO，启动 Plan B |
| **Day 4 EOD** | LLM 并发调用通过 | 4 并发耗时 < 15s | 优化并发逻辑 |
| **Day 6 EOD** | 完整辩论跑通 | 3 轮辩论正常返回 | Debug + 延期 1 天 |
| **Day 9 EOD** | 集成测试通过 | 辩论耗时 < 60s，相似度 < 0.7 | 性能优化 |
| **Day 10 EOD** | 生产部署完成 | 冒烟测试通过 | 回滚到 v0.1.3 |

---

**文档版本**: v1.0  
**状态**: 已评审，立即实施  
**下一步**: Prompt 模板详细版（见 `03_PROMPT_TEMPLATES.md`）
