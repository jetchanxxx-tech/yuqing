"""Report Engine — 舆情报告生成（DeepSeek 研判 + HTML 模板渲染）。

LLM 失败时降级为纯数据报告：保留情感统计/话题/文档列表，
AI 研判段落替换为「AI 研判不可用」提示 —— 结果呈现优于整体失败。
"""
import io
import os
import uuid
from datetime import datetime
from html import escape
from string import Template

from fastapi import FastAPI
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

from engines.common.llm_client import LLM_MODEL, build_client

try:
    from docx import Document
    from docx.shared import Pt, RGBColor, Inches
    from docx.enum.text import WD_ALIGN_PARAGRAPH
    DOCX_AVAILABLE = True
except ImportError:
    DOCX_AVAILABLE = False

LLM_API_KEY = os.environ.get("LLM_API_KEY", "") or os.environ.get("DEEPSEEK_API_KEY", "")

app = FastAPI(title="Report Engine", version="0.2.0")


class GenerateRequest(BaseModel):
    title: str = ""
    template_id: str = ""
    format: str = "html"
    documents: list[dict] = []
    sentiments: list[dict] = []
    topics: list[dict] = []
    # 五维度研判结论（背景/热度/情感观点/群体差异/深层原因）。
    # 报告基于它们撰写：渲染层直接呈现结论，LLM 只写连接性叙述 ——
    # 不再从聚合 JSON 二次概括（这是此前结论干瘪趋同的根因）。
    dimensions: list[dict] = []
    analysis_id: str = ""
    api_key: str = ""
    llm_base_url: str = ""  # LLM 供应商可配置（后台在线修改，经管线透传）
    llm_model: str = ""
    # False = 洞察引擎未产出（未配置/调用失败）。渲染时不得把空情感
    # 数据画成 0/0/0 —— 那与「全部中性」无法区分，属误导（审核 D1）。
    insight_available: bool = True


class GenerateResponse(BaseModel):
    report_id: str = ""
    file_key: str = ""
    format: str = "html"
    content: str = ""


_LLM_PROMPT = """你是资深舆情分析师和报告撰写专家。你的使命是挖掘真实的民意和人情味，
避免官方化、套路化表达 —— 每句话都要有信息含量。

标题：{title}
情感分布统计（代码聚合的确定数字，请直接引用）：{stats_text}
话题聚类：{topics}

【文档素材包】引用原文时必须逐字摘自以下内容，并注明来源平台：
{materials}

【五维度研判结论】以下是由五个独立分析员（溯源专家/传播学者/民意研究员/
社会分层学者/社会观察家）产出的结论。你的任务是**编排**而非重写：
执行摘要与话题研判必须引用它们的核心发现，不得二次概括成空话：
{dimensions}

输出 JSON 对象：
{{
  "event_nature": "事件定性（七选一）：产品质量型|服务投诉型|价格争议型|谣言驱动型|价值观冲突型|营销活动型|其他",
  "action_advice": "行动建议（三选一）：介入|关注|规避",
  "executive_summary": "150-250字执行摘要，至少包含3个具体数字（文档数/情感占比/话题数）",
  "topic_analyses": [
    {{"topic": "话题名", "analysis": "该话题研判，120-200字，按「核心发现→数据→代表性声音（逐字引用1条原声并注明来源）→深层解读」组织"}}
  ],
  "risk_points": [
    {{"level": "高|中|低", "type": "短期|长期|次生", "desc": "风险描述", "evidence": "佐证素材（逐字摘录）"}}
  ],
  "recommendations": [
    {{"stage": "立即(0-24h)|短期(1-3天)|中期(1-4周)", "action": "具体行动", "rationale": "依据"}}
  ]
}}

【自检清单】输出前逐条自查：
[ ] 执行摘要是否含至少3个具体数字？
[ ] 每个话题分析是否引用了至少1条逐字原声？
[ ] 是否避免了"舆情""传播""倾向""展望"等官方术语？
[ ] 风险是否分了短期/长期/次生且给出了证据？
[ ] 建议是否落到具体行动而非泛泛而谈？"""


