"""Report Engine — 舆情报告生成（DeepSeek 研判 + HTML 模板渲染）。

LLM 失败时降级为纯数据报告：保留情感统计/话题/文档列表，
AI 研判段落替换为「AI 研判不可用」提示 —— 结果呈现优于整体失败。
"""
import os
import uuid
from datetime import datetime
from string import Template

from fastapi import FastAPI
from pydantic import BaseModel

from engines.common.llm_client import DEEPSEEK_MODEL, build_client

DEEPSEEK_API_KEY = os.environ.get("DEEPSEEK_API_KEY", "")

app = FastAPI(title="Report Engine", version="0.2.0")


class GenerateRequest(BaseModel):
    title: str = ""
    template_id: str = ""
    format: str = "html"
    documents: list[dict] = []
    sentiments: list[dict] = []
    topics: list[dict] = []
    analysis_id: str = ""
    api_key: str = ""


class GenerateResponse(BaseModel):
    report_id: str = ""
    file_key: str = ""
    format: str = "html"
    content: str = ""


_LLM_PROMPT = """你是资深舆情分析师。基于以下舆情数据，撰写研判内容。

标题：{title}
情感分布：{sentiments}
话题聚类：{topics}

输出 JSON 对象：
{{
  "executive_summary": "150字以内的执行摘要",
  "topic_analyses": [{{"topic": "话题名", "analysis": "该话题的态势研判"}}],
  "risk_points": ["风险点"],
  "recommendations": ["应对建议"]
}}"""


def _sentiment_stats(sentiments: list[dict]) -> dict:
    counts = {"positive": 0, "negative": 0, "neutral": 0}
    for s in sentiments:
        v = s.get("sentiment", "neutral")
        if v in counts:
            counts[v] += 1
    return counts


async def _llm_insight(req: GenerateRequest) -> dict | None:
    """LLM 研判。Key 缺失或调用失败返回 None（降级渲染）。"""
    key = req.api_key or DEEPSEEK_API_KEY
    if not key:
        return None
    llm = build_client(key)
    try:
        return await llm.chat_json(
            DEEPSEEK_MODEL,
            [
                {"role": "system", "content": "你是资深舆情分析师。输出必须是 JSON 对象。"},
                {
                    "role": "user",
                    "content": _LLM_PROMPT.format(
                        title=req.title,
                        sentiments=req.sentiments,
                        topics=req.topics,
                    ),
                },
            ],
            temperature=0.3,
        )
    except Exception:
        return None


_TEMPLATE = Template("""<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>$title</title>
<style>
  body { font-family: "PingFang SC", "Microsoft YaHei", sans-serif; margin: 0; color: #1f2937; background: #f5f6fa; }
  .page { max-width: 860px; margin: 0 auto; padding: 40px 32px; background: #fff; }
  h1 { font-size: 26px; border-bottom: 3px solid #1677ff; padding-bottom: 12px; }
  h2 { font-size: 18px; margin-top: 28px; color: #1677ff; }
  .meta { color: #6b7280; font-size: 13px; margin-top: 4px; }
  .cards { display: flex; gap: 12px; margin: 16px 0; flex-wrap: wrap; }
  .card { flex: 1; min-width: 120px; padding: 14px; border-radius: 10px; text-align: center; }
  .card .num { font-size: 28px; font-weight: 700; }
  .card.pos { background: #e8f9ee; } .card.pos .num { color: #02b940; }
  .card.neg { background: #ffe9ec; } .card.neg .num { color: #FF2442; }
  .card.neu { background: #f3f4f6; } .card.neu .num { color: #9ca3af; }
  .card.doc { background: #eaf2ff; } .card.doc .num { color: #1677ff; }
  .card .lbl { font-size: 12px; color: #6b7280; margin-top: 4px; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; margin-top: 10px; }
  th, td { border: 1px solid #e5e7eb; padding: 8px 10px; text-align: left; }
  th { background: #f9fafb; }
  .notice { background: #fff7e6; border: 1px solid #ffd591; border-radius: 8px; padding: 10px 14px; font-size: 13px; color: #ad6800; margin-top: 14px; }
  .empty { color: #9ca3af; text-align: center; padding: 30px 0; }
  ul { padding-left: 20px; line-height: 1.9; }
  p { line-height: 1.8; }
</style>
</head>
<body>
<div class="page">
  <h1>$title</h1>
  <div class="meta">生成时间：$generated_at · 盘古舆情平台</div>

  $body
</div>
</body>
</html>
""")


