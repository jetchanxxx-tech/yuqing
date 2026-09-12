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

# 每篇文档正文截断长度 —— 控制输入 token，19 篇文档约 15K 字符仍在上下文内
MAX_CONTENT_CHARS = 800


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
    {{"document_id": "文档id", "sentiment": "positive|negative|neutral", "score": 0到1的情感强度, "emotions": {{"情绪词": 0到1的强度}}}}
  ],
  "topics": [
    {{"id": "t1", "name": "话题名（简短）", "keywords": ["关键词"], "doc_count": 该话题包含的文档数, "trend": "rising|stable|falling"}}
  ]
}}
要求：sentiments 必须覆盖每一篇文档；topics 覆盖所有文档且 doc_count 总和等于文档总数。"""

_SUMMARY_PROMPT = """你是一名资深舆情分析师。基于以下情感分布与话题聚类结果，
写一段 200 字以内的中文舆情研判摘要：总体态势、主要风险点、应对建议。{context}

情感分布（JSON）：
{sentiments}

话题聚类（JSON）：
{topics}

输出 JSON 对象：{{"summary": "摘要正文"}}"""


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
        # ② 研判摘要（第二次调用，基于 ① 的结果）
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
        "summary": summary_resp.get("summary", ""),
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
