"""Insight Engine — 情感分析 + 话题聚类 + 研判摘要（DeepSeek）。

LLM Key 三级来源：请求参数 api_key → 环境变量 DEEPSEEK_API_KEY。
无 Key 时返回 503；LLM 调用失败返回 502（由平台管线决定降级策略）。
"""
import json
import os

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from engines.common.llm_client import DEEPSEEK_MODEL, build_client

DEEPSEEK_API_KEY = os.environ.get("DEEPSEEK_API_KEY", "")

app = FastAPI(title="Insight Engine", version="0.2.0")

# 每篇文档正文截断长度 —— DeepSeek 上下文充裕，放宽到 2000 字
# 让分析 LLM 能读到正文细节（BettaFish 的写作 prompt 全程可见原文）
MAX_CONTENT_CHARS = 2000


class AnalyzeRequest(BaseModel):
    documents: list[dict] = []
    analysis_id: str = ""
    analysis_type: str = ""
    api_key: str = ""


class SentimentRequest(BaseModel):
    documents: list[dict] = []
    model: str = ""
    analysis_id: str = ""
    api_key: str = ""


def _require_key(api_key: str) -> str:
    key = api_key or DEEPSEEK_API_KEY
    if not key:
        raise HTTPException(status_code=503, detail="DEEPSEEK_API_KEY 未配置")
    return key


def _doc_briefs(documents: list[dict]) -> list[dict]:
    """压缩文档为 LLM 输入：截断标题与正文。"""
    out = []
    for d in documents:
        content = (d.get("content") or "").strip()
        out.append({
            "id": d.get("id", ""),
            "title": (d.get("title") or "")[:120],
            "content": content[:MAX_CONTENT_CHARS],
        })
    return out


_SENTIMENT_TOPIC_PROMPT = """请对以下舆情文档逐条做情感分析，并聚类出 3-6 个话题。

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
    {{"id": "t1", "name": "话题名（简短）", "keywords": ["关键词"], "doc_count": 该话题包含的文档数, "trend": "rising|stable|falling"}}
  ]
}}
要求：sentiments 必须覆盖每一篇文档；topics 覆盖所有文档且 doc_count 总和等于文档总数。
level 与 sentiment 的对应：非常正面/正面→positive，非常负面/负面→negative，中性→neutral。"""

_SUMMARY_PROMPT = """你是一名资深舆情分析师。请分两步完成研判摘要：

第一步【批判】：先写一段不超过 80 字的初稿，然后自评这四点：
① 是否过于官方化、套路化？② 是否缺乏真实的民众声音和情感表达？
③ 是否遗漏了重要的公众观点和争议焦点？④ 是否缺少具体的数字和案例？

第二步【重写】：根据自评结果重写，输出最终摘要。
要求：200 字以内；至少包含 2 个具体数字（情感占比/文档数）；
避免"舆情""传播""倾向""展望"等官方术语，改用网民真实表达；
若文档间存在数据或说法冲突，明确指出。{context}

基于以下情感分布与话题聚类结果：

情感分布（JSON）：
{sentiments}

话题聚类（JSON）：
{topics}

输出 JSON 对象：{{"critique": "初稿及自评", "revised_summary": "重写后的最终摘要"}}"""


@app.get("/health")
async def health():
    return {"status": "ok", "engine": "insight", "llm": bool(DEEPSEEK_API_KEY)}


@app.post("/analyze")
async def analyze(req: AnalyzeRequest) -> dict:
    """情感分类 + 话题聚类 + 研判摘要。空文档返回零值，不调用 LLM。"""
    if not req.documents:
        return {"sentiments": [], "topics": [], "summary": ""}

    key = _require_key(req.api_key)
    llm = build_client(key)
    briefs = _doc_briefs(req.documents)

    context = f"\n分析类型：{req.analysis_type}" if req.analysis_type else ""
    try:
        # ① 情感 + 话题（一次调用）
        sent_topics = await llm.chat_json(
            DEEPSEEK_MODEL,
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
        # ② 研判摘要（第二次调用，批判—重写两段式）
        summary_resp = await llm.chat_json(
            DEEPSEEK_MODEL,
            [
                {"role": "system", "content": "你是资深舆情分析师。输出必须是 JSON 对象。"},
                {
                    "role": "user",
                    "content": _SUMMARY_PROMPT.format(
                        context=context,
                        sentiments=json.dumps(sent_topics.get("sentiments", []), ensure_ascii=False),
                        topics=json.dumps(sent_topics.get("topics", []), ensure_ascii=False),
                    ),
                },
            ],
            temperature=0.3,
        )
    except HTTPException:
        raise
    except Exception as exc:
        raise HTTPException(status_code=502, detail=f"LLM 调用失败: {exc}") from exc

    return {
        "sentiments": sent_topics.get("sentiments", []),
        "topics": sent_topics.get("topics", []),
        "summary": summary_resp.get("revised_summary", summary_resp.get("summary", "")),
    }


@app.post("/sentiment")
async def sentiment(req: SentimentRequest) -> dict:
    """批量情感分类（供外部单独调用）。"""
    if not req.documents:
        return {"results": []}

    key = _require_key(req.api_key)
    llm = build_client(key)
    briefs = _doc_briefs(req.documents)
    try:
        resp = await llm.chat_json(
            req.model or DEEPSEEK_MODEL,
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

    return {"results": resp.get("sentiments", [])}
