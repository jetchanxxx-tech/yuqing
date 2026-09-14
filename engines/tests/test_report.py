"""Report Engine 测试 — 报告生成（TDD RED）。

report_engine 当前是空桩（/generate 返回空串），以下测试应全部失败。
"""
import json

import pytest
from fastapi.testclient import TestClient

import engines.report_engine.main as report_engine
from engines.report_engine.main import app

client = TestClient(app)

REQ = {
    "title": "雅阁舆情监测报告",
    "documents": [
        {
            "id": "d1", "title": "雅阁后排空间实测翻车", "content": "后排腿部空间局促。",
            "source_type": "news", "source_name": "汽车之家", "published_at": "2026-09-01",
        },
        {
            "id": "d2", "title": "雅阁混动油耗惊艳", "content": "百公里油耗4.2L。",
            "source_type": "news", "source_name": "懂车帝", "published_at": "2026-09-02",
        },
    ],
    "sentiments": [
        {"document_id": "d1", "sentiment": "negative", "score": 0.85},
        {"document_id": "d2", "sentiment": "positive", "score": 0.9},
    ],
    "topics": [
        {"id": "t1", "name": "后排空间", "keywords": ["后排"], "doc_count": 1, "trend": "rising"},
    ],
    "analysis_id": "an-1",
}


DEFAULT_LLM_PAYLOAD = {
    "event_nature": "产品质量型",
    "action_advice": "关注",
    "executive_summary": "本轮舆情整体可控：2篇文档中负面1篇、正面1篇，负面集中在后排空间话题。",
    "topic_analyses": [{
        "topic": "后排空间",
        "analysis": "核心发现：空间争议集中在实测数据。代表性声音：\"后排腿部空间局促。\"（汽车之家）。深层解读：口碑两极分化。",
    }],
    "risk_points": [{
        "level": "高", "type": "短期",
        "desc": "空间实测差评有扩散趋势", "evidence": "实测视频评论区负面居多",
    }],
    "recommendations": [{
        "stage": "立即(0-24h)", "action": "发布官方后排空间实测回应", "rationale": "差评扩散速度快",
    }],
}


class FakeLLM:
    """payload=None 时返回默认研判；显式传入用于模拟 LLM 输出漂移。
    calls 记录完整 prompt（用于断言素材包注入）。"""

    def __init__(self, fail: bool = False, payload: dict | None = None):
        self.fail = fail
        self.payload = payload
        self.calls: list[str] = []

    async def chat_json(self, model: str, messages: list[dict], **kwargs) -> dict:
        self.calls.append(json.dumps(messages, ensure_ascii=False))
        if self.fail:
            raise RuntimeError("deepseek unavailable")
        return self.payload if self.payload is not None else dict(DEFAULT_LLM_PAYLOAD)


@pytest.fixture
def fake_llm(monkeypatch):
    fake = FakeLLM()
    monkeypatch.setattr(report_engine, "build_client", lambda api_key="", base_url="": fake)
    return fake


def test_generate_returns_html_report_with_llm_content(fake_llm):
    resp = client.post("/generate", json={**REQ, "api_key": "sk-x"})

    assert resp.status_code == 200
    body = resp.json()
    assert body["report_id"] != ""
    assert body["format"] == "html"
    content = body["content"]
    # 结构：完整 HTML + 标题 + LLM 摘要 + 话题
    assert "<html" in content
    assert "雅阁舆情监测报告" in content
    assert "舆情整体可控" in content  # LLM 执行摘要
    assert "后排空间" in content  # 话题分析


def test_generate_llm_failure_falls_back_to_data_only_report(fake_llm):
    fake_llm.fail = True
    resp = client.post("/generate", json={**REQ, "api_key": "sk-x"})

    assert resp.status_code == 200
    content = resp.json()["content"]
    # 降级：仍渲染数据部分（标题/情感统计/话题名），并标注 AI 研判不可用
    assert "雅阁舆情监测报告" in content
    assert "后排空间" in content
    assert "AI 研判不可用" in content


def test_generate_empty_documents_returns_empty_report(fake_llm):
    resp = client.post(
        "/generate",
        json={"title": "空报告", "documents": [], "sentiments": [], "topics": [], "api_key": "sk-x"},
    )

    assert resp.status_code == 200
    content = resp.json()["content"]
    assert "空报告" in content
    assert "暂无数据" in content


# ── 报告是给人看的 HTML：所有动态内容都必须转义 ──────────────────
#
# 三个不可信来源：① 分析名称（租户输入，进 <title>/<h1>）
# ② 抓取到的第三方网页标题与 URL（进 <td>/<a href>）
# ③ LLM 输出（由被爬正文派生，可被提示词注入影响）
# 报告在浏览器中以 iframe 渲染，且会被下载/转发，因此必须转义。

