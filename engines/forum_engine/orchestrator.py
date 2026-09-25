"""Debate orchestrator for multi-agent debate system.

协调主持人和4个专家Agent进行3轮辩论。
"""
import asyncio
from typing import Dict, Any, List
from .agents import AGENTS, AgentRole
from .prompts import format_prompt, get_all_prompts
from .llm_client import LLMClient, calculate_cost_cny
import time


class DebateOrchestrator:
    """多Agent辩论协调器。"""

    def __init__(self, llm_client: LLMClient):
        """初始化协调器。

        Args:
            llm_client: LLM客户端实例
        """
        self.llm_client = llm_client
        self.prompts = get_all_prompts()

    async def run_debate(
        self,
        topic: str,
        data_summary: Dict[str, Any],
        max_rounds: int = 3
    ) -> Dict[str, Any]:
        """运行完整的多Agent辩论。

        Args:
            topic: 舆情主题
            data_summary: 数据摘要字典
            max_rounds: 最大轮次（默认3）

        Returns:
            {
                "rounds": [...],  # 所有发言记录
                "verdict": "...",  # 最终研判
                "confidence": 0.85,  # 置信度
                "metadata": {
                    "total_tokens": 14250,
                    "cost_cny": 0.021,
                    "duration_ms": 38500,
                    "similarity_scores": {...}
                }
            }
        """
        start_time = time.time()
        rounds = []
        total_tokens = 0

        # ══════════════════════════════════════════════════════════
        # Round 1: 议题设定 + 初步陈述
        # ══════════════════════════════════════════════════════════

        # 1.1 主持人设定议题
        moderator_agenda = await self._call_moderator_round1(topic, data_summary)
        rounds.append({
            "round": 1,
            "agent": "主持人",
            "role": "议题设定",
            "statement": moderator_agenda["content"],
            "timestamp": time.time()
        })
        total_tokens += moderator_agenda["tokens"]["total_tokens"]

        # 1.2 4个专家并发初步陈述
        agent_r1_prompts = []
        for agent in AGENTS:
            prompt_template = self.prompts[f"{self._agent_name_to_key(agent.name)}_r1"]
            prompt = format_prompt(
                prompt_template,
                moderator_agenda=moderator_agenda["content"],
                data_summary=self._format_data_summary(data_summary)
            )
            agent_r1_prompts.append((prompt, agent.temperature))

        agent_r1_results = await self.llm_client.call_batch(agent_r1_prompts)

        # 检查错误
        for i, result in enumerate(agent_r1_results):
            if isinstance(result, Exception):
                raise RuntimeError(f"Agent {AGENTS[i].name} Round 1 failed: {result}")

        # 记录Round 1专家发言
        agent_r1_views = {}
        for i, agent in enumerate(AGENTS):
            content = agent_r1_results[i]["content"]
            agent_r1_views[agent.name] = content
            rounds.append({
                "round": 1,
                "agent": agent.name,
                "role": agent.role,
                "statement": content,
                "timestamp": time.time()
            })
            total_tokens += agent_r1_results[i]["tokens"]["total_tokens"]

        # ══════════════════════════════════════════════════════════
        # Round 2: 交叉质询
        # ══════════════════════════════════════════════════════════

        # 2.1 主持人质询
        moderator_challenge = await self._call_moderator_round2(agent_r1_views)
        rounds.append({
            "round": 2,
            "agent": "主持人",
            "role": "交叉质询",
            "statement": moderator_challenge["content"],
            "timestamp": time.time()
        })
        total_tokens += moderator_challenge["tokens"]["total_tokens"]

        # 2.2 4个专家并发回应质询
        agent_r2_prompts = []
        for agent in AGENTS:
            prompt_template = self.prompts[f"{self._agent_name_to_key(agent.name)}_r2"]

            # 构建"其他专家观点"
            other_views = {
                k: v for k, v in agent_r1_views.items() if k != agent.name
            }

            prompt = format_prompt(
                prompt_template,
                moderator_challenge=moderator_challenge["content"],
                **{f"{self._agent_name_to_key(k)}_view": v for k, v in other_views.items()}
            )
            agent_r2_prompts.append((prompt, agent.temperature))

        agent_r2_results = await self.llm_client.call_batch(agent_r2_prompts)

        # 检查错误
        for i, result in enumerate(agent_r2_results):
            if isinstance(result, Exception):
                raise RuntimeError(f"Agent {AGENTS[i].name} Round 2 failed: {result}")

        # 记录Round 2专家发言
        agent_r2_views = {}
        for i, agent in enumerate(AGENTS):
            content = agent_r2_results[i]["content"]
            agent_r2_views[agent.name] = content
            rounds.append({
                "round": 2,
                "agent": agent.name,
                "role": agent.role,
                "statement": content,
                "timestamp": time.time()
            })
            total_tokens += agent_r2_results[i]["tokens"]["total_tokens"]

        # ══════════════════════════════════════════════════════════
        # Round 3: 综合研判
        # ══════════════════════════════════════════════════════════

        # 3.1 主持人综合研判
        moderator_synthesis = await self._call_moderator_round3(agent_r1_views, agent_r2_views)
        rounds.append({
            "round": 3,
            "agent": "主持人",
            "role": "综合研判",
            "statement": moderator_synthesis["content"],
            "timestamp": time.time()
        })
        total_tokens += moderator_synthesis["tokens"]["total_tokens"]

        # 3.2 4个专家串行最终确认（不需要并发）
        for agent in AGENTS:
            prompt_template = self.prompts[f"{self._agent_name_to_key(agent.name)}_r3"]
            prompt = format_prompt(
                prompt_template,
                moderator_synthesis=moderator_synthesis["content"]
            )

            result = await self.llm_client.call(prompt, agent.temperature)

            rounds.append({
                "round": 3,
                "agent": agent.name,
                "role": agent.role,
                "statement": result["content"],
                "timestamp": time.time()
            })
            total_tokens += result["tokens"]["total_tokens"]

        # ══════════════════════════════════════════════════════════
        # 计算元数据
        # ══════════════════════════════════════════════════════════

        duration_ms = int((time.time() - start_time) * 1000)
        cost_cny = calculate_cost_cny(total_tokens)

        # 计算观点相似度
        similarity_scores = self._calculate_similarity(agent_r1_views)

        return {
            "rounds": rounds,
            "verdict": moderator_synthesis["content"],
            "confidence": 0.85,  # TODO: 从主持人综合研判中提取
            "metadata": {
                "total_tokens": total_tokens,
                "cost_cny": cost_cny,
                "duration_ms": duration_ms,
                "similarity_scores": similarity_scores
            }
        }

    # ══════════════════════════════════════════════════════════
    # 私有辅助方法
    # ══════════════════════════════════════════════════════════

    async def _call_moderator_round1(self, topic: str, data_summary: Dict) -> Dict:
        """调用主持人Round 1（议题设定）。"""
        prompt = format_prompt(
            self.prompts["moderator_r1"],
            topic=topic,
            **data_summary
        )
        return await self.llm_client.call(prompt, temperature=0.3)

    async def _call_moderator_round2(self, agent_r1_views: Dict[str, str]) -> Dict:
        """调用主持人Round 2（交叉质询）。"""
        prompt = format_prompt(
            self.prompts["moderator_r2"],
            **{f"{self._agent_name_to_key(k)}_view": v for k, v in agent_r1_views.items()}
        )
        return await self.llm_client.call(prompt, temperature=0.3)

    async def _call_moderator_round3(
        self,
        agent_r1_views: Dict[str, str],
        agent_r2_views: Dict[str, str]
    ) -> Dict:
        """调用主持人Round 3（综合研判）。"""
        # 合并R1和R2的观点
        prompt_vars = {}
        for agent_name in agent_r1_views.keys():
            key_name = self._agent_name_to_key(agent_name)
            prompt_vars[f"{key_name}_r1"] = agent_r1_views[agent_name]
            prompt_vars[f"{key_name}_r2"] = agent_r2_views[agent_name]

        prompt = format_prompt(self.prompts["moderator_r3"], **prompt_vars)
        return await self.llm_client.call(prompt, temperature=0.3)

    def _agent_name_to_key(self, agent_name: str) -> str:
        """Agent名称转换为Prompt模板key。"""
        mapping = {
            "事实核查员": "fact_checker",
            "情绪分析师": "emotion_analyst",
            "传播路径专家": "propagation_expert",
            "处置建议官": "action_advisor"
        }
        return mapping[agent_name]

    def _format_data_summary(self, data_summary: Dict) -> str:
        """格式化数据摘要为文本。"""
        return f"""
文档总数：{data_summary['doc_count']} 条
时间范围：{data_summary['time_range']}
平台分布：{data_summary['platforms']}
情感倾向：正面 {data_summary['sentiment_positive']}% / 负面 {data_summary['sentiment_negative']}% / 中性 {data_summary['sentiment_neutral']}%
高频关键词：{', '.join(data_summary['top_keywords'])}
""".strip()

    def _calculate_similarity(self, agent_views: Dict[str, str]) -> Dict[str, float]:
        """计算专家观点之间的相似度（TF-IDF余弦相似度）。

        Returns:
            {
                "avg": 0.42,  # 平均相似度
                "max": 0.65,  # 最大相似度
                "pairs": {
                    "事实核查员_vs_情绪分析师": 0.35,
                    ...
                }
            }
        """
        from sklearn.feature_extraction.text import TfidfVectorizer
        from sklearn.metrics.pairwise import cosine_similarity
        import numpy as np

        # 提取所有观点文本
        agent_names = list(agent_views.keys())
        texts = [agent_views[name] for name in agent_names]

        # TF-IDF向量化
        vectorizer = TfidfVectorizer()
        tfidf_matrix = vectorizer.fit_transform(texts)

        # 计算余弦相似度矩阵
        similarity_matrix = cosine_similarity(tfidf_matrix)

        # 提取上三角（不包括对角线）
        pairs = {}
        similarities = []
        n = len(agent_names)
        for i in range(n):
            for j in range(i + 1, n):
                sim = similarity_matrix[i][j]
                pair_key = f"{agent_names[i]}_vs_{agent_names[j]}"
                pairs[pair_key] = float(sim)
                similarities.append(sim)

        return {
            "avg": float(np.mean(similarities)),
            "max": float(np.max(similarities)),
            "pairs": pairs
        }


