"""Insight Engine 测试 — 情感分析 + 话题聚类 + 研判摘要（TDD RED）。

insight_engine 当前是空桩（/analyze 返回零值），以下测试应全部失败。
LLM 通过 monkeypatch build_client 注入 FakeLLM，不发起真实请求。
"""
import json

import pytest
from fastapi.testclient import TestClient

import engines.insight_engine.main as insight_engine
from engines.insight_engine.main import app

client = TestClient(app)

DOCS = [
    {
        "id": "d1",
        "title": "雅阁后排空间实测翻车",
        "content": "后排腿部空间局促，身高178cm顶膝，长途乘坐舒适度差。",
        "source_type": "news",
    },
    {
        "id": "d2",
        "title": "雅阁混动油耗惊艳全场",
        "content": "百公里油耗4.2L，驾驶质感出色，车主好评如潮。",
        "source_type": "news",
    },
]


class FakeLLM:
    """按 prompt 内容返回预置 JSON 的假 LLM，并记录调用次数。"""

    def __init__(self, fail: bool = False):
        self.fail = fail
        self.calls: list[str] = []

    async def chat_json(self, model: str, messages: list[dict], **kwargs) -> dict:
        self.calls.append(json.dumps(messages, ensure_ascii=False))
        if self.fail:
            raise RuntimeError("deepseek unavailable")
        if "研判摘要" in self.calls[-1]:
            return {"summary": "舆情总体偏中性，后排空间话题占比最高，需持续关注。"}
        return {
            "sentiments": [
                {"document_id": "d1", "sentiment": "negative", "score": 0.85, "emotions": {"不满": 0.7}},
                {"document_id": "d2", "sentiment": "positive", "score": 0.9},
            ],
            "topics": [
                {"id": "t1", "name": "后排空间", "keywords": ["后排", "腿部"], "doc_count": 1, "trend": "rising"},
                {"id": "t2", "name": "油耗表现", "keywords": ["混动", "油耗"], "doc_count": 1, "trend": "stable"},
            ],
        }


@pytest.fixture
def fake_llm(monkeypatch):
    fake = FakeLLM()
    monkeypatch.setattr(insight_engine, "build_client", lambda api_key="": fake)
    return fake


def test_analyze_empty_documents_returns_zero_values(fake_llm):
    resp = client.post("/analyze", json={"documents": [], "analysis_id": "a1", "api_key": "sk-x"})

    assert resp.status_code == 200
    body = resp.json()
    assert body["sentiments"] == []
    assert body["topics"] == []
    assert body["summary"] == ""
    # 空文档不应发起 LLM 调用
    assert fake_llm.calls == []


def test_analyze_with_documents_returns_sentiments_topics_summary(fake_llm):
    resp = client.post(
        "/analyze",
        json={"documents": DOCS, "analysis_id": "a1", "analysis_type": "brand", "api_key": "sk-x"},
    )

    assert resp.status_code == 200
    body = resp.json()
    assert len(body["sentiments"]) == 2
    assert body["sentiments"][0]["document_id"] == "d1"
    assert body["sentiments"][0]["sentiment"] == "negative"
    assert body["sentiments"][0]["score"] == pytest.approx(0.85)
    assert len(body["topics"]) == 2
    assert body["topics"][0]["name"] == "后排空间"
    assert body["summary"] != ""
    # 情感+话题一次调用，摘要一次调用
    assert len(fake_llm.calls) == 2


def test_analyze_without_api_key_returns_503(fake_llm, monkeypatch):
    monkeypatch.setattr(insight_engine, "DEEPSEEK_API_KEY", "")
    resp = client.post("/analyze", json={"documents": DOCS})

    assert resp.status_code == 503


def test_analyze_llm_failure_returns_502(fake_llm):
    fake_llm.fail = True
    resp = client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x"})

    assert resp.status_code == 502


def test_sentiment_batches_documents(fake_llm):
    resp = client.post(
        "/sentiment",
        json={"documents": DOCS, "analysis_id": "a1", "api_key": "sk-x"},
    )

    assert resp.status_code == 200
    results = resp.json()["results"]
    assert len(results) == 2
    assert results[0]["document_id"] == "d1"