def _dimensions_text(dimensions: list[dict]) -> str:
    """五维度结论的 prompt 摘要 —— 各维度的核心发现/数据点/趋势。

    完整结论由渲染层直接呈现；这里只给写作 LLM 编排所需的要点。
    """
    parts = []
    for d in dimensions if isinstance(dimensions, list) else []:
        if not isinstance(d, dict):
            continue
        lines = [f"【{d.get('name', '')}】"]
        if d.get("findings"):
            lines.append(f"核心发现：{d.get('findings')}")
        for dp in _str_list(d.get("data_points"))[:3]:
            lines.append(f"数据：{dp}")
        if d.get("trend"):
            lines.append(f"趋势：{d.get('trend')}")
        parts.append("\n".join(lines))
    return "\n\n".join(parts) if parts else "（无维度结论）"


def _sentiment_stats(sentiments: list[dict]) -> dict:
    counts = {"positive": 0, "negative": 0, "neutral": 0}
    for s in sentiments:
        v = s.get("sentiment", "neutral")
        if v in counts:
            counts[v] += 1
    return counts


def _stats_text(stats: dict) -> str:
    """情感统计的叙述文本 —— 注入 prompt 供 LLM 直接引用确定数字。"""
    total = sum(stats.values())
    if not total:
        return "情感数据不可用"
    pct = lambda n: round(n * 100 / total)
    return (
        f"共 {total} 篇文档：正面 {stats['positive']} 篇（{pct(stats['positive'])}%），"
        f"负面 {stats['negative']} 篇（{pct(stats['negative'])}%），"
        f"中性 {stats['neutral']} 篇（{pct(stats['neutral'])}%）"
    )


def _materials_text(documents: list[dict], max_docs: int = 30) -> str:
    """文档素材包 —— 让写作 LLM 看到原文（标题/来源/时间/正文前 2000 字）。"""
    parts = []
    for i, d in enumerate([x for x in documents if isinstance(x, dict)][:max_docs], 1):
        meta = " | ".join(
            v for v in [
                d.get("source_name") or d.get("source_type", ""),
                (d.get("published_at") or "")[:10],
            ] if v
        )
        parts.append(
            f"[{i}] 标题：{d.get('title', '')}\n"
            f"来源：{meta}\n"
            f"正文：{(d.get('content') or '')[:2000]}"
        )
    return "\n\n".join(parts)


def _sentiment_map(sentiments: list[dict]) -> dict:
    return {
        s.get("document_id"): s.get("sentiment", "neutral")
        for s in sentiments
        if isinstance(s, dict)
    }


def _platform_breakdown(documents: list[dict], sentiments: list[dict]) -> list[dict]:
    """平台对比（确定性聚合）：来源 → 文档数 + 情感分布。"""
    sent_map = _sentiment_map(sentiments)
    agg: dict[str, dict] = {}
    for d in documents:
        if not isinstance(d, dict):
            continue
        name = d.get("source_name") or d.get("source_type") or "未知平台"
        row = agg.setdefault(name, {"platform": name, "docs": 0, "pos": 0, "neg": 0, "neu": 0})
        row["docs"] += 1
        s = sent_map.get(d.get("id", ""), "neutral")
        if s == "positive":
            row["pos"] += 1
        elif s == "negative":
            row["neg"] += 1
        else:
            row["neu"] += 1
    return list(agg.values())


def _sentiment_timeline(documents: list[dict], sentiments: list[dict]) -> list[dict]:
    """情感演变轨迹（确定性聚合）：按发布时间日期分桶。"""
    sent_map = _sentiment_map(sentiments)
    by_date: dict[str, dict] = {}
    for d in documents:
        if not isinstance(d, dict):
            continue
        date = (d.get("published_at") or "")[:10]
        if not date:
            continue
        row = by_date.setdefault(date, {"date": date, "docs": 0, "pos": 0, "neg": 0, "neu": 0})
        row["docs"] += 1
        s = sent_map.get(d.get("id", ""), "neutral")
        if s == "positive":
            row["pos"] += 1
        elif s == "negative":
            row["neg"] += 1
        else:
            row["neu"] += 1
    return [by_date[k] for k in sorted(by_date)]


