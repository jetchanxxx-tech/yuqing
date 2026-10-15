"""Actual engine entrypoints must distinguish background and account for every text span."""
import json

import pytest
from fastapi.testclient import TestClient

from engines.insight_engine import main as insight
from engines.report_engine import main as report


class ReadingProvider:
    def __init__(self):
        self.calls = []

    async def chat_json(self, model, messages, **kwargs):
        prompt = messages[-1]["content"]
        self.calls.append((prompt, kwargs))
        if "文档列表（JSON）" in prompt:
            docs = json.JSONDecoder().raw_decode(prompt.split("文档列表（JSON）：", 1)[1].lstrip())[0]
            return {"sentiments": [{"document_id": d["id"], "sentiment": "neutral", "score": 0.5} for d in docs],
                    "topics": [{"id": "topic", "name": "新品体验", "keywords": ["续航"], "doc_count": len(docs), "doc_ids": [d["id"] for d in docs]}]}
        if "【分析维度】" in prompt:
            return {"findings": "本批直接证据显示续航讨论。", "data_points": [], "quotes": [], "deep_read": "背景不计为新品评价。", "trend": "仅基于材料"}
        if "revised_summary" in prompt:
            return {"revised_summary": "直接证据与背景材料已分开处理。"}
        return {"executive_summary": "报告仅统计直接证据。", "risk_points": [], "topic_analyses": [], "recommendations": []}


def _docs(count=44):
    return [{"id": f"d{i}", "title": f"iPhone18 Pro体验{i}", "source_type": "weibo" if i % 2 else "wechat",
             "content": f"唯一正文标记{i}：续航体验。", "published_at": "2026-10-10T12:00:00Z", "url": f"https://example.invalid/{i}"} for i in range(count)]


def _assert_span_coverage(coverage, phase, documents):
    item = next(row for row in coverage["phases"] if row["phase"] == phase)
    assert item["unprocessed"] == []
    for doc in documents:
        spans = sorted((span["start"], span["end"]) for span in item["read_spans"] if span["document_id"] == doc["id"])
        cursor = 0
        for start, end in spans:
            assert start <= cursor, f"unread gap in {doc['id']}"
            cursor = max(cursor, end)
        assert cursor == len(doc["content"]), f"silently omitted {doc['id']}"


def test_insight_http_reads_all_direct_spans_and_excludes_context_from_classification(monkeypatch):
    provider = ReadingProvider()
    monkeypatch.setattr(insight, "build_client", lambda *_args, **_kwargs: provider)
    docs = _docs()
    docs[0]["content"] = "长文正文。" * 5000 + "末尾唯一续航结论"
    background = {"id": "background-old-model", "title": "旧款比较", "content": "旧款电池负面体验", "source_type": "wechat"}
    intent = {"version": "intent-v1", "topic": "iPhone18系列续航", "focus_points": ["续航"]}
    response = TestClient(insight.app).post("/analyze", json={"api_key": "offline-key", "mode": "quick", "documents": docs,
        "context_documents": [background], "research_intent": intent})
    assert response.status_code == 200
    body = response.json()
    assert body["warning"] == ""
    assert len(body["sentiments"]) == len(docs)
    classified = [prompt for prompt, _ in provider.calls if "文档列表（JSON）" in prompt]
    assert all("background-old-model" not in prompt for prompt in classified)
    assert any("末尾唯一续航结论" in prompt for prompt in classified)
    dimensions = [prompt for prompt, _ in provider.calls if "【分析维度】" in prompt]
    assert any("iPhone18系列续航" in prompt for prompt in dimensions)
    assert any("background-old-model" in prompt and "背景" in prompt for prompt in dimensions)
    _assert_span_coverage(body["material_coverage"], "classification", docs)
    for phase in ("dimension:heat", "dimension:sentiment", "dimension:deep_cause"):
        _assert_span_coverage(body["material_coverage"], phase, [*docs, background])
    assert all(kwargs.get("max_tokens", 0) >= 16384 for _, kwargs in provider.calls)


def test_report_http_reads_beyond_old_document_cap_and_renders_context_separately(monkeypatch):
    provider = ReadingProvider()
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: provider)
    docs = _docs()
    docs[0]["content"] = "长文正文。" * 5000 + "报告末尾唯一标记"
    background = {"id": "old-model-context", "title": "旧款比较背景", "content": "旧款体验不计入新品统计", "source_type": "wechat"}
    response = TestClient(report.app).post("/generate", json={"api_key": "offline-key", "documents": docs,
        "context_documents": [background], "research_intent": {"topic": "iPhone18系列续航"}})
    assert response.status_code == 200
    body = response.json()
    assert any("唯一正文标记43" in prompt for prompt, _ in provider.calls)
    assert any("报告末尾唯一标记" in prompt for prompt, _ in provider.calls)
    assert any("iPhone18系列续航" in prompt for prompt, _ in provider.calls)
    assert "背景／比较材料（不计入目标评价统计）" in body["content"]
    assert "旧款比较背景" in body["content"]
    _assert_span_coverage(body["material_coverage"], "report", [*docs, background])
    assert all(kwargs.get("max_tokens", 0) >= 16384 for _, kwargs in provider.calls)


