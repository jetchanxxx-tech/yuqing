"""Numbered citations survive model selection without losing source boundaries."""
import copy

import pytest
from fastapi.testclient import TestClient

from engines.insight_engine import main as insight
from engines.report_engine import main as report
from engines.tests.test_material_coverage import ReadingProvider


TEXT = "雅阁后座靠背偏直，长途乘坐觉得累。"
DOCS = [
    {"id": "d1", "title": "后排实测", "content": TEXT, "source_type": "toutiao",
     "published_at": "2026-10-15T10:00:00Z"},
    {"id": "d2", "title": "相同内容转载", "content": TEXT, "source_type": "wechat",
     "published_at": "2026-10-15T11:00:00Z"},
]
GOOD = {"document_id": "d1", "unit_id": "d1@0:17"}


class UnitProvider(ReadingProvider):
    def __init__(self, quote=GOOD, repair=False):
        super().__init__()
        self.quote = quote
        self.repair = repair
        self.dimension_calls = {}

    async def chat_json(self, model, messages, **kwargs):
        prompt = messages[-1]["content"]
        result = await super().chat_json(model, messages, **kwargs)
        if "【分析维度】" in prompt:
            dimension = prompt.split("【分析维度】id=", 1)[1].split()[0]
            self.dimension_calls[dimension] = self.dimension_calls.get(dimension, 0) + 1
            quote = GOOD if self.repair and self.dimension_calls[dimension] > 1 else self.quote
            result["quotes"] = [copy.deepcopy(quote)]
        elif "risk_points" in result:
            quote = self.quote
            result["risk_points"] = [{"level": "中", "type": "长期", "desc": "后排舒适性需核实",
                "evidence_unit_id": quote.get("unit_id"), "evidence_document_id": quote.get("document_id", ""),
                **({"evidence": quote["text"]} if "text" in quote else {}),
                **({"evidence_source": quote["source"]} if "source" in quote else {})}]
        return result


def install_provider(monkeypatch, module, provider):
    monkeypatch.setattr(module, "build_client", lambda *_args, **_kwargs: provider)


def test_insight_http_restores_original_text_and_source_from_a_numbered_unit(monkeypatch):
    provider = UnitProvider()
    install_provider(monkeypatch, insight, provider)
    body = TestClient(insight.app).post("/analyze", json={"api_key": "offline-key", "mode": "quick", "documents": DOCS}).json()
    assert body["warning"] == ""
    assert len(body["dimensions"]) == 3
    for dimension in body["dimensions"]:
        assert dimension["quotes"] == [{"document_id": "d1", "text": TEXT, "source": "今日头条"}]
        assert dimension["quote_locations"] == [{"document_id": "d1", "field": "content", "start": 0, "end": 17,
                                                "material_class": "direct", "quote_text": TEXT}]
    assert all(count == 1 for count in provider.dimension_calls.values())
    assert any('"id": "d1@0:17"' in prompt and TEXT in prompt for prompt, _ in provider.calls)


@pytest.mark.asyncio
async def test_report_restores_numbered_risk_as_original_external_fields(monkeypatch):
    provider = UnitProvider()
    install_provider(monkeypatch, report, provider)
    req = report.GenerateRequest(api_key="offline-key", documents=DOCS)
    data = await report._llm_insight(req)
    assert data is not None and not req.generation_warning and not data.get("_evidence_warning")
    risk = data["risk_points"][0]
    assert risk["evidence"] == TEXT
    assert risk["evidence_document_id"] == "d1"
    assert risk["evidence_source"] == "今日头条"
    assert risk["evidence_location"] == {"document_id": "d1", "field": "content", "start": 0, "end": 17,
                                         "material_class": "direct", "quote_text": TEXT}
    assert "evidence_unit_id" not in risk
    assert len(provider.calls) == 1


ATTACKS = [
    {**GOOD, "unit_id": "unknown@0:17"},
    {**GOOD, "unit_id": "d2@0:17"},
    {**GOOD, "unit_id": "d1@8000:8017"},
    {**GOOD, "unit_id": None},
    {**GOOD, "unit_id": ["d1@0:17"]},
    {**GOOD, "source": "公众号"},
]


@pytest.mark.parametrize("attack", ATTACKS)
def test_insight_invalid_unit_cannot_be_rescued_by_a_parallel_valid_quote(monkeypatch, attack):
    provider = UnitProvider({"text": TEXT, **attack})
    install_provider(monkeypatch, insight, provider)
    body = TestClient(insight.app).post("/analyze", json={"api_key": "offline-key", "mode": "quick", "documents": DOCS}).json()
    assert body["warning"], "unknown/cross-document/cross-batch/invalid references must remain a failed analysis"
    assert all(not dimension["quotes"] for dimension in body["dimensions"])
    assert all(count == 2 for count in provider.dimension_calls.values())


@pytest.mark.asyncio
@pytest.mark.parametrize("attack", ATTACKS)
async def test_report_invalid_unit_cannot_be_rescued_by_a_parallel_valid_quote(monkeypatch, attack):
    provider = UnitProvider({"text": TEXT, **attack})
    install_provider(monkeypatch, report, provider)
    req = report.GenerateRequest(api_key="offline-key", documents=DOCS)
    data = await report._llm_insight(req)
    assert data is not None
    assert data["risk_points"] == []
    assert data.get("_evidence_warning"), "invalid numbered evidence must still block the Go strict report gate"
    assert len(provider.calls) == 1


def test_numbered_quote_repair_uses_the_existing_two_attempt_budget(monkeypatch):
    provider = UnitProvider({**GOOD, "unit_id": "unknown@0:17"}, repair=True)
    install_provider(monkeypatch, insight, provider)
    body = TestClient(insight.app).post("/analyze", json={"api_key": "offline-key", "mode": "quick", "documents": DOCS}).json()
    assert body["warning"] == ""
    assert all(dimension["quotes"][0]["text"] == TEXT for dimension in body["dimensions"])
    assert all(count == 2 for count in provider.dimension_calls.values())
    assert all(kwargs["max_tokens"] >= 16384 for _, kwargs in provider.calls)


@pytest.mark.asyncio
async def test_numbered_repeated_text_location_stays_in_the_current_original_chunk():
    document = {**DOCS[0], "content": "重" * 400, "_content_start": 8000, "_content_end": 8400, "_material_class": "context"}
    provider = UnitProvider({"document_id": "d1", "unit_id": "d1@8000:8400"})
    data = await insight._analyze_one_dimension(provider, insight.DIMENSIONS[0], [document], "brand", "后排体验")
    assert data["quotes"] == [{"document_id": "d1", "text": "重" * 400, "source": "今日头条"}]
    assert data["quote_locations"][0] == {"document_id": "d1", "field": "content", "start": 8000,
        "end": 8400, "material_class": "context", "quote_text": "重" * 400}


@pytest.mark.asyncio
async def test_title_unit_preserves_title_coordinates_and_exact_whitespace():
    document = {**DOCS[0], "title": "  后排\n实测  "}
    provider = UnitProvider({"document_id": "d1", "unit_id": "d1@title:0:9"})
    data = await insight._analyze_one_dimension(provider, insight.DIMENSIONS[0], [document], "brand", "后排体验")
    assert data["quotes"][0]["text"] == "  后排\n实测  "
    assert data["quote_locations"][0]["field"] == "title"
    assert data["quote_locations"][0]["start"] == 0 and data["quote_locations"][0]["end"] == 9
