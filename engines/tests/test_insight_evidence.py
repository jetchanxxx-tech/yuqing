"""Observable provenance and classification contracts through /analyze."""
import copy
import json

import pytest
from fastapi.testclient import TestClient

from engines.insight_engine import main as insight
from engines.tests.test_insight_quick_mode import DOCS, DIMENSION_PAYLOAD, SENT_TOPIC_RESP


class EvidenceProvider:
    def __init__(self, quotes, classification=None):
        self.quotes = quotes
        self.classification = classification or SENT_TOPIC_RESP

    async def chat_json(self, _model, messages, **_kwargs):
        prompt = json.dumps(messages, ensure_ascii=False)
        if "【分析维度】" in prompt:
            return {**copy.deepcopy(DIMENSION_PAYLOAD), "quotes": copy.deepcopy(self.quotes)}
        if "revised_summary" in prompt:
            return {"revised_summary": "仅依据已核验材料的摘要。"}
        return copy.deepcopy(self.classification)


def analyze(monkeypatch, quotes, documents=None, classification=None):
    provider = EvidenceProvider(quotes, classification)
    monkeypatch.setattr(insight, "build_client", lambda *_args, **_kwargs: provider)
    monkeypatch.setattr(insight, "_RETRY_DELAY_SECONDS", 0)
    response = TestClient(insight.app).post("/analyze", json={
        "documents": documents or DOCS, "api_key": "offline-key", "mode": "quick",
    })
    assert response.status_code == 200
    return response.json()


@pytest.mark.parametrize("quote", [
    {"document_id": "d2", "text": "顶膝", "source": "微博"},
    {"document_id": "invented", "text": "顶膝", "source": "汽车之家"},
    {"document_id": "d1", "text": "顶膝", "source": "伪造媒体"},
    {"text": "顶膝", "source": "微博"},
])
def test_wrong_document_or_source_is_rejected_even_when_text_exists(monkeypatch, quote):
    body = analyze(monkeypatch, [quote])
    assert all(d["quotes"] == [] for d in body["dimensions"])
    assert body["warning"]


def test_legacy_unique_quote_gets_verified_document_and_canonical_source(monkeypatch):
    body = analyze(monkeypatch, [{"text": "「顶膝」", "source": "新闻"}])
    assert body["dimensions"][0]["quotes"] == [{
        "text": "「顶膝」", "source": "汽车之家", "document_id": "d1",
    }]
    assert body["warning"] == ""


def test_ambiguous_legacy_quote_does_not_guess_a_document(monkeypatch):
    documents = [*DOCS, {**DOCS[0], "id": "d3"}]
    body = analyze(monkeypatch, [{"text": "顶膝", "source": "汽车之家"}], documents)
    assert all(d["quotes"] == [] for d in body["dimensions"])
    assert body["warning"]


def test_quote_cannot_join_the_end_of_body_to_the_start_of_title(monkeypatch):
    documents = [{**DOCS[0], "content": "正文尾部", "title": "标题开头"}, DOCS[1]]
    body = analyze(monkeypatch, [{"document_id": "d1", "text": "尾部标题", "source": "汽车之家"}], documents)
    assert all(d["quotes"] == [] for d in body["dimensions"])
    assert body["warning"]


def test_topic_counts_come_from_unique_real_document_membership(monkeypatch):
    classification = copy.deepcopy(SENT_TOPIC_RESP)
    classification["topics"][0].update(doc_count=99, doc_ids=["d1", "d1", "invented", "d2"])
    body = analyze(monkeypatch, [], classification=classification)
    assert body["topics"][0]["doc_ids"] == ["d1", "d2"]
    assert body["topics"][0]["doc_count"] == 2
    assert body["warning"]


@pytest.mark.parametrize("invalid", [
    {"document_id": "invented", "sentiment": "negative", "score": 0.8},
    {"document_id": "d1", "sentiment": "invented", "score": 0.8},
    {"document_id": "d1", "sentiment": "negative", "score": 2},
    {"document_id": "d1", "sentiment": "negative", "score": 0.8, "confidence": "certain"},
])
def test_invalid_classification_does_not_enter_statistics(monkeypatch, invalid):
    classification = copy.deepcopy(SENT_TOPIC_RESP)
    classification["sentiments"] = [invalid, classification["sentiments"][1]]
    body = analyze(monkeypatch, [], classification=classification)
    assert [s["document_id"] for s in body["sentiments"]] == ["d2"]
    assert body["warning"]
