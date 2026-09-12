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
        {"id": "d1", "title": "雅阁后排空间实测翻车", "content": "后排腿部空间局促。", "source_type": "news"},
        {"id": "d2", "title": "雅阁混动油耗惊艳", "content": "百公里油耗4.2L。", "source_type": "news"},
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


class FakeLLM:
    def __init__(self, fail: bool = False):
        self.fail = fail

    async def chat_json(self, model: str, messages: list[dict], **kwargs) -> dict:
        if self.fail:
            raise RuntimeError("deepseek unavailable")
        return {
            "executive_summary": "本轮舆情整体可控，负面集中在后排空间话题。",
            "topic_analyses": [{"topic": "后排空间", "analysis": "负面占比高，需要官方回应。"}],
            "risk_points": ["空间实测差评有扩散趋势"],
            "recommendations": ["发布官方后排空间实测回应"],
        }


@pytest.fixture
def fake_llm(monkeypatch):
    fake = FakeLLM()
    monkeypatch.setattr(report_engine, "build_client", lambda api_key="": fake)
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