def test_report_context_sentiment_cannot_enter_target_statistics(monkeypatch):
    provider = ReadingProvider()
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: provider)
    response = TestClient(report.app).post("/generate", json={"api_key": "offline-key", "documents": _docs(2),
        "context_documents": [{"id": "old-model", "title": "旧款背景", "content": "旧款好评"}],
        "sentiments": [{"document_id": "d0", "sentiment": "negative", "score": 0.9},
                       {"document_id": "old-model", "sentiment": "positive", "score": 0.8}]})
    assert response.status_code == 200
    assert "正面 0 篇" in provider.calls[0][0]
    assert "负面 1 篇" in provider.calls[0][0]
    assert "共 1 篇文档" in provider.calls[0][0]


def test_dimension_quote_location_points_to_the_read_long_article_chunk(monkeypatch):
    class LocatedProvider(ReadingProvider):
        async def chat_json(self, model, messages, **kwargs):
            result = await super().chat_json(model, messages, **kwargs)
            prompt = messages[-1]["content"]
            if "【分析维度】" in prompt and "结束标记" in prompt:
                result["quotes"] = [{"document_id": "d0", "text": "相同表达", "source": "wechat"}]
            return result

    provider = LocatedProvider()
    monkeypatch.setattr(insight, "build_client", lambda *_args, **_kwargs: provider)
    docs = _docs(2)
    docs[0]["content"] = "相同表达。" + "正文。" * 4000 + "相同表达。结束标记"
    expected_start = docs[0]["content"].rfind("相同表达")
    response = TestClient(insight.app).post("/analyze", json={"api_key": "offline-key", "mode": "quick", "documents": docs})
    assert response.status_code == 200
    assert response.json()["warning"] == ""
    for dimension in response.json()["dimensions"]:
        assert dimension["quote_locations"][0]["start"] == expected_start
        assert dimension["quote_locations"][0]["end"] == expected_start + len("相同表达")


@pytest.mark.parametrize("invalid", [{}, {"executive_summary": "", "topic_analyses": [], "risk_points": [], "recommendations": []},
    {"executive_summary": "只有摘要而无完整报告结构"}])
def test_report_empty_or_incomplete_model_output_is_not_marked_read_or_complete(monkeypatch, invalid):
    class EmptyReportProvider(ReadingProvider):
        async def chat_json(self, model, messages, **kwargs):
            self.calls.append((messages[-1]["content"], kwargs))
            return invalid

    provider = EmptyReportProvider()
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: provider)
    response = TestClient(report.app).post("/generate", json={"api_key": "offline-key", "documents": _docs(2)})
    assert response.status_code == 200
    body = response.json()
    assert body["warning"], "empty JSON was accepted as a completed report"
    phase = body["material_coverage"]["phases"][0]
    assert phase["read_spans"] == []
    assert len(phase["unprocessed"]) == 2
    assert "AI 研判不可用" in body["content"]
    assert len(provider.calls) <= 2, "schema recovery has its own unbounded attempt counter"


def test_dimension_quote_recovery_shares_prior_truncation_attempt_budget(monkeypatch):
    class TruncatedThenInventedProvider(ReadingProvider):
        def __init__(self):
            super().__init__()
            self.heat_calls = 0

        async def chat_json(self, model, messages, **kwargs):
            prompt = messages[-1]["content"]
            if "【分析维度】id=heat" in prompt:
                self.heat_calls += 1
                if self.heat_calls == 1:
                    from engines.common.llm_client import LLMOutputTruncated
                    raise LLMOutputTruncated("output truncated")
                return {"findings": "材料有续航争议", "data_points": [],
                        "quotes": [{"document_id": "d0", "text": "原文根本没有的引语", "source": "wechat"}],
                        "deep_read": "不得采用虚构引语", "trend": "未核实"}
            return await super().chat_json(model, messages, **kwargs)

    provider = TruncatedThenInventedProvider()
    monkeypatch.setattr(insight, "build_client", lambda *_args, **_kwargs: provider)
    response = TestClient(insight.app).post("/analyze", json={"api_key": "offline-key", "mode": "quick", "documents": _docs(2)})
    assert response.status_code == 200
    body = response.json()
    assert provider.heat_calls == 2, "quote retry restarted an already exhausted supplier budget"
    assert body["warning"]
    assert all(not dimension["quotes"] for dimension in body["dimensions"])
