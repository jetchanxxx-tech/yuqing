"""Report risk citations must remain attached to their actual document."""
import copy

import pytest
from fastapi.testclient import TestClient

from engines.report_engine import main as report
from engines.tests.test_report import REQ, DEFAULT_LLM_PAYLOAD, FakeLLM


def test_risk_citation_cannot_borrow_text_from_another_document(monkeypatch):
    payload = copy.deepcopy(DEFAULT_LLM_PAYLOAD)
    payload["risk_points"] = [{"level": "高", "desc": "错误证据支持的风险", "evidence": "后排腿部空间局促。", "evidence_document_id": "d2"}]
    provider = FakeLLM(payload=payload)
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: provider)
    response = TestClient(report.app).post("/generate", json={**REQ, "api_key": "offline-key"})
    assert response.status_code == 200
    content = response.json()["content"]
    assert "错误证据支持的风险" not in content
    assert "风险证据未通过核验" in content


@pytest.mark.asyncio
async def test_legacy_unique_risk_citation_gets_canonical_provenance(monkeypatch):
    provider = FakeLLM(payload=copy.deepcopy(DEFAULT_LLM_PAYLOAD))
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: provider)
    result = await report._llm_insight(report.GenerateRequest(**REQ, api_key="offline-key"))
    assert result["risk_points"][0]["evidence_document_id"] == "d1"
    assert result["risk_points"][0]["evidence_source"] == "汽车之家"
