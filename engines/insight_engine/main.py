"""Insight Engine — 情感分析 + 话题聚类 + 五维度研判 + 摘要（DeepSeek）。

分析机制（吸收 BettaFish 的分析思想，代码为本项目独立实现）：
  ① 情感分类 + 话题聚类（temperature=0，分类任务要稳定）
  ② 五个固定维度**各自独立调用**，各有专属人设与挖掘任务：
     背景概述 / 热度传播 / 情感观点 / 群体平台差异 / 深层原因
     每维度输入完整文档素材包（含来源平台、发布时间、正文），
     输出按五段骨架组织（核心发现→数据→代表性声音→深入解读→趋势）
  ③ 汇总摘要：输入 = 全部维度结论 + 原文素材，两段式（批判→重写）

为什么这么改：单轮单视角的「一次调用出全部」必然产出干瘪且跨分析趋同的
结论 —— LLM 既看不到原文（只能对聚合结果二次概括），又没有分维度挖掘的
约束。分维度独立分析让每个维度用不同人设深挖一件事，再汇总。

话题 trend 由代码按发布时间确定性计算（LLM 拿不到时间信息时只能瞎猜）。
"""
import asyncio
import json
import logging
import os
from dataclasses import dataclass
from datetime import datetime

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from engines.common.llm_client import LLM_MODEL, build_client

LLM_API_KEY = os.environ.get("LLM_API_KEY", "") or os.environ.get("DEEPSEEK_API_KEY", "")

app = FastAPI(title="Insight Engine", version="0.3.0")

# 每篇文档正文截断长度 —— 维度分析要读到细节，放宽到 3000 字
MAX_CONTENT_CHARS = 3000

# 每维度素材包的总字符预算（≈8k token）。五个维度各自注入全量素材会 ×5，
# 预算内按发布时间最新优先保留 —— 舆情分析里最新证据最相关。
# P2 的证据底稿（EvidenceSheet 共享）是根本解，本预算是过渡防线。
MAX_MATERIAL_CHARS = 16000


class AnalyzeRequest(BaseModel):
    documents: list[dict] = []
    analysis_id: str = ""
    analysis_type: str = ""
    title: str = ""  # 分析任务名（可选，让 prompt 知道分析对象）
    api_key: str = ""
    # LLM 供应商可配置（后台「数据源配置」在线修改，经 Go 管线透传）：
    llm_base_url: str = ""  # 空 = 环境变量/默认（智谱）
    llm_model: str = ""  # 空 = 环境变量/默认


class SentimentRequest(BaseModel):
    documents: list[dict] = []
    model: str = ""
    analysis_id: str = ""
    api_key: str = ""
    llm_base_url: str = ""


def _require_key(api_key: str) -> str:
    key = api_key or LLM_API_KEY
    if not key:
        raise HTTPException(status_code=503, detail="LLM API Key 未配置")
    return key


# ── 五个分析维度 ────────────────────────────────────────────────
#
# 每个维度独立成一次 LLM 调用，配专属人设与挖掘任务。人设不是装饰 ——
# 它决定模型调用哪部分知识、关注什么证据、用什么口吻表达。
# 同一个"资深分析师"干完所有事，结果必然是各方面都平庸的概括。


@dataclass(frozen=True)
class DimensionSpec:
    id: str
    name: str
    persona: str  # 专属人设
    mission: str  # 该维度要挖到什么


