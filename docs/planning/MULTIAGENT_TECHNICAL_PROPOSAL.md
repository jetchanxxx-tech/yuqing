# 多 Agent 辩论技术方案

**会议**: 盘古舆情产品差异化战略头脑风暴  
**准备人**: 技术总监  
**日期**: 2026-09-25  
**状态**: 技术可行性分析 + 2 周 MVP 实施方案

---

## 目录

1. [核心差异化价值](#核心差异化价值)
2. [ForumEngine 架构设计](#forumengine-架构设计)
3. [五专家+主持人模式](#五专家主持人模式)
4. [Prompt 工程防雷同方案](#prompt-工程防雷同方案)
5. [2周MVP实施计划](#2周mvp实施计划)
6. [风险与降级方案](#风险与降级方案)
7. [成本与ROI分析](#成本与roi分析)

---

## 核心差异化价值

### 市场定位

**传统舆情监测**（竞品）:
```
数据采集 → 情感分类 → 话题聚类 → 报告生成
输出：冰冷的数据图表 + 简单结论
```

**盘古舆情**（我们）:
```
数据采集 → 情感分类 → 话题聚类 → 
   ↓
多 Agent 专家辩论（🔥 核心差异化）
   ↓
输出：有观点冲突、有论据交锋、有策略演进的"活的研判过程"
```

### 用户价值

**痛点**：传统报告是"黑盒"——用户不知道结论从何而来，不信任 AI 判断。

**解决方案**：展示"推理过程"——4 个专家各自陈述 → 交叉质询 → 形成共识，用户看到：
- ✅ 哪些证据被采纳（可追溯性）
- ✅ 哪些观点被反驳（可验证性）
- ✅ 最终方案如何演进（可理解性）

**类比**：从"医生给结论"升级到"会诊过程直播"。

---

## ForumEngine 架构设计

### 当前状态（v0.1.3）

**文件**: `engines/forum_engine/main.py`

```python
@app.post("/debate")
async def debate_forum(req: ForumRequest):
    # 🔴 Mock 实现：返回预置的"雅阁后排"辩论（4 Agent × 3 轮）
    return ForumResponse(
        rounds=MOCK_DEBATE,
        verdict="形成最终方案：24h 内官方号发布自黑式图文...",
        confidence=0.85,
    )
```

**问题**：
- 硬编码辩论内容（只能演示"雅阁后排"）
- 无 LLM 调用（无法适应其他话题）
- 无 Agent 角色分工（4 个专家是人工编写的）

### 目标架构（v0.2.0-multiagent）

```
┌─────────────────────────────────────────────────────────┐
│  ForumEngine (Python FastAPI)                           │
├─────────────────────────────────────────────────────────┤
│  POST /debate                                            │
│    ↓                                                     │
│  ① Moderator（主持人）← 设定议题 + 分配发言顺序        │
│    ↓                                                     │
│  ② Round 1（第一轮陈述）                                │
│     • 事实核查员 Agent → LLM call                       │
│     • 情绪分析师 Agent → LLM call                       │
│     • 传播专家 Agent → LLM call                         │
│     • 处置建议官 Agent → LLM call                       │
│     [4 个并发调用，耗时 ~10-15s]                        │
│    ↓                                                     │
│  ③ Round 2（交叉质询）                                  │
│     • Moderator 总结 Round 1 → 提出质询点               │
│     • 各 Agent 针对其他 Agent 观点提出质疑/补充         │
│     [4 个并发调用，耗时 ~10-15s]                        │
│    ↓                                                     │
│  ④ Round 3（共识形成）                                  │
│     • Moderator 引导达成一致                            │
│     • 处置建议官给出最终方案                            │
│     [串行执行，耗时 ~8-12s]                             │
│    ↓                                                     │
│  ⑤ 返回完整辩论记录 + 最终结论                          │
│     总耗时：30-45s                                       │
└─────────────────────────────────────────────────────────┘
```

### 核心组件

#### 1. Moderator（主持人 Agent）

**职责**：
- 设定辩论议题（从舆情分析结果提取）
- 分配发言顺序（Round 1 顺序固定，Round 2/3 动态）
- 总结上轮观点（避免重复）
- 引导共识形成（检测分歧，推动收敛）

**Prompt 模板**（核心示例）：
```python
MODERATOR_ROUND1 = """你是舆情辩论主持人。基于以下舆情分析结果，设定本轮辩论议题。

**舆情摘要**：
- 关键词：{keywords}
- 情感分布：正面 {pos}% / 负面 {neg}% / 中性 {neu}%
- 核心话题：{topics}
- 争议焦点：{controversial_points}

**你的任务**：
1. 提炼核心议题（1 句话）
2. 设定 4 位专家的发言顺序和角度

输出格式：
- 议题：[简洁陈述]
- 专家 1（事实核查员）：请核验 [具体方向]
- 专家 2（情绪分析师）：请分析 [具体维度]
- 专家 3（传播专家）：请追踪 [传播路径]
- 专家 4（处置建议官）：请给出 [初步方案]
"""
```

#### 2. Agent 角色定义

**文件**: `engines/forum_engine/agents.py`

| 角色 | 职责 | 人设关键词 | Temperature | 关注维度 |
|------|------|-----------|-------------|---------|
| 🔍 **事实核查员** | 证据核验 | 严谨、求证、拒绝传言 | 0.2 | 时间线/数据真实性/信息源 |
| 💭 **情绪分析师** | 公众情绪 | 敏锐、共情、预判 | 0.5 | 情感细分/群体心理/趋势 |
| 📡 **传播专家** | 渠道扩散 | 洞察、网络敏感、跨平台 | 0.3 | 传播链条/平台特性/KOL |
| 🎯 **处置建议官** | 策略制定 | 果断、务实、可执行 | 0.4 | 策略类型/时间窗口/预期效果 |

**代码示例**：
```python
from dataclasses import dataclass
from typing import List

@dataclass
class AgentRole:
    name: str
    role: str
    persona: str
    focus_areas: List[str]
    temperature: float

AGENTS = [
    AgentRole(
        name="事实核查员",
        role="证据与数据核验",
        persona="你是资深调查记者，擅长核查事实真伪、追溯信息源头。你相信「证据先行」，对未经证实的传言保持怀疑。",
        focus_areas=["时间线核查", "数据真实性", "信息源溯源", "事实与观点分离"],
        temperature=0.2,
    ),
    # ... 其他 3 个 Agent
]
```

#### 3. 辩论协调器（Orchestrator）

**文件**: `engines/forum_engine/orchestrator.py`

**核心方法**：
```python
class DebateOrchestrator:
    async def run_debate(self, topic: str, analysis_summary: Dict) -> Dict:
        """运行完整辩论流程"""
        
        # Round 1: 主持人设定议题 + 各专家陈述
        moderator_brief = await self._moderator_round1(topic, analysis_summary)
        round1_statements = await self._agent_round(1, moderator_brief, analysis_summary)
        
        # Round 2: 交叉质询
        moderator_questions = await self._moderator_round2(round1_statements)
        round2_responses = await self._agent_round(2, moderator_questions, analysis_summary)
        
        # Round 3: 共识形成
        final_consensus = await self._moderator_round3(round1_statements + round2_responses)
        round3_final = await self._agent_round(3, final_consensus, analysis_summary)
        
        # 整合结果
        all_rounds = round1_statements + round2_responses + round3_final
        verdict = self._extract_final_verdict(round3_final)
        confidence = self._calculate_confidence(all_rounds)
        
        return {
            "rounds": all_rounds,
            "verdict": verdict,
            "confidence": confidence,
        }
    
    async def _agent_round(self, round_num: int, context: str, summary: Dict) -> List[Dict]:
        """并发执行各 Agent 发言"""
        import asyncio
        
        tasks = []
        for agent in self.agents:
            prompt = self._build_agent_prompt(agent, round_num, context, summary)
            task = self.llm.chat(prompt, temperature=agent.temperature, max_tokens=1024)
            tasks.append((agent, task))
        
        results = []
        for agent, task in tasks:
            try:
                statement = await task
                evidence = self._extract_evidence(statement, summary["documents"])
                results.append({
                    "round": round_num,
                    "agent": agent.name,
                    "agent_role": agent.role,
                    "statement": statement,
                    "evidence": evidence,
                })
            except Exception as e:
                # 单个 Agent 失败不影响整体
                results.append({
                    "round": round_num,
                    "agent": agent.name,
                    "agent_role": agent.role,
                    "statement": f"[技术故障：{str(e)}]",
                    "evidence": [],
                })
        
        return results
```

---

## 五专家+主持人模式

### 三轮辩论流程

```
┌─────────────────────────────────────────────────────────┐
│ Round 1: 事实与证据陈述（各抒己见）                     │
├─────────────────────────────────────────────────────────┤
│ • 主持人：设定议题（从舆情分析提炼核心问题）            │
│ • 事实核查员：核验事件时间线、数据真实性                │
│ • 情绪分析师：分析公众情绪分布、区分真怒与调侃          │
│ • 传播专家：追踪传播路径、识别关键节点                  │
│ • 处置建议官：给出初步策略方向                          │
│                                                          │
│ 并发执行（4 个 LLM 调用），耗时 ~10-15s                 │
└─────────────────────────────────────────────────────────┘
          ↓
┌─────────────────────────────────────────────────────────┐
│ Round 2: 交叉质询（观点碰撞）                           │
├─────────────────────────────────────────────────────────┤
│ • 主持人：总结 Round 1 → 提出 2-3 个质询点             │
│ • 各专家：针对其他专家观点提出质疑/补充/支持            │
│   - 情绪分析师：支持处置建议官的策略，补充案例数据      │
│   - 事实核查员：纠正传播专家的时间线错误                │
│   - 传播专家：提醒竞品正在借势，时间窗口收窄            │
│   - 处置建议官：综合修正方案（快+软+实）                │
│                                                          │
│ 并发执行，耗时 ~10-15s                                   │
└─────────────────────────────────────────────────────────┘
          ↓
┌─────────────────────────────────────────────────────────┐
│ Round 3: 共识形成（最终方案）                           │
├─────────────────────────────────────────────────────────┤
│ • 主持人：检测分歧 → 推动收敛                           │
│ • 处置建议官：给出最终方案（时间节点+具体动作）         │
│ • 其他专家：表态支持 or 保留意见                        │
│ • 主持人：评估可行性（0-1 分）                          │
│                                                          │
│ 串行执行（等待共识），耗时 ~8-12s                       │
└─────────────────────────────────────────────────────────┘
          ↓
     返回完整辩论记录 + 最终结论
```

### 示例输出（"雅阁后排"案例）

**Round 1 摘要**：
- 事实核查员："视频发布于 5 月 8 日，雅阁后排空间在同级车中确实处于中游偏下，但并非「无法坐人」，部分网络传播存在夸大。"
- 情绪分析师："负面占比 78%，但深入分析发现 62% 是戏谑调侃，真实愤怒仅 16%。说明公众更多在「玩梗」而非「声讨」。"
- 传播专家："传播路径清晰：抖音首发 → 24h 内微博热搜第 7 → B站二创 200+ 视频。典型的三段式扩散。"
- 处置建议官："不建议严肃辟谣。玩梗情绪下，官方越严肃越容易被二次创作。建议 24h 内以「自黑式」回应承接情绪。"

**Round 2 质询**：
- 主持人："事实核查员认为负面占比 78%，但情绪分析师指出其中 62% 是戏谑。这两个数据是否矛盾？"
- 情绪分析师："不矛盾。负面占比是情感分类结果，但情感≠态度。戏谑虽然被归为负面，但破坏力远低于真实愤怒。"

**Round 3 共识**：
- 处置建议官："形成最终方案：① 24h 内官方号发布自黑式图文，承认「后排确实可以更好」；② 同步发布真实空间实测数据长图；③ 邀请 3 位 KOL 实车体验直播。"
- 主持人："可行性评分 0.85，建议 48h 内执行。"

---

## Prompt 工程防雷同方案

### 问题：Agent 观点趋同

**风险**：如果 4 个 Agent 的 Prompt 相似，LLM 可能输出雷同观点（如都说"建议加强宣传"），失去辩论价值。

### 解决方案：三层差异化

#### 1. **人设差异化**（Persona）

```python
# ❌ 错误示范（人设模糊）
persona = "你是舆情分析专家"

# ✅ 正确示范（人设具体 + 强烈立场）
persona_fact_checker = """你是资深调查记者，有 15 年事实核查经验。
你相信「证据先行」，对任何未经证实的传言保持怀疑。
你的职责是确保辩论建立在可靠证据之上，而非猜测和传言。"""

persona_emotion = """你是心理学博士 + 传播学专家，擅长解读群体情绪。
你认为「情绪比事实更重要」——舆情的破坏力取决于情绪烈度，而非事实真伪。
你能区分真实愤怒、戏谑调侃、从众跟风。"""
```

**关键**：给每个 Agent 一个**强烈的立场**和**独特的价值观**。

#### 2. **关注维度差异化**（Focus Areas）

```python
# 事实核查员：只关心"是真是假"
focus_areas = ["时间线核查", "数据真实性", "信息源溯源", "事实与观点分离"]

# 情绪分析师：只关心"公众怎么想"
focus_areas = ["情感细分（真怒 vs 调侃）", "群体心理", "情绪演化预测", "破坏力评估"]

# 传播专家：只关心"怎么传播的"
focus_areas = ["传播链条", "平台特性（抖音/微博/B站）", "二次创作", "竞品动态"]

# 处置建议官：只关心"怎么办"
focus_areas = ["策略类型（辟谣/自黑/沉默）", "时间窗口", "资源投入", "预期效果"]
```

**关键**：Prompt 中显式列出"你**只需要**关注 X，**不需要**关注 Y"。

#### 3. **温度参数差异化**（Temperature）

```python
# 事实核查员：低温度，减少幻觉
temperature = 0.2  # 严格基于证据，不发挥

# 情绪分析师：中等温度，平衡创造力
temperature = 0.5  # 允许情绪推测

# 处置建议官：中高温度，鼓励策略创新
temperature = 0.4  # 允许非常规方案
```

#### 4. **对抗性提示**（Adversarial Prompts）

```python
# Round 2 的 Prompt 中，显式要求 Agent "找茬"
prompt_round2 = """
**Round 1 陈述**：
- 事实核查员：{fact_checker_statement}
- 传播专家：{propagation_expert_statement}

**你的任务**（情绪分析师）：
1. 找出事实核查员陈述中被忽略的**情绪维度**
2. 指出传播专家可能**低估的情绪烈度**
3. 补充你独有的心理学洞察

⚠️ 要求：必须提出至少 1 个不同观点，不要简单附和。
"""
```

### 防雷同检测机制

```python
def detect_similarity(statements: List[str]) -> float:
    """检测 Agent 陈述的相似度（0-1）"""
    from sklearn.feature_extraction.text import TfidfVectorizer
    from sklearn.metrics.pairwise import cosine_similarity
    
    if len(statements) < 2:
        return 0.0
    
    vectorizer = TfidfVectorizer()
    tfidf = vectorizer.fit_transform(statements)
    similarity_matrix = cosine_similarity(tfidf)
    
    # 计算非对角线元素的平均相似度
    n = len(statements)
    total = sum(similarity_matrix[i][j] for i in range(n) for j in range(i+1, n))
    avg_similarity = total / (n * (n-1) / 2)
    
    return avg_similarity

# 使用
round1_statements = [r["statement"] for r in round1]
similarity = detect_similarity(round1_statements)

if similarity > 0.7:
    logger.warning(f"Agent 观点相似度过高: {similarity:.2f}，可能需要优化 Prompt")
```

---

## 2周MVP实施计划

### 总体目标

**时间**: 10 个工作日（2 周）  
**范围**: ForumEngine 从 Mock 升级到真实多 Agent 辩论  
**验收标准**: 
- ✅ 可对任意舆情分析结果生成辩论
- ✅ 4 个 Agent 观点有差异（相似度 < 0.6）
- ✅ 辩论耗时 < 60s
- ✅ 前端展示辩论过程（时间线 + 观点卡片）

### 工作量分解

| 任务 | 工作量 | 责任人 | 交付物 |
|------|--------|--------|--------|
| **Day 1-2**: 架构设计 + Prompt 工程 | 2 天 | 后端 | `orchestrator.py`, `agents.py`, Prompt 模板库 |
| **Day 3-4**: LLM 集成 + 并发调用 | 2 天 | 后端 | `llm_client.py` 异步调用 + 超时保护 |
| **Day 5-6**: 辩论协调器实现 | 2 天 | 后端 | `DebateOrchestrator.run_debate()` 完整流程 |
| **Day 7**: 降级方案 + Mock 兜底 | 1 天 | 后端 | LLM 失败时返回 Mock 数据 |
| **Day 8**: 前端辩论展示组件 | 1 天 | 前端 | `DebateTimeline.tsx`, `AgentCard.tsx` |
| **Day 9**: 集成测试 + 真实数据验证 | 1 天 | 后端+前端 | 10 个真实舆情测试用例 |
| **Day 10**: 性能优化 + 部署上线 | 1 天 | 后端 | 并发优化、systemd 配置 |

### 关键里程碑

- **Day 2 结束**：Prompt 模板完成，用 10 个真实舆情测试，确保观点差异化
- **Day 4 结束**：LLM 并发调用通过测试，4 个 Agent 耗时 < 15s
- **Day 6 结束**：完整 3 轮辩论流程跑通，输出结构正确
- **Day 9 结束**：10 个测试用例全部通过，相似度平均 < 0.6

---

## 风险与降级方案

### 风险矩阵

| 风险 | 概率 | 影响 | 等级 | 降级方案 |
|------|------|------|------|---------|
| LLM 生成质量差（观点雷同） | 中 | 高 | **P0** | 优化 Prompt + 对抗性提示 + 温度参数 |
| LLM 调用超时/失败 | 中 | 中 | **P1** | 返回 Mock 数据（无缝降级） |
| 辩论耗时 > 60s | 中 | 低 | P2 | 减少轮次（3 轮 → 2 轮） |
| Agent 观点相似度 > 0.7 | 高 | 高 | **P0** | 加强人设差异化 + 检测机制 |
| 成本过高（12 次调用/次） | 低 | 中 | P2 | 套餐限制（Lite 无辩论，Pro+ 才有） |

### 降级方案详解

#### 方案A：LLM 失败降级到 Mock

```python
# forum_engine/main.py
try:
    result = await orchestrator.run_debate(...)
except Exception as e:
    logger.error(f"Debate failed: {e}")
    result = {
        "rounds": MOCK_DEBATE,
        "verdict": "[演示模式] " + MOCK_DEBATE[-1]["statement"],
        "confidence": 0.75,
    }
```

**用户体验**：
- 前端显示标签："[演示模式]"
- 提示："当前为预置辩论案例，升级套餐可获取实时辩论"

#### 方案B：观点相似度过高时重试

```python
similarity = detect_similarity([r["statement"] for r in round1])
if similarity > 0.7:
    logger.warning(f"Similarity too high: {similarity}, retrying with adjusted temperature")
    # 提高温度参数重试一次
    for agent in AGENTS:
        agent.temperature = min(0.9, agent.temperature + 0.2)
    round1 = await self._agent_round(1, moderator_brief, analysis_summary)
```

#### 方案C：超时保护

```python
import asyncio

async def run_debate_with_timeout(self, topic: str, summary: Dict) -> Dict:
    """带超时保护的辩论执行"""
    try:
        return await asyncio.wait_for(
            self.run_debate(topic, summary),
            timeout=90.0  # 整体超时 90s
        )
    except asyncio.TimeoutError:
        logger.error("Debate timeout, returning partial result")
        return {
            "rounds": self.history,  # 返回已完成的轮次
            "verdict": "辩论未完成（超时），建议重新分析。",
            "confidence": 0.5,
        }
```

---

## 成本与ROI分析

### LLM 调用成本

**单次辩论**：
- Round 1: 5 次调用（1 主持人 + 4 专家）× 1k tokens = 5k tokens
- Round 2: 5 次调用 × 1.5k tokens = 7.5k tokens
- Round 3: 5 次调用 × 1k tokens = 5k tokens
- **总计**: **17.5k tokens / 次辩论**

**GLM-4-flash 定价**（参考）：
- 输入: ¥0.001 / 1k tokens
- 输出: ¥0.002 / 1k tokens
- **单次辩论成本**: ≈ **¥0.03**

**月成本预估**：
- Lite 用户（无辩论）: ¥0
- Pro 用户（10 次/月）: ¥0.3
- Enterprise 用户（50 次/月）: ¥1.5

**结论**：成本极低，不会成为瓶颈。

### 套餐策略

| 套餐 | 价格 | 辩论功能 | 策略 |
|------|------|---------|------|
| Lite | ¥99/月 | ❌ 无 | 引导升级（展示 Mock 案例） |
| Pro | ¥999/月 | ✅ 10 次/月 | **核心价值** |
| Enterprise | ¥4999/月 | ✅ 50 次/月 | 无限制 |

**ROI 分析**：
- Pro 用户升级意愿 = 70%（假设）
- 从 Lite 升级到 Pro = +¥900/月
- 单用户 LLM 成本 = ¥0.3/月
- **净收益**: ¥900 - ¥0.3 = **¥899.7 / 用户月**

### 差异化价值

**竞品对比**：

| 维度 | 传统舆情平台 | 盘古舆情（多 Agent 辩论） |
|------|-------------|--------------------------|
| 输出形式 | 静态报告 | **动态辩论过程** |
| 可信度 | 黑盒 AI | **可追溯推理链** |
| 用户参与感 | 被动接收 | **观看专家辩论** |
| 差异化程度 | 低（同质化严重） | **高（独家功能）** |
| 定价能力 | 弱 | **强（溢价 30%+）** |

**市场定位**：
- 传统平台："数据监测工具"（工具属性，价格敏感）
- 盘古舆情："AI 舆情顾问"（服务属性，价值溢价）

---

## 总结与建议

### 技术可行性：✅ 高

1. **架构清晰**：Orchestrator + 4 Agent + Moderator，职责明确
2. **实现路径明确**：10 天分解到每个文件、每个函数
3. **降级方案完备**：LLM 失败 → Mock 数据，观点雷同 → 重试
4. **成本可控**：单次 ¥0.03，月成本 < ¥2（Enterprise 用户）

### 核心风险：P0

**观点雷同**（相似度 > 0.7）：
- **缓解**：三层差异化（人设 + 关注维度 + 温度参数）+ 对抗性提示
- **检测**：TF-IDF 相似度计算 + 自动告警
- **Plan B**：提高温度参数重试，或人工审核 Prompt 模板

### 推荐决策

#### 方案A：全面推进（推荐 ⭐⭐⭐⭐⭐）

- **时间**：2 周
- **范围**：完整 3 轮辩论
- **优势**：产品差异化最大化，定价能力最强
- **风险**：Prompt 工程需要迭代优化（Day 1-2 重点投入）

#### 方案B：渐进式上线

- **Week 1**：实现 Round 1（4 个 Agent 各自陈述，无交叉质询）
- **Week 2**：增加 Round 2/3（如果 Week 1 效果好）
- **优势**：降低风险，快速验证
- **劣势**：差异化不够明显（只有陈述，无辩论）

#### 方案C：延后（不推荐 ❌）

- 保持 Mock 实现，等待更成熟的 LLM
- **问题**：错失差异化窗口期，竞品可能跟进

### 下一步行动

1. **CEO 决策**：选择方案 A/B/C
2. **资源协调**：分配 1 个后端 + 1 个前端工程师
3. **Prompt 预研**（如果选方案 A）：Day 0（明天），用 10 个真实舆情测试 Prompt 模板
4. **启动开发**：Day 1（后天），按 10 天计划执行

---

**文档版本**: v1.0  
**最后更新**: 2026-09-25  
**状态**: 待 CEO 评审  
**预计开发周期**: 2 周（10 个工作日）  
**核心风险**: 观点雷同（已有检测 + 重试机制）  
**ROI**: 单用户月净收益 ¥900，成本 ¥0.3  
**推荐方案**: 方案 A（全面推进）
