"""A deterministic full report must retain recommendations actually analyzed upstream."""
from fastapi.testclient import TestClient

from engines.report_engine import main as report
from engines.tests.test_resilient_delivery import verified_request


def test_complete_evidence_without_verified_action_is_limited(monkeypatch):
    req = verified_request()
    req["api_key"] = ""
    for dimension in req["dimensions"]:
        dimension.pop("recommendations", None)
    monkeypatch.setattr(report, "LLM_API_KEY", "")
    data = TestClient(report.app).post("/generate", json=req).json()
    assert data["delivery_level"] == "limited"
    assert any("建议" in reason for reason in data["delivery_reasons"])