DIMENSIONS: tuple[DimensionSpec, ...] = (
    DimensionSpec(
        id="background",
        name="背景与事件概述",
        persona="你是舆情事件溯源专家，擅长从碎片信息中还原事件全貌与因果链。",
        mission="梳理事件起因、发展脉络与关键节点；标出时间线上真正被反复提及的转折点，"
                "而不是罗列所有日期。",
    ),
    DimensionSpec(
        id="heat",
        name="热度与传播路径",
        persona="你是传播学研究者，擅长还原信息如何从源头扩散到大众视野。",
        mission="分析传播路径、平台分布、引爆点与关键传播角色；区分原始信源与二次转载，"
                "指出哪条内容真正带动了扩散。",
    ),
    DimensionSpec(
        id="sentiment",
        name="公众情感与观点",
        persona="你是民意研究专家，关注普通人的真实情绪而非抽象概括。",
        mission="梳理情感倾向分布、观点阵营与争议焦点；捕捉网民的真实表达方式"
                "（口语、梗、情绪词），而不是把它们翻译成书面语。",
    ),
    DimensionSpec(
        id="group",
        name="群体与平台差异",
        persona="你是社会分层研究者，擅长发现不同群体对同一事件的态度分歧。",
        mission="对比不同平台/群体的立场与话语差异；指出被主流叙事忽略的声音，"
                "以及同一事实在不同平台上被如何不同地表述。",
    ),
    DimensionSpec(
        id="deep_cause",
        name="深层原因与社会影响",
        persona="你是社会观察家，擅长穿透表象追问结构性动因。",
        mission="追问根本原因、社会心理与长期影响；指出该事件折射出的更大议题，"
                "而不是停在事件本身。",
    ),
)

# 维度输出的骨架（BettaFish 手法：把"写法"也约束住，避免自由发挥成散文）
_SKELETON = "核心发现 → 数据 → 代表性声音 → 深入解读 → 趋势"

_DIMENSION_PROMPT = """【分析维度】id={dim_id} 名称={dim_name}

{persona}
{mission}

分析对象：{title}
分析类型：{analysis_type}

【文档素材包】以下是你唯一可引用的素材。引用原声必须**逐字摘录**并注明来源平台，
不得改写、不得编造任何未出现的内容：
{materials}

按五段骨架组织输出（{skeleton}），输出 JSON 对象（不得输出其他内容）：
{{
  "findings": "【核心发现】本维度的关键结论，不少于 150 字，必须具体到人/事/数字",
  "data_points": ["【数据】每条须含具体数字或比例，如「4 篇文档中 3 篇来自微博」"],
  "quotes": [{{"text": "【代表性声音】逐字摘录的原声（不得改写）", "source": "来源平台"}}],
  "deep_read": "【深入解读】这一维度说明了什么，不少于 100 字，不得重复 findings",
  "trend": "【趋势】接下来会怎么走，依据是什么"
}}

【内容密度硬指标】输出前逐条自查：
[ ] findings 是否不少于 150 字？
[ ] data_points 是否至少 3 条，且每条都有具体数字？
[ ] quotes 是否至少 2 条，且都是逐字摘录（不是概括转述）？
[ ] deep_read 是否不少于 100 字，且不是 findings 的复述？
[ ] 是否避免了「舆情」「传播」「倾向」「展望」「引发广泛关注」等官方套话？
[ ] 素材不足时是否如实说明，而不是用通用话术填充？"""


_SENTIMENT_TOPIC_PROMPT = """请对以下舆情文档逐条做情感分析，并聚类出 2-8 个话题。

文档列表（JSON）：
{docs}

输出 JSON 对象（不得输出其他内容）：
{{
  "sentiments": [
    {{"document_id": "文档id",
      "sentiment": "positive|negative|neutral",
      "level": "非常正面|正面|中性|负面|非常负面",
      "score": 0到1的情感强度,
      "confidence": 0到1的置信度,
      "emotions": {{"情绪词": 0到1的强度}}}}
  ],
  "topics": [
    {{"id": "t1", "name": "话题名（用网民的说法，不要抽象化）",
      "keywords": ["关键词"], "doc_count": 该话题包含的文档数,
      "doc_ids": ["属于该话题的文档id"]}}
  ]
}}
要求：
- sentiments 必须覆盖每一篇文档。
- topics 按语料真实结构划分，不要凑数：语料只讲一件事就给 1-2 个话题，
  确实有多个独立议题才拆更多。允许存在无法归类的边缘文档。
- doc_ids 必须真实来自上面的文档列表，且各话题的 doc_ids 不重叠。
- trend 字段不要输出，趋势由平台按发布时间计算。"""

