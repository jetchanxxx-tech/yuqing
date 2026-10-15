import pytest

from engines.common.semantic_admission import (
    ADMISSION_VERSION,
    SemanticDecision,
    validate_decisions,
)


def test_semantic_version_and_one_model_variant_can_be_direct():
    result = validate_decisions([
        SemanticDecision(
            "iphone-pro", "direct", "讨论系列型号续航",
            ("iPhone18 Pro",), ("iPhone18 Pro续航体验优秀",),
        ),
    ], {"iphone-pro": "标题\n正文：iPhone18 Pro续航体验优秀"})
    assert ADMISSION_VERSION == "semantic-v2"
    assert result.valid
    assert [item.document_id for item in result.accepted] == ["iphone-pro"]


@pytest.mark.parametrize("bad", [
    [SemanticDecision("unknown", "direct", evidence_spans=("x",))],
    [SemanticDecision("d1", "direct"), SemanticDecision("d1", "context")],
    [SemanticDecision("d1", "made-up", evidence_spans=("正文",))],
    [SemanticDecision("d1", "direct", evidence_spans=("模型臆造",))],
])
def test_invalid_model_protocol_fails_closed(bad):
    result = validate_decisions(bad, {"d1": "真实正文"})
    assert not result.valid
    assert result.accepted == ()


def test_unknown_or_missing_decision_cannot_become_accepted():
    result = validate_decisions(
        [SemanticDecision("d1", "direct", evidence_spans=("真实",))],
        {"d1": "真实正文", "d2": "另一篇正文"},
    )
    assert not result.valid
    assert any(error.startswith("missing_decision:d2") for error in result.errors)


def test_hard_boundary_rejection_is_separate_from_model_relevance():
    result = validate_decisions(
        [SemanticDecision("d1", "direct", evidence_spans=("真实",))],
        {"d1": "真实正文"},
        eligible_ids=set(),
    )
    assert not result.valid
    assert "hard_boundary_violation:d1" in result.errors

@pytest.mark.parametrize("classification", ["direct", "context"])
def test_accepted_decision_requires_reason_and_nonempty_observed_evidence(classification):
    result = validate_decisions([SemanticDecision("d1", classification, reason="", evidence_spans=())], {"d1": "原始正文"})
    assert not result.valid
    assert result.accepted == () and result.context == ()

@pytest.mark.asyncio
async def test_long_material_segments_are_all_observed_with_original_offsets():
    import asyncio
    import json
    from engines.common.semantic_admission import classify_materials
    calls = []
    class Judge:
        async def chat_json(self, model, messages, **kwargs):
            prompt = json.loads(messages[-1]["content"])
            calls.append(prompt["materials"])
            return {"decisions": [{"document_id": row["document_id"], "classification": "direct",
                "reason": "当前片段包含目标材料", "evidence_spans": [row["content"][-20:]]} for row in prompt["materials"]]}
    material = "iPhone18 Pro真实体验内容。" * 6000
    outcome = await classify_materials(Judge(), "fixture-model", {"topic": "iPhone18系列"}, {"long": material},
        deadline=asyncio.get_running_loop().time() + 20)
    assert outcome.valid and len(calls) > 1
    decision = outcome.accepted[0]
    assert decision.observed_spans[0][0] == 0
    assert decision.observed_spans[-1][1] == len(material)
    assert all(left <= previous_end for (_, previous_end), (left, _) in zip(decision.observed_spans, decision.observed_spans[1:]))
    assert all(span in material for span in decision.evidence_spans)

@pytest.mark.parametrize("items,expected", [
    ([{"document_id": "known", "classification": "direct", "reason": "目标体验", "evidence_spans": ["不存在的引文"]}],
     [{"category": "evidence_not_in_document", "document_id": "known"}]),
    ([{"document_id": "known", "classification": "invented-model-class", "reason": "任意模型文本", "evidence_spans": []}],
     [{"category": "invalid_classification", "document_id": "known"}]),
    ([{"document_id": "model-unknown-secret", "classification": "direct", "reason": "任意模型文本", "evidence_spans": []}],
     [{"category": "unknown_document_id"}, {"category": "missing_decision", "document_id": "known"}]),
])
def test_validator_exposes_finite_diagnostics_bound_only_to_known_ids(items, expected):
    import json
    outcome = validate_decisions(items, {"known": "真实的研究材料正文"})
    assert not outcome.valid
    assert list(outcome.protocol_errors) == expected
    serialized = json.dumps(outcome.protocol_errors)
    for raw in ("model-unknown-secret", "invented-model-class", "任意模型文本", "不存在的引文"):
        assert raw not in serialized