def _render(req: GenerateRequest, insight: dict | None) -> str:
    """渲染完整 HTML 报告。insight 为 None 时降级为纯数据报告。"""
    stats = _sentiment_stats(req.sentiments)
    generated_at = datetime.now().strftime("%Y-%m-%d %H:%M")

    if not req.documents:
        body = '<div class="empty">暂无数据</div>'
        return _TEMPLATE.substitute(title=req.title, generated_at=generated_at, body=body)

    parts = []

    # 概览卡片
    parts.append(
        f"""<div class="cards">
  <div class="card doc"><div class="num">{len(req.documents)}</div><div class="lbl">采集文档</div></div>
  <div class="card pos"><div class="num">{stats['positive']}</div><div class="lbl">正面</div></div>
  <div class="card neg"><div class="num">{stats['negative']}</div><div class="lbl">负面</div></div>
  <div class="card neu"><div class="num">{stats['neutral']}</div><div class="lbl">中性</div></div>
</div>"""
    )

    if insight:
        parts.append(f"<h2>执行摘要</h2><p>{insight.get('executive_summary', '')}</p>")
        analyses = insight.get("topic_analyses") or []
        if analyses:
            parts.append("<h2>话题研判</h2>")
            for item in analyses:
                parts.append(f"<p><b>{item.get('topic', '')}</b>：{item.get('analysis', '')}</p>")
        risks = insight.get("risk_points") or []
        if risks:
            parts.append("<h2>风险点</h2><ul>" + "".join(f"<li>{r}</li>" for r in risks) + "</ul>")
        recs = insight.get("recommendations") or []
        if recs:
            parts.append("<h2>应对建议</h2><ul>" + "".join(f"<li>{r}</li>" for r in recs) + "</ul>")
    else:
        parts.append('<div class="notice">AI 研判不可用（未配置或调用失败），以下为数据汇总。</div>')

    # 话题表
    if req.topics:
        rows = "".join(
            f"<tr><td>{t.get('name', '')}</td><td>{', '.join(t.get('keywords') or [])}</td>"
            f"<td>{t.get('doc_count', 0)}</td><td>{t.get('trend', '')}</td></tr>"
            for t in req.topics
        )
        parts.append(
            "<h2>话题聚类</h2>"
            "<table><tr><th>话题</th><th>关键词</th><th>文档数</th><th>趋势</th></tr>"
            f"{rows}</table>"
        )

    # 文档列表
    doc_rows = "".join(
        f'<tr><td>{i + 1}</td><td><a href="{d.get("url", "#")}">{d.get("title", "")}</a></td>'
        f"<td>{d.get('source_name') or d.get('source_type', '')}</td></tr>"
        for i, d in enumerate(req.documents[:50])
    )
    parts.append(
        "<h2>文档明细</h2>"
        "<table><tr><th>#</th><th>标题</th><th>来源</th></tr>"
        f"{doc_rows}</table>"
    )

    body = "\n".join(parts)
    return _TEMPLATE.substitute(title=req.title, generated_at=generated_at, body=body)


@app.get("/health")
async def health():
    return {"status": "ok", "engine": "report", "llm": bool(DEEPSEEK_API_KEY)}


@app.post("/generate")
async def generate(req: GenerateRequest) -> GenerateResponse:
    """LLM 研判 + 模板渲染。LLM 不可用时降级为数据报告。"""
    report_id = "rep-" + uuid.uuid4().hex[:16]
    insight = await _llm_insight(req)
    content = _render(req, insight)
    return GenerateResponse(
        report_id=report_id,
        file_key=f"reports/{report_id}.html",
        format="html",
        content=content,
    )