# ══════════════════════════════════════════════════════════
# 测试
# ══════════════════════════════════════════════════════════

async def test_orchestrator():
    """测试辩论协调器。"""
    import sys
    import io
    from .test_cases import get_test_case

    # 修复Windows GBK编码问题
    if sys.platform == 'win32':
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')

    print("[TEST] Debate Orchestrator")

    # 初始化
    llm_client = LLMClient()
    orchestrator = DebateOrchestrator(llm_client)

    # 获取测试用例
    test_case = get_test_case("case_01")  # 雅阁后排空间
    print(f"\n[TEST] Case: {test_case['name']}")

    # 运行辩论
    print("\n[RUNNING] 3-round debate...")
    result = await orchestrator.run_debate(
        topic=test_case["topic"],
        data_summary=test_case["summary"]
    )

    # 输出结果
    print(f"\n[OK] Debate completed:")
    print(f"  Total rounds: {len(result['rounds'])} statements")
    print(f"  Total tokens: {result['metadata']['total_tokens']}")
    print(f"  Cost: ¥{result['metadata']['cost_cny']:.4f}")
    print(f"  Duration: {result['metadata']['duration_ms']}ms")
    print(f"\n[SIMILARITY] Viewpoint diversity:")
    print(f"  Average: {result['metadata']['similarity_scores']['avg']:.3f}")
    print(f"  Max: {result['metadata']['similarity_scores']['max']:.3f}")
    print(f"\n  Pairs:")
    for pair, score in result['metadata']['similarity_scores']['pairs'].items():
        print(f"    {pair}: {score:.3f}")

    # 验收标准
    avg_sim = result['metadata']['similarity_scores']['avg']
    if avg_sim < 0.6:
        print(f"\n[✓] PASS: Average similarity {avg_sim:.3f} < 0.6")
    else:
        print(f"\n[✗] FAIL: Average similarity {avg_sim:.3f} >= 0.6")

    return result


if __name__ == "__main__":
    asyncio.run(test_orchestrator())