@pytest.mark.asyncio
async def test_classifier_exception_preserves_validator_category_and_current_batch_ids():
    import asyncio
    from engines.common.semantic_admission import classify_materials, SemanticAdmissionIncomplete
    class InvalidJudge:
        async def chat_json(self, model, messages, **kwargs):
            return {"decisions": [{"document_id": "known", "classification": "direct", "reason": "研究对象体验",
                                    "evidence_spans": ["臆造文字"]}]}
    with pytest.raises(SemanticAdmissionIncomplete) as captured:
        await classify_materials(InvalidJudge(), "fixture-model", {"topic": "研究主题"}, {"known": "真实正文"},
            deadline=asyncio.get_running_loop().time() + 20)
    assert captured.value.batch_document_ids == ("known",)
    assert list(captured.value.protocol_errors) == [{"category": "evidence_not_in_document", "document_id": "known"}]
    assert str(captured.value) == "semantic_protocol_rejected"

@pytest.mark.parametrize("content,model_span,original_span", [
    ("正文：iPhone\n18\nPro续航实测，使用60\n瓦充电器。", "iPhone 18 Pro续航实测，使用60 瓦充电器。",
     "iPhone\n18\nPro续航实测，使用60\n瓦充电器。"),
    ("作者说“续航很稳”，也认为‘手感不错’。", '作者说"续航很稳"，也认为\'手感不错\'。',
     "作者说“续航很稳”，也认为‘手感不错’。"),
    ("正文：iPhone\n18 Pro让作者称之为“续航惊喜”。", 'iPhone 18 Pro让作者称之为"续航惊喜"。',
     "iPhone\n18 Pro让作者称之为“续航惊喜”。"),
])
def test_format_only_evidence_is_restored_to_exact_contiguous_original_body(content, model_span, original_span):
    materials = {"known": content}
    saved = dict(materials)
    result = validate_decisions([SemanticDecision("known", "direct", "正文直接讨论研究对象",
                                                evidence_spans=(model_span,))], materials)
    assert result.valid, result.errors
    assert result.accepted[0].evidence_spans == (original_span,)
    assert original_span in content
    assert materials == saved  # Restoration never rewrites saved content.


@pytest.mark.parametrize("model_span", [
    "iPhone18 Pro使用80瓦充电器，作者称之为“续航惊喜”。",  # Changed number.
    "iPhone18 Pro使用60瓦充电器，作者称之为“充电惊喜”。",  # Changed word.
    'iPhone18 Pro使用60瓦充电器,作者称之为"续航惊喜"。',  # Punctuation is not interchangeable.
    "iPhone18 Pro使用60瓦充电器作者称之为续航惊喜",  # Arbitrary punctuation removal.
    "iPhone18 Pro续航惊喜",  # Omitted interior text / stitched fragments.
    '"iPhone18 Pro使用60瓦充电器，作者称之为“续航惊喜”。"',  # Added quote wrappers.
])
def test_restoration_never_changes_words_numbers_punctuation_or_stitches_fragments(model_span):
    content = "iPhone18 Pro使用60瓦充电器，作者称之为“续航惊喜”。"
    result = validate_decisions([SemanticDecision("known", "direct", "模型给了候选引文",
                                                evidence_spans=(model_span,))], {"known": content})
    assert not result.valid
    assert result.accepted == ()
    assert result.protocol_errors == ({"category": "evidence_not_in_document", "document_id": "known"},)


def test_format_restoration_never_borrows_quote_from_another_document():
    result = validate_decisions([
        SemanticDecision("one", "direct", "不能错绑证据", evidence_spans=('作者称iPhone 18 Pro为"续航惊喜"。',)),
        SemanticDecision("two", "irrelevant", "另一个文档不能给前文提供引文"),
    ], {"one": "雅阁后座坐垫偏软，长途乘坐舒服。", "two": "作者称iPhone\n18\nPro为“续航惊喜”。"})
    assert not result.valid
    assert result.protocol_errors == ({"category": "evidence_not_in_document", "document_id": "one"},)

