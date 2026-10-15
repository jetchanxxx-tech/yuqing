"""Report risk citations must remain attached to their actual document."""
import copy
import json

import httpx
import pytest
from fastapi.testclient import TestClient

from engines.report_engine import main as report
from engines.tests.test_report import REQ, DEFAULT_LLM_PAYLOAD, FakeLLM
from engines.common.llm_client import LLMClient
from engines.tests.test_usage_bridge import billing_sandbox


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


@pytest.mark.asyncio
async def test_report_provider_receives_real_document_ids_before_citing_them(monkeypatch, billing_sandbox):
    observed = []

    def provider(request):
        body = json.loads(request.content)
        observed.append(body)
        prompt = body["messages"][-1]["content"]
        payload = copy.deepcopy(DEFAULT_LLM_PAYLOAD)
        # A provider can only cite an ID that the actual request exposes.
        payload["risk_points"][0]["evidence_document_id"] = "d1" if "id=d1" in prompt else "1"
        return httpx.Response(200, json={
            "model": body["model"], "id": "offline-report-request",
            "choices": [{"message": {"content": json.dumps(payload, ensure_ascii=False)}}],
            "usage": {"prompt_tokens": 100, "completion_tokens": 50},
        })

    client = LLMClient("https://offline.invalid/v1", transport=httpx.MockTransport(provider))
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: client)
    result = await report._llm_insight(report.GenerateRequest(**REQ, run_id="accepted-run", api_key="offline-key"))
    assert result is not None
    assert len(result["risk_points"]) == 1, "correct citation was rejected because the provider never received the real ID"
    prompt = observed[0]["messages"][-1]["content"]
    assert "id=d1" in prompt and "id=d2" in prompt
