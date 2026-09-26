“””Forum Engine — Multi-agent debate coordinator (盘古舆情核心差异化功能).

Host LLM moderates N specialist agents over R rounds.
Real multi-agent LLM implementation with fallback to mock data.

注意：本文件内的中文文本一律使用全角引号「」与””，绝不使用 ASCII 双引号 ——
ASCII 引号会提前终止 Python 字符串字面量，导致 SyntaxError（曾因此导致
yuqing-forum 服务启动失败）。
“””
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import os
import asyncio
from typing import Optional

# 导入真实实现
try:
    from .llm_client import LLMClient
    from .orchestrator import DebateOrchestrator
    LLM_AVAILABLE = bool(os.getenv(“ZHIPU_API_KEY”))
except Exception as e:
    LLM_AVAILABLE = False
    print(f”[WARN] LLM client not available: {e}”)

app = FastAPI(title=”Forum Engine”, version=”0.3.0”)


# ── Models ──────────────────────────────────────────────

class ForumRequest(BaseModel):
    topic: str = ""
    documents: list[dict] = []
    analysis_id: str = ""
    max_rounds: int = 3


class ForumRound(BaseModel):
    round: int
    agent: str
    agent_role: str
    statement: str
    evidence: list[str] = []


class ForumResponse(BaseModel):
    rounds: list[dict]
    verdict: str
    confidence: float


# ── Mock Debate (MVP) ───────────────────────────────────

MOCK_DEBATE: list[dict] = [
    # ── 第 1 轮：事实与证据核查 ──
    {
        "round": 1, "agent": "事实核查员", "agent_role": "证据与数据核验",
        "statement": "核验核心事实：引发事件的视频发布于 5 月 8 日，车主实测展示后排腿部空间。通过对比多家汽车媒体的实测数据，雅阁后排腿部空间在同级车中确实处于中游偏下，但并非「无法坐人」。部分网络传播内容存在夸大。",
        "evidence": ["太平洋汽车实测数据（2026 年 3 月）", "视频原始链接及发布时间线", "车主后续澄清声明"],
    },
    {
        "round": 1, "agent": "情绪分析师", "agent_role": "公众情绪与心理分析",
        "statement": "负面占比 78%，但深入分析发现 62% 是戏谑调侃情绪，真实愤怒投诉仅 16%。说明公众更多在「玩梗」而非「声讨」，舆情烈度高但破坏力中等。",
        "evidence": ["情感分类模型输出（18,292 条文档标签分布）", "戏谑/愤怒二分类交叉验证结果"],
    },
    {
        "round": 1, "agent": "传播路径专家", "agent_role": "渠道与扩散机制",
        "statement": "传播路径清晰：抖音首发 → 24h 内微博热搜第 7 → B站二创 200+ 视频 → 知乎深度讨论 1,800+ 回答。典型的三段式：短视频情绪引爆 + 社交平台放大 + 社区深度发酵。",
        "evidence": ["时间线：5/8 09:12 首发，5/9 热搜，5/10-12 二创高峰", "各平台互动量对比数据"],
    },
    {
        "round": 1, "agent": "处置建议官", "agent_role": "公关策略与行动方案",
        "statement": "不建议严肃辟谣。玩梗情绪下，官方越严肃越容易被二次创作。建议 24h 内以「自黑式」回应承接情绪。",
        "evidence": ["同类案例：3 个汽车品牌自黑回应当日负面下降 23%", "严肃回应对照组：负面反升 11%"],
    },
    # ── 第 2 轮：交叉质询 ──
    {
        "round": 2, "agent": "情绪分析师", "agent_role": "公众情绪与心理分析",
        "statement": "我支持处置建议官。检索到 3 个汽车品牌类似案例，其中 2 个自黑回应后话题热度 3 天内下降 70% 以上，1 个严肃回应反而引发第二轮负面。数据支持该策略。",
        "evidence": ["3 个可比案例的声量与情绪追踪数据"],
    },
    {
        "round": 2, "agent": "事实核查员", "agent_role": "证据与数据核验",
        "statement": "补充：官方 5 月 10 日的首次回应措辞偏官方化，评论区出现了「避重就轻」的负面解读，印证了严肃回应的风险。",
        "evidence": ["官方回应评论区前 100 条高赞评论分析"],
    },
    {
        "round": 2, "agent": "传播路径专家", "agent_role": "渠道与扩散机制",
        "statement": "我提醒：竞品正在借势。监测到至少 5 个竞品官方号发布空间对比内容，抢占流量。时间窗口在收窄，建议 48h 内完成回应。",
        "evidence": ["竞品官方号发帖时间线与互动数据"],
    },
    {
        "round": 2, "agent": "处置建议官", "agent_role": "公关策略与行动方案",
        "statement": "综合修正建议：回应要快（48h 内）+ 要软（自黑承接）+ 要实（附真实数据）。三要素缺一不可。",
        "evidence": [],
    },
    # ── 第 3 轮：综合研判 ──
    {
        "round": 3, "agent": "处置建议官", "agent_role": "公关策略与行动方案",
        "statement": "形成最终方案：① 24h 内官方号发布自黑式图文，承认「后排确实可以更好」；② 同步发布真实空间实测数据长图；③ 邀请 3 位 KOL 实车体验直播。",
        "evidence": [],
    },
    {
        "round": 3, "agent": "情绪分析师", "agent_role": "公众情绪与心理分析",
        "statement": "同意。预测：方案执行后 72h 负面情绪占比可下降至 45% 以下，正面情绪（「敢自黑」「有诚意」）将上升至 30% 以上。",
        "evidence": ["依据：可比案例情绪迁移曲线"],
    },
    {
        "round": 3, "agent": "传播路径专家", "agent_role": "渠道与扩散机制",
        "statement": "传播预测：自黑式回应本身会成为新的传播点，预计带来第二轮正向流量，可将危机转化为品牌曝光机会。",
        "evidence": [],
    },
    {
        "round": 3, "agent": "事实核查员", "agent_role": "证据与数据核验",
        "statement": "最终核验通过。所有结论均有数据支撑，可溯源。建议置信度评级：0.87。",
        "evidence": [],
    },
]