@pytest.mark.asyncio
@pytest.mark.parametrize("first_failure", ["missing_decision", "rewritten_quote"])
async def test_one_protocol_correction_can_complete_without_resetting_deadline_or_output_budget(first_failure):
    import asyncio
    import copy
    import json
    from engines.common.semantic_admission import classify_materials
    from engines.common.model_budget import AttemptBudget
    calls = []
    marker = "untrusted-model-text-must-not-enter-correction"
    class CorrectingJudge:
        async def chat_json(self, model, messages, **kwargs):
            calls.append({"messages": copy.deepcopy(messages), "kwargs": dict(kwargs)})
            if len(calls) == 1:
                if first_failure == "missing_decision":
                    return {"decisions": []}
                return {"decisions": [{"document_id": "known", "classification": "direct", "reason": marker,
                                       "evidence_spans": [marker]}]}
            return {"decisions": [{"document_id": "known", "classification": "direct", "reason": "目标实际体验",
                                   "evidence_spans": ["iPhone18 Pro真实续航体验"]}]}
    deadline = asyncio.get_running_loop().time() + 10
    result = await classify_materials(CorrectingJudge(), "fixture-model", {"topic": "iPhone18系列"},
                                     {"known": "iPhone18 Pro真实续航体验"}, deadline=deadline)
    assert result.valid and result.accepted[0].document_id == "known"
    assert len(calls) == 2
    assert all(call["kwargs"]["max_tokens"] >= 16384 for call in calls)
    correction = json.loads(calls[1]["messages"][-1]["content"])
    assert correction["protocol_correction"]["batch_document_ids"] == ["known"]
    expected_category = "missing_decision" if first_failure == "missing_decision" else "evidence_not_in_document"
    assert correction["protocol_correction"]["errors"] == [{"category": expected_category, "document_id": "known"}]
    rules = " ".join(correction["protocol_correction"]["rules"])
    assert "JSON" in rules and "decisions" in rules and "uncertain" in rules
    assert "原文" in rules or "verbatim" in rules
    assert marker not in json.dumps(calls[1]["messages"])


@pytest.mark.asyncio
async def test_persistent_bad_protocol_stops_after_two_and_keeps_final_safe_error():
    import asyncio
    from engines.common.semantic_admission import classify_materials, SemanticAdmissionIncomplete
    calls = []
    class InvalidJudge:
        async def chat_json(self, model, messages, **kwargs):
            calls.append(messages)
            return {"decisions": [{"document_id": "known", "classification": "direct", "reason": "任意无关模型文字",
                                   "evidence_spans": ["不在原文的错误证据"]}]}
    with pytest.raises(SemanticAdmissionIncomplete) as captured:
        await classify_materials(InvalidJudge(), "fixture-model", {"topic": "研究主题"}, {"known": "真实正文"},
                                 deadline=asyncio.get_running_loop().time() + 10)
    assert len(calls) == 2
    assert captured.value.protocol_errors == ({"category": "evidence_not_in_document", "document_id": "known"},)
    assert captured.value.batch_document_ids == ("known",)
    assert captured.value.decisions == ()


@pytest.mark.asyncio
async def test_transport_truncation_and_bad_protocol_share_two_actual_supplier_calls_total():
    import asyncio
    import json
    import httpx
    from engines.common.llm_client import LLMClient
    from engines.common.semantic_admission import classify_materials, SemanticAdmissionIncomplete
    calls = []
    def provider(request):
        calls.append(json.loads(request.content))
        response = {"decisions": [{"document_id": "known", "classification": "direct", "reason": "目标体验",
                                    "evidence_spans": ["修改了原文的假引文"]}]}
        return httpx.Response(200, json={"choices": [{"message": {"content": json.dumps(response)},
            "finish_reason": "length" if len(calls) == 1 else "stop"}]})
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    with pytest.raises(SemanticAdmissionIncomplete) as captured:
        await classify_materials(client, "fixture-model", {"topic": "研究主题"}, {"known": "真实正文"},
                                 deadline=asyncio.get_running_loop().time() + 10)
    assert len(calls) == 2
    assert [request["max_tokens"] for request in calls] == [16384, 32768]
    assert captured.value.protocol_errors == ({"category": "evidence_not_in_document", "document_id": "known"},)
    assert all("attempt_budget" not in request for request in calls)


@pytest.mark.asyncio
async def test_protocol_correction_preserves_previously_verified_batch_on_final_failure():
    import asyncio
    import json
    from engines.common.semantic_admission import classify_materials, SemanticAdmissionIncomplete
    calls = []
    materials = {f"doc-{index}": f"第{index}条实际原文，记录研究对象的具体体验。" for index in range(13)}
    class LastBatchFails:
        async def chat_json(self, model, messages, **kwargs):
            calls.append(messages)
            first_prompt = json.loads(messages[1]["content"])
            rows = first_prompt["materials"]
            return {"decisions": [{"document_id": row["document_id"], "classification": "direct", "reason": "原文体验",
                 "evidence_spans": [row["content"] if len(rows) == 12 else "最后一批错误改写引文"]} for row in rows]}
    with pytest.raises(SemanticAdmissionIncomplete) as captured:
        await classify_materials(LastBatchFails(), "fixture-model", {"topic": "研究主题"}, materials,
                                 deadline=asyncio.get_running_loop().time() + 10)
    assert len(calls) == 3  # First batch succeeds once; last batch gets at most two.
    assert {item.document_id for item in captured.value.decisions} == {f"doc-{index}" for index in range(12)}
    assert captured.value.batch_document_ids == ("doc-12",)
    assert captured.value.protocol_errors == ({"category": "evidence_not_in_document", "document_id": "doc-12"},)
    assert set(captured.value.observed) == {f"doc-{index}" for index in range(12)}