_SUMMARY_PROMPT = """你是一名资深舆情分析师。请分两步完成研判摘要：

第一步【批判】：先写一段不超过 80 字的初稿，然后自评这四点：
① 是否过于官方化、套路化？② 是否缺乏真实的民众声音和情感表达？
③ 是否遗漏了重要的公众观点和争议焦点？④ 是否缺少具体的数字和案例？

第二步【重写】：根据自评结果重写，输出最终摘要。
要求：200-300 字；至少包含 3 个具体数字；必须体现不同维度结论之间的**张力**
（例如情感上同情但平台上沉默、官方回应与民间感受背离）；
避免"舆情""传播""倾向""展望"等官方术语，改用网民真实表达；
若素材间存在数据或说法冲突，明确指出。{context}

各维度分析结论（JSON）：
{dimensions}

情感分布（JSON）：
{sentiments}

话题聚类（JSON）：
{topics}

输出 JSON 对象：{{"critique": "初稿及自评", "revised_summary": "重写后的最终摘要"}}"""


# ── 素材准备 ────────────────────────────────────────────────────


def _doc_briefs(documents: list[dict]) -> list[dict]:
    """压缩文档为情感/话题分类的输入。"""
    out = []
    for d in documents:
        content = (d.get("content") or "").strip()
        out.append({
            "id": d.get("id", ""),
            "title": (d.get("title") or "")[:120],
            "content": content[:MAX_CONTENT_CHARS],
        })
    return out


def _materials_text(documents: list[dict], max_docs: int = 40) -> str:
    """文档素材包 —— 维度分析必须看到原文、来源平台与发布时间。

    缺来源则无法做平台差异分析；缺时间则趋势只能靠猜。
    总字符超过 MAX_MATERIAL_CHARS 时按发布时间最新优先截断，
    并在末尾显式标记 —— 截断必须可见，不得静默丢数据。
    """
    docs = [x for x in documents if isinstance(x, dict)]
    if not docs:
        return "（无文档素材）"

    def _date_key(d: dict) -> str:
        return (d.get("published_at") or "")[:19]

    # 有时间的按时间倒序（最新在前），无时间的排最后；总量不超 max_docs
    ordered = sorted(docs, key=_date_key, reverse=True)[:max_docs]

    parts = []
    kept = 0
    total_chars = 0
    for i, d in enumerate(ordered, 1):
        meta = " | ".join(
            v for v in [
                d.get("source_name") or d.get("source_type", ""),
                (d.get("published_at") or "")[:10],
            ] if v
        )
        entry = (
            f"[{i}] id={d.get('id', '')} 标题：{d.get('title', '')}\n"
            f"来源：{meta}\n"
            f"正文：{(d.get('content') or '')[:MAX_CONTENT_CHARS]}"
        )
        total_chars += len(entry)
        if kept > 0 and total_chars > MAX_MATERIAL_CHARS:
            break
        parts.append(entry)
        kept += 1

    if kept < len(ordered):
        parts.append(f"\n[材料截断：预算 {MAX_MATERIAL_CHARS} 字符，共 {len(ordered)} 篇仅保留最新 {kept} 篇]")
    return "\n\n".join(parts)


# ── 确定性趋势计算 ──────────────────────────────────────────────


def _parse_date(value: str) -> datetime | None:
    if not value:
        return None
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None


