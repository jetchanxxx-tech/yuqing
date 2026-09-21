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
        if "【分析维度】" in self.calls[-1]:
            return {
                "findings": "核心发现：后排空间争议集中在实测数据。",
                "data_points": ["2 篇文档中 1 篇负面"],
                "quotes": [{"text": "腿都伸不直", "source": "微博"}],
                "deep_read": "深入解读：产品定位与用户预期错位。",
                "trend": "趋势：争议仍在扩散。",
            }
        if "批判" in self.calls[-1] and "重写" in self.calls[-1]:
            return {
                "critique": "初稿过于官方化，缺少具体数字。",
                "revised_summary": "舆情总体偏中性：后排空间负面占比高（2篇中1篇负面），需持续关注。",
            }
        return {
            "sentiments": [
                {
                    "document_id": "d1", "sentiment": "negative", "level": "非常负面",
                    "score": 0.85, "confidence": 0.92, "emotions": {"不满": 0.7},
                },
                {
                    "document_id": "d2", "sentiment": "positive", "level": "正面",
                    "score": 0.9, "confidence": 0.88,
                },
            ],
            "topics": [
                {"id": "t1", "name": "后排空间", "keywords": ["后排", "腿部"], "doc_count": 1, "trend": "rising"},
                {"id": "t2", "name": "油耗表现", "keywords": ["混动", "油耗"], "doc_count": 1, "trend": "stable"},
            ],
        }


@pytest.fixture
def fake_llm(monkeypatch):
    fake = FakeLLM()
    monkeypatch.setattr(insight_engine, "build_client", lambda api_key="", base_url="", timeout=120.0: fake)
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
    # 5 级情感 + 置信度（融合 BettaFish 的输出粒度）
    assert body["sentiments"][0]["level"] == "非常负面"
    assert body["sentiments"][0]["confidence"] == pytest.approx(0.92)
    assert len(body["topics"]) == 2
    assert body["topics"][0]["name"] == "后排空间"
    # 摘要来自批判—重写后的 revised_summary
    assert body["summary"] == "舆情总体偏中性：后排空间负面占比高（2篇中1篇负面），需持续关注。"
    # 1 次情感+话题 + 5 个维度 + 1 次批判—重写摘要 = 7 次调用
    assert len(fake_llm.calls) == 7, f"调用次数 = {len(fake_llm.calls)}，期望 7"


def test_analyze_without_api_key_returns_503(fake_llm, monkeypatch):
    monkeypatch.setattr(insight_engine, "LLM_API_KEY", "")
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