# ── 渲染输入的类型防御 ────────────────────────────────────────
#
# 进入 HTML 的动态内容全部不可信：
#   ① req.title —— 租户输入（分析名称）
#   ② documents/topics —— 抓取到的第三方网页标题、URL、关键词
#   ③ LLM 输出 —— 由被爬正文派生，可通过提示词注入影响形状与内容
# 因此既要转义，也要容忍 LLM 返回的类型漂移（字符串而非数组、非 dict 等）：
# 报告降级为数据汇总远好于整份报告 500。

def _esc(value) -> str:
    """转义一切进入 HTML 的动态内容（文本与属性值通用）。"""
    return escape(str(value if value is not None else ""))


def _safe_url(value) -> str:
    """只放行 http(s)；javascript:/data: 等伪协议退化为 #，避免点击执行脚本。"""
    url = str(value or "").strip()
    return url if url.lower().startswith(("http://", "https://")) else "#"


def _str_list(value) -> list[str]:
    """把 LLM 可能返回的「字符串或数组」统一为字符串列表。"""
    if isinstance(value, str):
        return [value] if value.strip() else []
    if isinstance(value, list):
        return [str(item) for item in value if item is not None and str(item).strip()]
    return []


def _topic_analyses(value) -> list[tuple[str, str]]:
    """把 topic_analyses 统一为 (话题, 研判) 列表；非 dict 项按话题名处理。"""
    out: list[tuple[str, str]] = []
    for item in value if isinstance(value, list) else []:
        if isinstance(item, dict):
            out.append((str(item.get("topic", "")), str(item.get("analysis", ""))))
        elif isinstance(item, str) and item.strip():
            out.append((item, ""))
    return out


