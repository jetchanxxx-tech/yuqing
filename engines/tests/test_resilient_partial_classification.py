"""A partially valid classification batch preserves exact per-document consumption."""
from fastapi.testclient import TestClient

from engines.common.llm_client import LLMContentRejected
from engines.insight_engine import main as insight
from engines.tests.test_resilient_delivery import DOC, DOC2


def test_partial_classification_marks_only_the_unambiguous_valid_document_read(monkeypatch):
    class IncompleteClassification:
        async def chat_json(self, _model, messages, **_kwargs):
            prompt = messages[-1]["content"]
            if "【分析维度】" in prompt:
                raise LLMContentRejected()
            assert "sentiments" in prompt or "classification_correction" in prompt
            return {"sentiments": [{"document_id": "d2", "sentiment": "negative", "score": 0.7,
                                     "level": "负面", "confidence": 0.9}],
                    "topics": [{"id": "t1", "name": "续航反馈", "keywords": ["续航"], "doc_ids": ["d2"], "doc_count": 1}]}
    monkeypatch.setattr(insight, "build_client", lambda *_args, **_kwargs: IncompleteClassification())
    data = TestClient(insight.app).post("/analyze", json={"resilient": True, "api_key": "offline-key",
        "documents": [DOC, DOC2]}).json()
    assert data["delivery_level"] == "limited"
    assert [s["document_id"] for s in data["sentiments"]] == ["d2"]
    phase = next(p for p in data["material_coverage"]["phases"] if p["phase"] == "classification")
    assert [s["document_id"] for s in phase["read_spans"]] == ["d2"]
    assert [s["document_id"] for s in phase["unprocessed"]] == ["d1"]