MOCK_VERDICT = (
    "「雅阁后排」事件本质是「产品短板被情绪放大」的典型戏谑型舆情。"
    "负面情绪中真实愤怒仅占 16%，处置核心不是辟谣而是「借势」。"
    "建议 48 小时内以自黑式回应承接玩梗情绪，同步发布真实数据，"
    "将危机转化为品牌「听得进批评」的正面形象资产。"
    "预期 72h 负面占比降至 45% 以下，并产生第二轮正向传播。"
)


# ── Routes ──────────────────────────────────────────────

@app.get("/health")
async def health():
    return {"status": "ok", "engine": "forum", "version": "0.2.1"}


@app.post("/run_forum", response_model=ForumResponse)
async def run_forum(req: ForumRequest) -> ForumResponse:
    """Run a multi-agent debate.

    If ZHIPU_API_KEY is available, uses real multi-agent LLM calls.
    Otherwise, returns mock debate data for demo purposes.
    """
    # 尝试使用真实LLM
    if LLM_AVAILABLE:
        try:
            # 初始化LLM客户端和协调器
            llm_client = LLMClient()
            orchestrator = DebateOrchestrator(llm_client)

            # 从documents提取数据摘要
            data_summary = _extract_data_summary(req.documents)

            # 运行辩论
            result = await orchestrator.run_debate(
                topic=req.topic,
                data_summary=data_summary,
                max_rounds=req.max_rounds
            )

            return ForumResponse(
                rounds=result["rounds"],
                verdict=result["verdict"],
                confidence=result["confidence"]
            )

        except Exception as e:
            # LLM调用失败，返回503（符合CEO要求：不返回Mock）
            raise HTTPException(
                status_code=503,
                detail=f"LLM服务暂时不可用: {str(e)}"
            )

    # 如果没有API Key，返回Mock数据（开发/演示模式）
    rounds = [r for r in MOCK_DEBATE if r["round"] <= req.max_rounds]
    return ForumResponse(rounds=rounds, verdict=MOCK_VERDICT, confidence=0.87)


def _extract_data_summary(documents: list[dict]) -> dict:
    """从documents中提取数据摘要。

    Args:
        documents: 文档列表（来自Go Platform的分析结果）

    Returns:
        数据摘要字典
    """
    if not documents:
        # 默认摘要
        return {
            "doc_count": 0,
            "time_range": "未知",
            "platforms": "未知",
            "sentiment_positive": 0,
            "sentiment_negative": 0,
            "sentiment_neutral": 0,
            "top_keywords": []
        }

    # TODO: 实际项目中应该从Go Platform的analysis结果中提取
    # 这里先返回基本信息
    return {
        "doc_count": len(documents),
        "time_range": "近7天",
        "platforms": "多平台",
        "sentiment_positive": 30,
        "sentiment_negative": 50,
        "sentiment_neutral": 20,
        "top_keywords": ["舆情", "监测", "分析"]
    }