@pytest.mark.asyncio
async def test_protocol_correction_never_extends_original_expired_deadline():
    import asyncio
    from engines.common.semantic_admission import classify_materials, SemanticAdmissionIncomplete
    calls = []
    loop = asyncio.get_running_loop()
    deadline = loop.time() + 0.03
    class SlowFirstProtocol:
        async def chat_json(self, model, messages, **kwargs):
            calls.append(messages)
            await asyncio.sleep(0.06)
            return {"decisions": []}
    with pytest.raises(SemanticAdmissionIncomplete):
        await classify_materials(SlowFirstProtocol(), "fixture-model", {"topic": "研究主题"}, {"known": "真实正文"}, deadline=deadline)
    assert len(calls) == 1

@pytest.mark.asyncio
async def test_semantic_attempt_timeout_recovers_stalled_real_stream_inside_original_phase_deadline(monkeypatch):
    import asyncio
    import json
    import httpx
    from engines.common.llm_client import LLMClient
    from engines.common import semantic_admission as module
    monkeypatch.setattr(module, "ATTEMPT_TIMEOUT_SECONDS", 0.05, raising=False)
    requests = []
    never = asyncio.Event()
    closed = []
    class StalledStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            try:
                yield ("data: " + json.dumps({"id": "stalled-first-provider-id", "model": "fixture-model",
                    "choices": [{"delta": {"reasoning_content": "still thinking"}, "finish_reason": None}]}) + "\n\n").encode()
                await never.wait()
            finally:
                closed.append(True)
    def provider(request):
        requests.append(json.loads(request.content))
        if len(requests) == 1:
            return httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=StalledStream())
        result = {"decisions": [{"document_id": "known", "classification": "direct", "reason": "目标实际体验",
                                 "evidence_spans": ["iPhone18 Pro真实续航体验"]}]}
        return httpx.Response(200, json={"choices": [{"message": {"content": json.dumps(result)}, "finish_reason": "stop"}]})
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    loop = asyncio.get_running_loop()
    deadline = loop.time() + 0.8
    result = await module.classify_materials(client, "fixture-model", {"topic": "iPhone18系列"},
        {"known": "iPhone18 Pro真实续航体验"}, deadline=deadline)
    assert result.valid and result.accepted[0].document_id == "known"
    assert len(requests) == 2
    assert [request["max_tokens"] for request in requests] == [16384, 16384]
    assert loop.time() < deadline
    assert closed == [True]
    assert not never.is_set()


@pytest.mark.asyncio
async def test_stalled_stream_then_bad_quote_spends_two_total_calls_and_preserves_safe_protocol_error(monkeypatch):
    import asyncio
    import json
    import httpx
    from engines.common.llm_client import LLMClient
    from engines.common import semantic_admission as module
    monkeypatch.setattr(module, "ATTEMPT_TIMEOUT_SECONDS", 0.05, raising=False)
    requests = []
    never = asyncio.Event()
    closed = []
    marker = "do-not-replay-private-model-text"
    class StalledStream(httpx.AsyncByteStream):
        async def __aiter__(self):
            try:
                yield ("data: " + json.dumps({"id": "stalled-first-provider-id", "model": "fixture-model", "choices": []}) + "\n\n").encode()
                await never.wait()
            finally:
                closed.append(True)
    def provider(request):
        requests.append(json.loads(request.content))
        if len(requests) == 1:
            return httpx.Response(200, headers={"content-type": "text/event-stream"}, stream=StalledStream())
        result = {"decisions": [{"document_id": "known", "classification": "direct", "reason": marker,
                                 "evidence_spans": [marker]}]}
        return httpx.Response(200, json={"choices": [{"message": {"content": json.dumps(result)}, "finish_reason": "stop"}]})
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    loop = asyncio.get_running_loop()
    deadline = loop.time() + 0.8
    with pytest.raises(module.SemanticAdmissionIncomplete) as captured:
        await module.classify_materials(client, "fixture-model", {"topic": "iPhone18系列"},
            {"known": "iPhone18 Pro真实续航体验"}, deadline=deadline)
    assert len(requests) == 2
    assert [request["max_tokens"] for request in requests] == [16384, 16384]
    assert captured.value.protocol_errors == ({"category": "evidence_not_in_document", "document_id": "known"},)
    assert captured.value.batch_document_ids == ("known",)
    assert captured.value.decisions == ()
    assert marker not in json.dumps(captured.value.protocol_errors)
    assert loop.time() < deadline
    assert closed == [True]
    assert not never.is_set()
