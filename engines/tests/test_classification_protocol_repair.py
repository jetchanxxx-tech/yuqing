"""Classification repairs share the existing allowance and preserve failed audit."""
import copy
import json

import httpx
import pytest
from fastapi.testclient import TestClient

from engines.common.llm_client import LLMOutputTruncated
from engines.insight_engine import main as insight
from engines.tests.test_material_coverage import ReadingProvider


DOCS = [{"id": "d1", "content": "iPhone18 Pro续航体验。", "source_type": "toutiao"},
        {"id": "d2", "content": "iPhone18 Max价格讨论。", "source_type": "wechat"}]


class ProtocolProvider(ReadingProvider):
    def __init__(self, second="valid", truncate_first=False, late_only=False):
        super().__init__()
        self.second = second
        self.truncate_first = truncate_first
        self.late_only = late_only
        self.classification_calls = []

    async def chat_json(self, model, messages, **kwargs):
        original = next((message["content"] for message in messages if "文档列表（JSON）" in message["content"]), None)
        if original is None:
            return await super().chat_json(model, messages, **kwargs)
        docs = json.JSONDecoder().raw_decode(original.split("文档列表（JSON）：", 1)[1].lstrip())[0]
        ids = [doc["id"] for doc in docs]
        step = 1 + sum(call["ids"] == ids for call in self.classification_calls)
        self.classification_calls.append({"ids": ids, "messages": copy.deepcopy(messages), "kwargs": kwargs})
        if self.truncate_first and step == 1:
            raise LLMOutputTruncated("output truncated")
        if step > 1 and self.second == "malformed":
            raise ValueError("invalid JSON")
        if step > 1 and self.second == "transport":
            raise httpx.ReadTimeout("offline supplier transport timeout")
        result = {"sentiments": [{"document_id": key, "sentiment": "neutral", "score": 0.5} for key in ids],
                  "topics": [{"id": "topic", "name": "新品体验", "keywords": ["体验"], "doc_count": len(ids), "doc_ids": ids}]}
        should_fail = not self.late_only or "late" in ids
        if should_fail and (step == 1 or self.second == "invalid" or self.truncate_first):
            result["topics"].append({"id": "overlap", "name": "MODEL_CONTENT_NEVER_ECHO", "keywords": [],
                                     "doc_count": 99, "doc_ids": [ids[0], ids[0], "unknown-model-id"]})
        return result


def analyze(monkeypatch, provider, docs=DOCS):
    monkeypatch.setattr(insight, "build_client", lambda *_args, **_kwargs: provider)
    response = TestClient(insight.app).post("/analyze", json={"api_key": "offline-key", "mode": "quick", "documents": docs})
    assert response.status_code == 200
    return response.json()


def classification_phase(body):
    return next(phase for phase in body["material_coverage"]["phases"] if phase["phase"] == "classification")


def test_overlapping_topic_output_is_corrected_before_statistics_are_marked_complete(monkeypatch):
    provider = ProtocolProvider()
    body = analyze(monkeypatch, provider)
    assert body["warning"] == ""
    assert [row["document_id"] for row in body["sentiments"]] == ["d1", "d2"]
    assert body["topics"][0]["doc_ids"] == ["d1", "d2"] and body["topics"][0]["doc_count"] == 2
    assert len(provider.classification_calls) == 2
    assert classification_phase(body)["unprocessed"] == []
    correction = provider.classification_calls[1]["messages"][-1]["content"]
    assert "d1" in correction and "d2" in correction
    assert "MODEL_CONTENT_NEVER_ECHO" not in correction and "unknown-model-id" not in correction
    assert provider.classification_calls[0]["kwargs"]["deadline"] == provider.classification_calls[1]["kwargs"]["deadline"]


@pytest.mark.parametrize("second", ["invalid", "malformed", "transport"])
def test_failed_classification_repair_cannot_buy_a_third_call_or_hide_the_audit(monkeypatch, second):
    provider = ProtocolProvider(second)
    body = analyze(monkeypatch, provider)
    assert body["warning"]
    assert len(provider.classification_calls) == 2
    assert {row["document_id"] for row in body["sentiments"]} == {"d1", "d2"}, "verified partial values must survive a later repair failure"
    assert classification_phase(body)["read_spans"] == []
    assert {row["document_id"] for row in classification_phase(body)["unprocessed"]} == {"d1", "d2"}


def test_truncation_and_topic_repair_share_the_same_two_supplier_attempts(monkeypatch):
    provider = ProtocolProvider(truncate_first=True)
    body = analyze(monkeypatch, provider)
    assert body["warning"]
    assert len(provider.classification_calls) == 2
    assert [call["kwargs"]["max_tokens"] for call in provider.classification_calls] == [16384, 32768]
    assert classification_phase(body)["read_spans"] == []
    assert len(classification_phase(body)["unprocessed"]) == 2


def test_repair_does_not_repurchase_earlier_valid_classification_batches(monkeypatch):
    docs = [{"id": str(index) if index < 34 else "late", "content": "续航体验" * 400, "source_type": "toutiao"} for index in range(35)]
    provider = ProtocolProvider(second="malformed", late_only=True)
    body = analyze(monkeypatch, provider, docs)
    assert body["warning"]
    calls = provider.classification_calls
    assert len([call for call in calls if "late" in call["ids"]]) == 2
    early = [call for call in calls if "late" not in call["ids"]]
    assert early and len({tuple(call["ids"]) for call in early}) == len(early)
    phase = classification_phase(body)
    assert phase["read_spans"] and phase["unprocessed"]
    assert all(row["document_id"] != "late" for row in phase["read_spans"])
    assert any(row["document_id"] == "late" for row in phase["unprocessed"])