async def _llm_insight(req: GenerateRequest) -> dict | None:
    """LLM 研判。Key 缺失、调用失败或返回非对象时返回 None（降级渲染）。"""
    key = req.api_key or LLM_API_KEY
    if not key:
        return None
    llm = build_client(key, req.llm_base_url, timeout=300)
    try:
        data = await llm.chat_json(
            req.llm_model or LLM_MODEL,
            [
                {"role": "system", "content": "你是资深舆情分析师。输出必须是 JSON 对象。"},
                {
                    "role": "user",
                    "content": _LLM_PROMPT.format(
                        title=req.title,
                        stats_text=_stats_text(_sentiment_stats(req.sentiments)),
                        topics=req.topics,
                        materials=_materials_text(req.documents),
                        dimensions=_dimensions_text(req.dimensions),
                    ),
                },
            ],
            temperature=0.3,
        )
    except Exception:
        return None
    # chat_json 只保证「能解析成 JSON」，不保证是对象
    return data if isinstance(data, dict) else None


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
  .badges { margin: 14px 0; }
  .badge { display: inline-block; border-radius: 999px; padding: 5px 14px; font-size: 13px; margin-right: 8px; }
  .badge.nature { background: #f0f5ff; color: #1677ff; border: 1px solid #adc6ff; }
  .badge.action { background: #fff7e6; color: #ad6800; border: 1px solid #ffd591; }
  .stage { font-size: 14px; margin: 10px 0 2px; color: #374151; }
  .dimension { border-left: 3px solid #e5e7eb; padding-left: 14px; margin: 18px 0; }
  .dimension h3 { font-size: 15px; color: #1677ff; margin: 0 0 6px; }
  blockquote { margin: 8px 0; padding: 8px 14px; background: #f9fafb; border-left: 3px solid #d1d5db; color: #374151; font-size: 13px; }
  blockquote footer { color: #9ca3af; font-size: 12px; margin-top: 4px; }
  .tag { font-size: 13px; color: #6b7280; }
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


def _topic_rows(topics: list[dict]) -> list[str]:
    """话题表行。非 dict 项跳过（请求由 Go 侧构造，此处仅做纵深防御）。"""
    return [
        f"<tr><td>{_esc(t.get('name', ''))}</td>"
        f"<td>{_esc(', '.join(_str_list(t.get('keywords'))))}</td>"
        f"<td>{_esc(t.get('doc_count', 0))}</td><td>{_esc(t.get('trend', ''))}</td></tr>"
        for t in topics
        if isinstance(t, dict)
    ]


def _document_rows(documents: list[dict]) -> list[str]:
    """文档明细行。URL 限 http(s)，标题转义 —— 两者都来自第三方网页。"""
    return [
        f'<tr><td>{i + 1}</td>'
        f'<td><a href="{_esc(_safe_url(d.get("url")))}">{_esc(d.get("title", ""))}</a></td>'
        f"<td>{_esc(d.get('source_name') or d.get('source_type', ''))}</td></tr>"
        for i, d in enumerate([x for x in documents[:50] if isinstance(x, dict)])
    ]


def _overview_cards(req: GenerateRequest, stats: dict) -> str:
    """概览卡片。情感数据缺失时不得画成误导性的 0/0/0。

    判据是「有没有情感数据」而非 insight_available：管线按「有产出」
    判定可用性（部分维度成功即 true），此时情感仍可能为空 —— 空数据
    画成 0/0/0 与「全部中性」无法区分，属误导（审核 D1 及其回归）。
    """
    if not req.sentiments:
        return (
            f'<div class="cards">'
            f'<div class="card doc"><div class="num">{len(req.documents)}</div><div class="lbl">采集文档</div></div>'
            f'</div>'
            f'<div class="notice">情感分析不可用（未产出或调用失败），以下为数据汇总。</div>'
        )
    return (
        f'<div class="cards">'
        f'<div class="card doc"><div class="num">{len(req.documents)}</div><div class="lbl">采集文档</div></div>'
        f'<div class="card pos"><div class="num">{stats["positive"]}</div><div class="lbl">正面</div></div>'
        f'<div class="card neg"><div class="num">{stats["negative"]}</div><div class="lbl">负面</div></div>'
        f'<div class="card neu"><div class="num">{stats["neutral"]}</div><div class="lbl">中性</div></div>'
        f'</div>'
    )


def _risk_section(value) -> str:
    """风险研判：dict 列表渲染分层表格；字符串列表回退为 <ul>。"""
    risks = value if isinstance(value, list) else []
    dict_risks = [r for r in risks if isinstance(r, dict)]
    if dict_risks:
        rows = "".join(
            f"<tr><td>{_esc(r.get('level', ''))}</td><td>{_esc(r.get('type', ''))}</td>"
            f"<td>{_esc(r.get('desc', ''))}</td><td>{_esc(r.get('evidence', ''))}</td></tr>"
            for r in dict_risks
        )
        return (
            "<h2>风险研判</h2>"
            "<table><tr><th>等级</th><th>类型</th><th>风险描述</th><th>佐证</th></tr>"
            f"{rows}</table>"
        )
    flat = _str_list(value)
    if flat:
        return "<h2>风险点</h2><ul>" + "".join(f"<li>{_esc(r)}</li>" for r in flat) + "</ul>"
    return ""


def _recommendation_section(value) -> str:
    """应对建议：dict 列表按执行阶段分组；字符串列表回退为 <ul>。"""
    recs = value if isinstance(value, list) else []
    dict_recs = [r for r in recs if isinstance(r, dict)]
    if dict_recs:
        by_stage: dict[str, list] = {}
        for r in dict_recs:
            by_stage.setdefault(str(r.get("stage") or "未分阶段"), []).append(r)
        parts = ["<h2>应对建议</h2>"]
        for stage, items in by_stage.items():
            parts.append(f'<p class="stage"><b>{_esc(stage)}</b></p><ul>')
            for r in items:
                line = f"<li><b>{_esc(r.get('action', ''))}</b>"
                if r.get("rationale"):
                    line += f" —— {_esc(r.get('rationale', ''))}"
                parts.append(line + "</li>")
            parts.append("</ul>")
        return "".join(parts)
    flat = _str_list(value)
    if flat:
        return "<h2>应对建议</h2><ul>" + "".join(f"<li>{_esc(r)}</li>" for r in flat) + "</ul>"
    return ""


def _platform_section(documents: list[dict], sentiments: list[dict]) -> str:
    rows = _platform_breakdown(documents, sentiments)
    if not rows:
        return ""
    trs = "".join(
        f"<tr><td>{_esc(r['platform'])}</td><td>{r['docs']}</td>"
        f"<td>{r['pos']}</td><td>{r['neg']}</td><td>{r['neu']}</td></tr>"
        for r in rows
    )
    return (
        "<h2>平台对比</h2>"
        "<table><tr><th>平台</th><th>内容数</th><th>正面</th><th>负面</th><th>中性</th></tr>"
        f"{trs}</table>"
    )


def _timeline_section(documents: list[dict], sentiments: list[dict]) -> str:
    rows = _sentiment_timeline(documents, sentiments)
    if not rows:
        return ""
    trs = "".join(
        f"<tr><td>{_esc(r['date'])}</td><td>{r['docs']}</td>"
        f"<td>{r['pos']}</td><td>{r['neg']}</td><td>{r['neu']}</td></tr>"
        for r in rows
    )
    return (
        "<h2>情感演变轨迹</h2>"
        "<table><tr><th>日期</th><th>文档数</th><th>正面</th><th>负面</th><th>中性</th></tr>"
        f"{trs}</table>"
    )


def _dimensions_section(dimensions: list[dict]) -> str:
    """渲染五维研判章节。结论是组件：直接呈现，转义沿用统一防御。

    即使写作 LLM 失败（insight=None），维度结论也要渲染 —— 它们
    由分析引擎产出，与报告引擎的写作无关。
    """
    dims = [d for d in dimensions if isinstance(d, dict)] if isinstance(dimensions, list) else []
    if not dims:
        return ""

    parts = ["<h2>五维研判</h2>"]
    for d in dims:
        name = _esc(d.get("name", "未命名维度"))
        body = ['<div class="dimension">', f"<h3>{name}</h3>"]
        if d.get("findings"):
            body.append(f"<p><b>核心发现</b>：{_esc(d.get('findings', ''))}</p>")
        dps = _str_list(d.get("data_points"))
        if dps:
            body.append("<ul>" + "".join(f"<li>{_esc(dp)}</li>" for dp in dps) + "</ul>")
        quotes = d.get("quotes")
        if isinstance(quotes, list):
            for q in quotes:
                if isinstance(q, dict) and str(q.get("text", "")).strip():
                    src = _esc(q.get("source", ""))
                    body.append(
                        f'<blockquote>{_esc(q.get("text", ""))}'
                        + (f"<footer>—— {src}</footer>" if src else "")
                        + "</blockquote>"
                    )
        if d.get("deep_read"):
            body.append(f"<p><b>深入解读</b>：{_esc(d.get('deep_read', ''))}</p>")
        if d.get("trend"):
            body.append(f'<p class="tag"><b>趋势</b>：{_esc(d.get("trend", ""))}</p>')
        body.append("</div>")
        parts.append("\n".join(body))
    return "\n".join(parts)


def _render(req: GenerateRequest, insight: dict | None) -> str:
    """渲染完整 HTML 报告。insight 为 None 时降级为纯数据报告。"""
    stats = _sentiment_stats(req.sentiments)
    generated_at = datetime.now().strftime("%Y-%m-%d %H:%M")
    title = _esc(req.title)

    if not req.documents:
        body = '<div class="empty">暂无数据</div>'
        return _TEMPLATE.substitute(title=title, generated_at=generated_at, body=body)

    parts = [_overview_cards(req, stats)]

    if insight:
        # 事件定性 + 行动建议徽章（枚举与叙述分离）
        badges = []
        if insight.get("event_nature"):
            badges.append(
                f'<span class="badge nature">事件定性：{_esc(insight.get("event_nature", ""))}</span>'
            )
        if insight.get("action_advice"):
            badges.append(
                f'<span class="badge action">行动建议：{_esc(insight.get("action_advice", ""))}</span>'
            )
        if badges:
            parts.append('<div class="badges">' + "".join(badges) + "</div>")

        parts.append(f"<h2>执行摘要</h2><p>{_esc(insight.get('executive_summary', ''))}</p>")
        analyses = _topic_analyses(insight.get("topic_analyses"))
        if analyses:
            parts.append("<h2>话题研判</h2>")
            for topic, analysis in analyses:
                parts.append(f"<p><b>{_esc(topic)}</b>：{_esc(analysis)}</p>")
        risk_sec = _risk_section(insight.get("risk_points"))
        if risk_sec:
            parts.append(risk_sec)
        rec_sec = _recommendation_section(insight.get("recommendations"))
        if rec_sec:
            parts.append(rec_sec)
    else:
        parts.append('<div class="notice">AI 研判不可用（未配置或调用失败），以下为数据汇总。</div>')

    # 五维研判（分析引擎结论，独立于写作 LLM 直接呈现）
    dims_sec = _dimensions_section(req.dimensions)
    if dims_sec:
        parts.append(dims_sec)

    # 话题表（确定性数据）
    topic_rows = _topic_rows(req.topics)
    if topic_rows:
        parts.append(
            "<h2>话题聚类</h2>"
            "<table><tr><th>话题</th><th>关键词</th><th>文档数</th><th>趋势</th></tr>"
            + "".join(topic_rows) + "</table>"
        )

    # 平台对比 + 情感演变轨迹（代码确定性聚合）
    platform_sec = _platform_section(req.documents, req.sentiments)
    if platform_sec:
        parts.append(platform_sec)
    timeline_sec = _timeline_section(req.documents, req.sentiments)
    if timeline_sec:
        parts.append(timeline_sec)

    # 文档列表
    parts.append(
        "<h2>文档明细</h2>"
        "<table><tr><th>#</th><th>标题</th><th>来源</th></tr>"
        + "".join(_document_rows(req.documents)) + "</table>"
    )

    body = "\n".join(parts)
    return _TEMPLATE.substitute(title=title, generated_at=generated_at, body=body)


def _render_docx(req: GenerateRequest, insight: dict | None) -> bytes:
    """渲染 Word 文档报告。降级策略与 HTML 版一致。"""
    if not DOCX_AVAILABLE:
        raise RuntimeError("python-docx not installed")

    doc = Document()
    stats = _sentiment_stats(req.sentiments)
    generated_at = datetime.now().strftime("%Y-%m-%d %H:%M")

    # 标题
    title_para = doc.add_heading(req.title, level=1)
    title_para.alignment = WD_ALIGN_PARAGRAPH.CENTER
    meta = doc.add_paragraph(f"生成时间：{generated_at} · 盘古舆情平台")
    meta.alignment = WD_ALIGN_PARAGRAPH.CENTER
    for run in meta.runs:
        run.font.size = Pt(10)
        run.font.color.rgb = RGBColor(107, 114, 128)

    if not req.documents:
        doc.add_paragraph("暂无数据")
        buf = io.BytesIO()
        doc.save(buf)
        return buf.getvalue()

    # 概览数据
    doc.add_heading("概览", level=2)
    if req.sentiments:
        overview_table = doc.add_table(rows=1, cols=4)
        overview_table.style = 'Light Grid Accent 1'
        hdr_cells = overview_table.rows[0].cells
        hdr_cells[0].text = "采集文档"
        hdr_cells[1].text = "正面"
        hdr_cells[2].text = "负面"
        hdr_cells[3].text = "中性"
        row_cells = overview_table.add_row().cells
        row_cells[0].text = str(len(req.documents))
        row_cells[1].text = str(stats["positive"])
        row_cells[2].text = str(stats["negative"])
        row_cells[3].text = str(stats["neutral"])
    else:
        doc.add_paragraph(f"采集文档：{len(req.documents)}")
        doc.add_paragraph("情感分析不可用（未产出或调用失败），以下为数据汇总。", style='Intense Quote')

    # AI 研判
    if insight:
        if insight.get("event_nature"):
            doc.add_paragraph(f"事件定性：{insight.get('event_nature', '')}", style='Intense Quote')
        if insight.get("action_advice"):
            doc.add_paragraph(f"行动建议：{insight.get('action_advice', '')}", style='Intense Quote')

        doc.add_heading("执行摘要", level=2)
        doc.add_paragraph(insight.get("executive_summary", ""))

        analyses = _topic_analyses(insight.get("topic_analyses"))
        if analyses:
            doc.add_heading("话题研判", level=2)
            for topic, analysis in analyses:
                p = doc.add_paragraph()
                p.add_run(f"{topic}：").bold = True
                p.add_run(analysis)

        # 风险研判
        risks = insight.get("risk_points")
        if isinstance(risks, list) and risks:
            doc.add_heading("风险研判", level=2)
            dict_risks = [r for r in risks if isinstance(r, dict)]
            if dict_risks:
                risk_table = doc.add_table(rows=1, cols=4)
                risk_table.style = 'Light Grid Accent 1'
                hdr = risk_table.rows[0].cells
                hdr[0].text = "等级"
                hdr[1].text = "类型"
                hdr[2].text = "描述"
                hdr[3].text = "佐证"
                for r in dict_risks:
                    row = risk_table.add_row().cells
                    row[0].text = str(r.get("level", ""))
                    row[1].text = str(r.get("type", ""))
                    row[2].text = str(r.get("desc", ""))
                    row[3].text = str(r.get("evidence", ""))
            else:
                for r in _str_list(risks):
                    doc.add_paragraph(r, style='List Bullet')

        # 应对建议
        recs = insight.get("recommendations")
        if isinstance(recs, list) and recs:
            doc.add_heading("应对建议", level=2)
            dict_recs = [r for r in recs if isinstance(r, dict)]
            if dict_recs:
                by_stage: dict[str, list] = {}
                for r in dict_recs:
                    by_stage.setdefault(str(r.get("stage") or "未分阶段"), []).append(r)
                for stage, items in by_stage.items():
                    doc.add_paragraph(stage, style='Intense Quote')
                    for r in items:
                        p = doc.add_paragraph(style='List Bullet')
                        p.add_run(r.get("action", "")).bold = True
                        if r.get("rationale"):
                            p.add_run(f" —— {r.get('rationale', '')}")
            else:
                for r in _str_list(recs):
                    doc.add_paragraph(r, style='List Bullet')
    else:
        doc.add_paragraph("AI 研判不可用（未配置或调用失败），以下为数据汇总。", style='Intense Quote')

    # 五维研判
    dims = [d for d in req.dimensions if isinstance(d, dict)] if isinstance(req.dimensions, list) else []
    if dims:
        doc.add_heading("五维研判", level=2)
        for d in dims:
            doc.add_heading(d.get("name", "未命名维度"), level=3)
            if d.get("findings"):
                p = doc.add_paragraph()
                p.add_run("核心发现：").bold = True
                p.add_run(d.get("findings", ""))
            for dp in _str_list(d.get("data_points")):
                doc.add_paragraph(dp, style='List Bullet')
            quotes = d.get("quotes")
            if isinstance(quotes, list):
                for q in quotes:
                    if isinstance(q, dict) and str(q.get("text", "")).strip():
                        doc.add_paragraph(q.get("text", ""), style='Intense Quote')
                        if q.get("source"):
                            doc.add_paragraph(f"—— {q.get('source', '')}", style='Quote')
            if d.get("deep_read"):
                p = doc.add_paragraph()
                p.add_run("深入解读：").bold = True
                p.add_run(d.get("deep_read", ""))
            if d.get("trend"):
                doc.add_paragraph(f"趋势：{d.get('trend', '')}")

    # 话题聚类
    topic_rows = [t for t in req.topics if isinstance(t, dict)]
    if topic_rows:
        doc.add_heading("话题聚类", level=2)
        topic_table = doc.add_table(rows=1, cols=4)
        topic_table.style = 'Light Grid Accent 1'
        hdr = topic_table.rows[0].cells
        hdr[0].text = "话题"
        hdr[1].text = "关键词"
        hdr[2].text = "文档数"
        hdr[3].text = "趋势"
        for t in topic_rows:
            row = topic_table.add_row().cells
            row[0].text = str(t.get("name", ""))
            row[1].text = ", ".join(_str_list(t.get("keywords")))
            row[2].text = str(t.get("doc_count", 0))
            row[3].text = str(t.get("trend", ""))

    # 平台对比
    platform_rows = _platform_breakdown(req.documents, req.sentiments)
    if platform_rows:
        doc.add_heading("平台对比", level=2)
        platform_table = doc.add_table(rows=1, cols=5)
        platform_table.style = 'Light Grid Accent 1'
        hdr = platform_table.rows[0].cells
        hdr[0].text = "平台"
        hdr[1].text = "内容数"
        hdr[2].text = "正面"
        hdr[3].text = "负面"
        hdr[4].text = "中性"
        for r in platform_rows:
            row = platform_table.add_row().cells
            row[0].text = r["platform"]
            row[1].text = str(r["docs"])
            row[2].text = str(r["pos"])
            row[3].text = str(r["neg"])
            row[4].text = str(r["neu"])

    # 情感演变轨迹
    timeline_rows = _sentiment_timeline(req.documents, req.sentiments)
    if timeline_rows:
        doc.add_heading("情感演变轨迹", level=2)
        timeline_table = doc.add_table(rows=1, cols=5)
        timeline_table.style = 'Light Grid Accent 1'
        hdr = timeline_table.rows[0].cells
        hdr[0].text = "日期"
        hdr[1].text = "文档数"
        hdr[2].text = "正面"
        hdr[3].text = "负面"
        hdr[4].text = "中性"
        for r in timeline_rows:
            row = timeline_table.add_row().cells
            row[0].text = r["date"]
            row[1].text = str(r["docs"])
            row[2].text = str(r["pos"])
            row[3].text = str(r["neg"])
            row[4].text = str(r["neu"])

    # 文档明细
    doc_rows = [d for d in req.documents[:50] if isinstance(d, dict)]
    if doc_rows:
        doc.add_heading("文档明细", level=2)
        doc_table = doc.add_table(rows=1, cols=3)
        doc_table.style = 'Light Grid Accent 1'
        hdr = doc_table.rows[0].cells
        hdr[0].text = "#"
        hdr[1].text = "标题"
        hdr[2].text = "来源"
        for i, d in enumerate(doc_rows, 1):
            row = doc_table.add_row().cells
            row[0].text = str(i)
            row[1].text = d.get("title", "")
            row[2].text = d.get("source_name") or d.get("source_type", "")

    buf = io.BytesIO()
    doc.save(buf)
    buf.seek(0)
    return buf.getvalue()


@app.get("/health")
async def health():
    return {"status": "ok", "engine": "report", "llm": bool(LLM_API_KEY)}


@app.post("/generate")
async def generate(req: GenerateRequest):
    """LLM 研判 + 模板渲染。LLM 不可用时降级为数据报告。"""
    report_id = "rep-" + uuid.uuid4().hex[:16]
    insight = await _llm_insight(req)

    if req.format == "docx":
        if not DOCX_AVAILABLE:
            return {"error": "python-docx not installed"}, 500
        content_bytes = _render_docx(req, insight)
        return StreamingResponse(
            io.BytesIO(content_bytes),
            media_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
            headers={"Content-Disposition": f"attachment; filename={report_id}.docx"}
        )
    else:
        # HTML (default)
        content = _render(req, insight)
        return GenerateResponse(
            report_id=report_id,
            file_key=f"reports/{report_id}.html",
            format="html",
            content=content,
        )