def _compute_trends(topics: list[dict], documents: list[dict]) -> dict[str, str]:
    """按发布时间计算话题趋势，覆盖 LLM 的猜测。

    LLM 在 prompt 里看不到日期（也无从判断时间走向），其 trend 属编造。
    规则：以语料时间中位数为界，话题内落在后半段的文档占比
      >= 0.6 → rising；<= 0.4 → falling；否则 stable。
    语料完全没有可用时间 → 一律 stable（不知道就不猜）。
    """
    dates = {}
    for d in documents:
        if not isinstance(d, dict):
            continue
        parsed = _parse_date(d.get("published_at", ""))
        if parsed:
            dates[d.get("id", "")] = parsed

    if not dates:
        return {t.get("id", ""): "stable" for t in topics if isinstance(t, dict)}

    ordered = sorted(dates.values())
    median = ordered[len(ordered) // 2]

    out: dict[str, str] = {}
    for t in topics:
        if not isinstance(t, dict):
            continue
        doc_ids = t.get("doc_ids") or []
        topic_dates = [dates[i] for i in doc_ids if i in dates]
        if not topic_dates:
            out[t.get("id", "")] = "stable"
            continue
        recent = sum(1 for dt in topic_dates if dt >= median)
        frac = recent / len(topic_dates)
        if frac >= 0.6:
            out[t.get("id", "")] = "rising"
        elif frac <= 0.4:
            out[t.get("id", "")] = "falling"
        else:
            out[t.get("id", "")] = "stable"
    return out


# ── 维度分析 ────────────────────────────────────────────────────


def _str_list(value) -> list[str]:
    """把 LLM 可能返回的「字符串或数组」统一为字符串列表（形状漂移防御）。"""
    if isinstance(value, str):
        return [value] if value.strip() else []
    if isinstance(value, list):
        return [str(v) for v in value if v is not None and str(v).strip()]
    return []


def _quote_list(value) -> list[dict]:
    """把 LLM 返回的原声统一为 [{text, source}]；形状漂移时降级而非报错。"""
    out: list[dict] = []
    for item in value if isinstance(value, list) else []:
        if isinstance(item, dict) and str(item.get("text", "")).strip():
            out.append({
                "text": str(item.get("text", "")),
                "source": str(item.get("source", "")),
            })
        elif isinstance(item, str) and item.strip():
            out.append({"text": item, "source": ""})
    return out


# 引语两侧可能的包裹符。LLM 爱给「代表性声音」套引号（「」/“”/'…），
# 包裹符不属于原文 —— 比对前剥掉（只影响匹配，不改写引语本身），
# 否则真引语会被成批误杀，反幻觉机制反而摧毁维度的代表性声音。
_MATCH_WRAPPERS = "\"'“”‘’「」『』«»"


def _normalize_for_match(s: str) -> str:
    """归一化用于子串匹配：去全部空白 + 剥两侧成对包裹引号。

    只去空白与包裹符，不动正文标点 —— 引语跨句拼接、改写标点仍算编造，
    必须丢弃（宁可误杀可疑引语，不放过看似引用的编造）。
    """
    t = "".join(str(s).split())
    while len(t) >= 2 and t[0] in _MATCH_WRAPPERS and t[-1] in _MATCH_WRAPPERS:
        t = t[1:-1]
    return t


def _verify_quotes(
    dimensions: list[dict], documents: list[dict]
) -> list[str]:
    """引用保真校验：维度里的每条原声必须是某篇源文档的归一化子串。

    LLM 编造或改写的引语一律丢弃，返回丢弃原因列表（并入 warning）。
    分析可以降级，但不允许出现「看起来像引用的编造」—— 引语是用户
    最容易轻信的内容，也是反幻觉的最高杠杆点。
    """
    corpus = [
        _normalize_for_match((d.get("content") or "") + (d.get("title") or ""))
        for d in documents
        if isinstance(d, dict)
    ]
    dropped: list[str] = []
    for dim in dimensions:
        kept: list[dict] = []
        for q in dim.get("quotes") or []:
            text = _normalize_for_match(q.get("text", ""))
            if text and any(text in doc for doc in corpus if doc):
                kept.append(q)
            else:
                dropped.append(f"{dim.get('name', dim.get('id', ''))} 的引语「{q.get('text', '')[:30]}」未命中原文")
        dim["quotes"] = kept
    return dropped


async def _analyze_one_dimension(
    llm, spec: DimensionSpec, documents: list[dict], analysis_type: str, title: str,
    llm_model: str = "",
) -> dict:
    """单个维度的独立分析调用。异常由调用方捕获（单维度失败不致命）。"""
    prompt = _DIMENSION_PROMPT.format(
        dim_id=spec.id,
        dim_name=spec.name,
        persona=spec.persona,
        mission=spec.mission,
        title=title or "（未命名分析任务）",
        analysis_type=analysis_type or "综合监测",
        materials=_materials_text(documents),
        skeleton=_SKELETON,
    )
    data = await llm.chat_json(
        llm_model,
        [
            {"role": "system", "content": spec.persona + " 输出必须是 JSON 对象。"},
            {"role": "user", "content": prompt},
        ],
        temperature=0.6,
        max_tokens=4096,
    )
    if not isinstance(data, dict):
        raise ValueError(f"dimension {spec.id} returned non-object")
    return {
        "id": spec.id,
        "name": spec.name,
        "findings": str(data.get("findings", "")),
        "data_points": _str_list(data.get("data_points")),
        "quotes": _quote_list(data.get("quotes")),
        "deep_read": str(data.get("deep_read", "")),
        "trend": str(data.get("trend", "")),
    }


# 维度并发闸门：5 路同时打满会触发供应商限流（429），闸到 3 并发 +
# 单次重试。串行则多花 1-2 分钟 —— 3 是延迟与稳定性的折中。
_DIMENSION_CONCURRENCY = 3

# 重试前的恒定退避秒数（测试 monkeypatch 为 0 加速）。闸门已把并发压到 3，
# 重试风暴风险有限；持锁退避避免绕过并发闸门。
_RETRY_DELAY_SECONDS = 2

logger = logging.getLogger(__name__)


async def _run_dimensions(
    llm, documents: list[dict], analysis_type: str, title: str,
    llm_model: str = "",
) -> tuple[list[dict], list[str]]:
    """并发跑五个维度（闸门限 3）。返回 (成功的维度, 失败原因列表)。"""
    sem = asyncio.Semaphore(_DIMENSION_CONCURRENCY)

    async def guarded(spec: DimensionSpec) -> dict:
        async with sem:
            try:
                return await _analyze_one_dimension(llm, spec, documents, analysis_type, title, llm_model)
            except Exception as exc:
                # 429/超时类瞬时错误重试一次。首次失败必须留痕（排障依赖它）；
                # 重试仍失败则异常向上抛，由 gather(return_exceptions=True)
                # 收进 failed 列表。永久性错误（如 key 无效）会白付一次重试，
                # 换取瞬时错误的恢复，值得。
                logger.warning("dimension %s first attempt failed: %s; retrying once", spec.id, exc)
                await asyncio.sleep(_RETRY_DELAY_SECONDS)
                return await _analyze_one_dimension(llm, spec, documents, analysis_type, title, llm_model)

    results = await asyncio.gather(
        *(guarded(spec) for spec in DIMENSIONS), return_exceptions=True
    )

    dims: list[dict] = []
    failed: list[str] = []
    for spec, res in zip(DIMENSIONS, results):
        if isinstance(res, Exception):
            failed.append(f"{spec.name}（{res}）")
        else:
            dims.append(res)
    return dims, failed


# ── 接口 ────────────────────────────────────────────────────────


@app.get("/health")
async def health():
    return {
        "status": "ok",
        "engine": "insight",
        "version": "0.3.0",
        "dimensions": len(DIMENSIONS),
        "llm": bool(LLM_API_KEY),
    }


@app.post("/analyze")
async def analyze(req: AnalyzeRequest) -> dict:
    """情感分类 + 话题聚类 + 五维度研判 + 汇总摘要。空文档返回零值，不调用 LLM。"""
    if not req.documents:
        return {"sentiments": [], "topics": [], "summary": "", "dimensions": [], "warning": ""}

    key = _require_key(req.api_key)
    llm = build_client(key, req.llm_base_url)
    model = req.llm_model or LLM_MODEL
    briefs = _doc_briefs(req.documents)

    context = f"\n分析类型：{req.analysis_type}" if req.analysis_type else ""
    try:
        # ① 情感 + 话题（分类任务，temperature=0 求稳定）
        sent_topics = await llm.chat_json(
            model,
            [
                {"role": "system", "content": "你是资深舆情分析师。输出必须是 JSON 对象。"},
                {
                    "role": "user",
                    "content": _SENTIMENT_TOPIC_PROMPT.format(
                        docs=json.dumps(briefs, ensure_ascii=False)
                    ),
                },
            ],
            temperature=0,
        )
    except HTTPException:
        raise
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"LLM 调用失败: {exc}") from exc

    sentiments = sent_topics.get("sentiments", []) if isinstance(sent_topics, dict) else []
    topics = sent_topics.get("topics", []) if isinstance(sent_topics, dict) else []
    if not isinstance(topics, list):
        topics = []

    # ② 趋势由代码按发布时间计算，覆盖 LLM 的猜测
    trends = _compute_trends(topics, req.documents)
    for t in topics:
        if isinstance(t, dict):
            t["trend"] = trends.get(t.get("id", ""), "stable")

    # ③ 五维度并发分析（单维度失败降级，不拖垮整次分析）
    dimensions, failed = await _run_dimensions(
        llm, req.documents, req.analysis_type, req.title, model
    )
    warnings = [f"部分维度分析失败：{'；'.join(failed)}"] if failed else []
    # ③' 引用保真校验：编造引语丢弃并告警（防幻觉，P2 核心）
    dropped = _verify_quotes(dimensions, req.documents)
    if dropped:
        warnings.append(f"丢弃 {len(dropped)} 条未命中原文的引语（防编造）：{'；'.join(dropped)}")
    warning = "；".join(warnings)

    # ④ 汇总摘要（输入含各维度结论，避免"对摘要的摘要"）
    try:
        summary_resp = await llm.chat_json(
            model,
            [
                {"role": "system", "content": "你是资深舆情分析师。输出必须是 JSON 对象。"},
                {
                    "role": "user",
                    "content": _SUMMARY_PROMPT.format(
                        context=context,
                        dimensions=json.dumps(
                            [
                                {k: v for k, v in d.items() if k in ("name", "findings", "trend")}
                                for d in dimensions
                            ],
                            ensure_ascii=False,
                        ),
                        sentiments=json.dumps(sentiments, ensure_ascii=False),
                        topics=json.dumps(topics, ensure_ascii=False),
                    ),
                },
            ],
            temperature=0.4,
        )
        summary = (
            summary_resp.get("revised_summary", summary_resp.get("summary", ""))
            if isinstance(summary_resp, dict)
            else ""
        )
    except Exception as exc:
        # 摘要失败不影响已产出的维度结论
        summary = ""
        warning = (warning + "；" if warning else "") + f"摘要生成失败：{exc}"

    return {
        "sentiments": sentiments,
        "topics": topics,
        "summary": summary,
        "dimensions": dimensions,
        "warning": warning,
    }


@app.post("/sentiment")
async def sentiment(req: SentimentRequest) -> dict:
    """批量情感分类（供外部单独调用）。"""
    if not req.documents:
        return {"results": []}

    key = _require_key(req.api_key)
    llm = build_client(key, req.llm_base_url)
    briefs = _doc_briefs(req.documents)
    try:
        resp = await llm.chat_json(
            req.model or LLM_MODEL,
            [
                {"role": "system", "content": "你是资深舆情分析师。输出必须是 JSON 对象。"},
                {
                    "role": "user",
                    "content": _SENTIMENT_TOPIC_PROMPT.format(
                        docs=json.dumps(briefs, ensure_ascii=False)
                    ),
                },
            ],
            temperature=0,
        )
    except HTTPException:
        raise
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"LLM 调用失败: {exc}") from exc

    results = resp.get("sentiments", []) if isinstance(resp, dict) else []
    return {"results": results}