HOSTILE_REQ = {
    "title": '</title><script>alert("title")</script>',
    "documents": [
        {
            "id": "d1",
            "title": '"><img src=x onerror=alert("doc")>',
            "url": "javascript:alert('url')",
            "source_type": "news",
        },
    ],
    "sentiments": [{"document_id": "d1", "sentiment": "negative", "score": 0.9}],
    "topics": [
        {"id": "t1", "name": "<b>话题</b>", "keywords": ["<i>k</i>"], "doc_count": 1, "trend": "rising"},
    ],
    "analysis_id": "an-1",
}


def test_generate_escapes_html_from_title_documents_and_topics(fake_llm):
    resp = client.post("/generate", json={**HOSTILE_REQ, "api_key": "sk-x"})

    assert resp.status_code == 200
    content = resp.json()["content"]
    # 不得产生任何可执行/可渲染的新标签
    assert "<script>" not in content
    assert "<img src=x" not in content
    assert "<b>话题</b>" not in content
    assert "&lt;script&gt;" in content
    assert "&lt;img src=x" in content
    # javascript: 伪协议不得进入 href（防下载后打开报告时执行）
    assert "javascript:" not in content


def test_generate_escapes_llm_generated_markup(fake_llm):
    fake_llm.payload = {
        "executive_summary": '<script>alert("summary")</script>摘要',
        "topic_analyses": [{"topic": "<b>t</b>", "analysis": "<i>a</i>"}],
        "risk_points": ["<script>alert('risk')</script>"],
        "recommendations": ['<img src=x onerror=alert("rec")>'],
    }
    resp = client.post("/generate", json={**REQ, "api_key": "sk-x"})

    assert resp.status_code == 200
    content = resp.json()["content"]
    assert "<script>alert" not in content
    assert "<i>a</i>" not in content
    assert "&lt;script&gt;" in content
    assert "摘要" in content  # 合法内容仍要展示


# ── LLM 输出形状漂移：合法 JSON 但字段类型不同，必须降级而非 500 ──

shape_client = TestClient(app, raise_server_exceptions=False)


def test_generate_tolerates_out_of_shape_llm_json(fake_llm):
    """LLM 返回 topic_analyses 字符串数组 / risk_points 字符串时不得 500。"""
    fake_llm.payload = {
        "executive_summary": "总体可控",
        "topic_analyses": ["后排空间", "油耗表现"],
        "risk_points": "差评有扩散趋势",
        "recommendations": ["发布官方回应"],
    }
    resp = shape_client.post("/generate", json={**REQ, "api_key": "sk-x"})

    assert resp.status_code == 200, resp.text
    content = resp.json()["content"]
    assert "总体可控" in content
    assert "后排空间" in content
    assert "差评有扩散趋势" in content
    assert "发布官方回应" in content


def test_generate_tolerates_non_dict_llm_payload(fake_llm):
    """LLM 返回 JSON 数组（而非对象）时应降级为数据报告，不得 500。"""
    fake_llm.payload = ["不是对象"]
    resp = shape_client.post("/generate", json={**REQ, "api_key": "sk-x"})

    assert resp.status_code == 200, resp.text
    assert "AI 研判不可用" in resp.json()["content"]


# ── BettaFish 融合：素材包、内容密度、结构化定性 ──────────────
#
# 报告 LLM 必须看到文档原文（标题/正文/来源/时间），否则研判只能
# 对"上一步输出的聚合 JSON"做二次概括 —— 这是品质差距的根因。
# 同时：风险分层（短期/长期/次生）、建议分阶段、事件定性 + 行动建议、
# 平台对比与情感演变轨迹由代码确定性聚合。

def test_generate_prompt_contains_document_materials(fake_llm):
    """LLM 的 prompt 必须包含文档原文素材包（标题/正文/来源/时间）。"""
    client.post("/generate", json={**REQ, "api_key": "sk-x"})

    assert len(fake_llm.calls) == 1
    prompt = fake_llm.calls[0]
    assert "雅阁后排空间实测翻车" in prompt  # 文档标题
    assert "后排腿部空间局促" in prompt  # 正文原文
    assert "汽车之家" in prompt  # 来源平台
    assert "2026-09-01" in prompt  # 发布时间
    # 情感统计文本（确定性数字）也应注入，供 LLM 直接引用
    assert "负面" in prompt


def test_generate_renders_event_nature_and_action(fake_llm):
    """事件定性与行动建议（介入/关注/规避）应渲染为显式区块。"""
    resp = client.post("/generate", json={**REQ, "api_key": "sk-x"})

    content = resp.json()["content"]
    assert "产品质量型" in content  # 事件定性
    assert "关注" in content  # 行动建议


def test_generate_renders_structured_risk_table(fake_llm):
    """风险点渲染为分层表格：等级/类型/描述/佐证。"""
    resp = client.post("/generate", json={**REQ, "api_key": "sk-x"})

    content = resp.json()["content"]
    assert "空间实测差评有扩散趋势" in content  # 风险描述
    assert "实测视频评论区负面居多" in content  # 佐证素材
    assert "短期" in content  # 风险类型
    assert "高" in content  # 风险等级


