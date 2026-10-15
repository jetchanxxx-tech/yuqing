"""Report writing uses verified bounded effort without shrinking output budgets."""
import copy
import json

import httpx
import pytest

from engines.common.llm_client import LLMClient
from engines.report_engine import main as report
from engines.tests.test_downstream_evidence_units import DOCS, TEXT
from engines.tests.test_report import DEFAULT_LLM_PAYLOAD
from engines.tests.test_usage_bridge import billing_sandbox


@pytest.mark.asyncio
@pytest.mark.parametrize("model", ["GLM-5.3-Flash", "glm-5.3", "glm-5.3-flashx"])
async def test_report_glm_effort_remains_bounded_through_full_output_recovery(monkeypatch, billing_sandbox, model):
    requests = []

    def provider(request):
        body = json.loads(request.content)
        requests.append(body)
        payload = copy.deepcopy(DEFAULT_LLM_PAYLOAD)
        payload["risk_points"][0] = {"level": "中", "type": "长期", "desc": "后排舒适性需核实",
                                    "evidence_unit_id": "d1@0:17", "evidence_document_id": "d1"}
        return httpx.Response(200, json={"model": body["model"], "id": "offline-report-" + str(len(requests)),
            "choices": [{"finish_reason": "length" if len(requests) == 1 else "stop",
                         "message": {"content": json.dumps(payload, ensure_ascii=False)}}],
            "usage": {"prompt_tokens": 100, "completion_tokens": 50}})

    client = LLMClient("https://offline.invalid/v1", transport=httpx.MockTransport(provider))
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: client)
    req = report.GenerateRequest(api_key="offline-key", run_id="accepted-run", llm_model=model, documents=DOCS)
    result = await report._llm_insight(req)
    assert result is not None and not req.generation_warning
    assert result["risk_points"][0]["evidence"] == TEXT
    assert len(requests) == 2
    assert [body["max_tokens"] for body in requests] == [16384, 32768]
    assert all(body.get("thinking") == {"type": "enabled"} and body.get("reasoning_effort") == "low" for body in requests)


@pytest.mark.asyncio
@pytest.mark.parametrize("model", ["deepseek-v4.1-flash", "MiniMax-M2.5", "glm-4-plus"])
async def test_report_does_not_send_glm_specific_effort_fields_to_other_models(monkeypatch, billing_sandbox, model):
    requests = []

    def provider(request):
        body = json.loads(request.content)
        requests.append(body)
        return httpx.Response(200, json={"model": body["model"], "id": "offline-report-compatibility",
            "choices": [{"finish_reason": "stop", "message": {"content": json.dumps(DEFAULT_LLM_PAYLOAD, ensure_ascii=False)}}],
            "usage": {"prompt_tokens": 100, "completion_tokens": 50}})

    client = LLMClient("https://offline.invalid/v1", transport=httpx.MockTransport(provider))
    monkeypatch.setattr(report, "build_client", lambda *_args, **_kwargs: client)
    req = report.GenerateRequest(api_key="offline-key", run_id="accepted-run", llm_model=model,
                                 documents=[{"id": "d1", "content": "后排腿部空间局促。", "source_name": "汽车之家"}])
    result = await report._llm_insight(req)
    assert result is not None and not req.generation_warning
    assert len(requests) == 1 and requests[0]["max_tokens"] == 16384
    assert "thinking" not in requests[0] and "reasoning_effort" not in requests[0]