def test_generate_renders_staged_recommendations(fake_llm):
    """建议按执行阶段（0-24h/1-3天/中期）分组渲染。"""
    resp = client.post("/generate", json={**REQ, "api_key": "sk-x"})

    content = resp.json()["content"]
    assert "立即(0-24h)" in content  # 执行阶段
    assert "发布官方后排空间实测回应" in content  # 具体行动
    assert "差评扩散速度快" in content  # 依据


def test_generate_renders_platform_breakdown(fake_llm):
    """平台对比表由代码确定性聚合（来源→文档数）。"""
    resp = client.post("/generate", json={**REQ, "api_key": "sk-x"})

    content = resp.json()["content"]
    assert "平台对比" in content
    assert "懂车帝" in content  # 第二个来源也必须在表中


def test_generate_renders_sentiment_timeline(fake_llm):
    """情感演变轨迹由代码按发布时间确定性聚合。"""
    resp = client.post("/generate", json={**REQ, "api_key": "sk-x"})

    content = resp.json()["content"]
    assert "情感演变" in content
    assert "2026-09-01" in content  # 时间桶


def test_generate_insight_unavailable_shows_notice_not_zero_stats(fake_llm):
    """洞察失败时不得渲染误导性的 0/0/0 情感统计（审核 D1）。"""
    resp = client.post(
        "/generate",
        json={**REQ, "sentiments": [], "insight_available": False, "api_key": "sk-x"},
    )

    assert resp.status_code == 200
    content = resp.json()["content"]
    assert "情感分析不可用" in content
    # 概览卡不得出现误导性的 0 统计
    assert '<div class="num">0</div>' not in content


def test_generate_insight_available_but_empty_sentiments_still_no_zero_stats(fake_llm):
    """情感数据缺失但 insight_available=True（如五维结论在、情感分类缺失）：
    同样不得渲染 0/0/0 —— 与「全部中性」无法区分，属误导。

    回归背景：管线 P0 修复后 insightAvailable 按「有产出」判定，
    维度非空即 true，此时情感可能为空 —— 旧条件放行了 0/0/0。
    """
    resp = client.post(
        "/generate",
        json={
            **REQ,
            "sentiments": [],
            "insight_available": True,
            "dimensions": [
                {"id": "background", "name": "背景与事件概述", "findings": "核心发现：测试。"},
            ],
            "api_key": "sk-x",
        },
    )

    assert resp.status_code == 200
    content = resp.json()["content"]
    assert "情感分析不可用" in content
    assert '<div class="num">0</div>' not in content
    # 维度结论独立于情感数据，照常渲染
    assert "背景与事件概述" in content


# ── P1：报告消费五维度结论 ─────────────────────────────────────
#
# 报告必须基于维度研判撰写（编排+润色），而不是从聚合 JSON 重新概括。
# 维度结论是组件：渲染层直接呈现，LLM 只写连接性叙述。

DIMENSIONS_REQ = {
    'dimensions': [
        {
            'id': 'background', 'name': '背景与事件概述',
            'findings': '核心发现：一条实测视频引爆争议。',
            'data_points': ['4 篇文档中 2 篇发布于 9 月上旬'],
            'quotes': [{'text': '腿都伸不直', 'source': '微博'}],
            'deep_read': '深入解读：产品定位与用户预期错位。',
            'trend': '趋势：官方回应后回落。',
        },
        {
            'id': 'deep_cause', 'name': '深层原因与社会影响',
            'findings': '核心发现：空间争议折射家用定位错位。',
            'data_points': ['负面占比 50%'],
            'quotes': [{'text': '建议到店体验', 'source': '新浪汽车'}],
            'deep_read': '深入解读：紧凑级轿车市场的空间军备竞赛。',
            'trend': '趋势：随官方回应进入沉淀期。',
        },
    ],
}


def test_generate_renders_dimension_sections(fake_llm):
    resp = client.post('/generate', json={**REQ, **DIMENSIONS_REQ, 'api_key': 'sk-x'})

    assert resp.status_code == 200
    content = resp.json()['content']
    assert '五维研判' in content
    assert '背景与事件概述' in content
    assert '核心发现：一条实测视频引爆争议。' in content
    assert '腿都伸不直' in content
    assert '深层原因与社会影响' in content


def test_generate_prompt_receives_dimensions(fake_llm):
    client.post('/generate', json={**REQ, **DIMENSIONS_REQ, 'api_key': 'sk-x'})

    assert len(fake_llm.calls) == 1
    prompt = fake_llm.calls[0]
    assert '背景与事件概述' in prompt
    assert '核心发现：一条实测视频引爆争议。' in prompt


def test_generate_without_dimensions_still_renders(fake_llm):
    resp = client.post('/generate', json={**REQ, 'api_key': 'sk-x'})

    assert resp.status_code == 200
    content = resp.json()['content']
    assert '舆情整体可控' in content
    assert '五维研判' not in content
