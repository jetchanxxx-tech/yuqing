"""Internal source-unit selection through classify_materials; external quotes stay exact.

No new helper is imported before implementation. Current quote-generating entry
will fail the positive cases because evidence_unit_ids have no source lookup.
"""
import asyncio
import copy
import json

import httpx
import pytest

from engines.common import semantic_admission as admission
from engines.common.llm_client import LLMClient


def material_prompt(messages):
    for message in messages:
        if message.get("role") != "user":
            continue
        value = json.loads(message["content"])
        if isinstance(value, dict) and "materials" in value:
            return value
    raise AssertionError("actual model call did not contain its materials prompt")


class UnitJudge:
    """Select only identifiers actually supplied by the production entrypoint."""
    def __init__(self, decide=None):
        self.calls = []
        self.decide = decide

    async def chat_json(self, model, messages, **kwargs):
        captured = {"messages": copy.deepcopy(messages), "kwargs": dict(kwargs), "model": model}
        self.calls.append(captured)
        rows = material_prompt(messages)["materials"]
        if self.decide:
            return self.decide(rows, len(self.calls))
        return {"decisions": [unit_decision(row) for row in rows]}


def unit_decision(row, classification="direct"):
    units = row.get("source_units", [])
    return {"document_id": row["document_id"], "classification": classification,
            "reason": "当前原文片段提供目标对象的实际体验", "matched_entities": [],
            "evidence_unit_ids": [units[0]["id"]] if units else []}


def seen_units(calls):
    result = {}
    for call in calls:
        for row in material_prompt(call["messages"])["materials"]:
            indexed = result.setdefault(row["document_id"], {})
            for unit in row.get("source_units", []):
                prior = indexed.setdefault(unit["id"], unit)
                assert prior == unit, "one source-unit ID changed its text or offsets"
    return {key: list(units.values()) for key, units in result.items()}


def assert_exact_partition(content, units):
    ordered = sorted(units, key=lambda unit: (unit["start"], unit["end"]))
    assert ordered and ordered[0]["start"] == 0
    assert ordered[-1]["end"] == len(content)
    assert len({unit["id"] for unit in ordered}) == len(ordered)
    for unit in ordered:
        assert isinstance(unit["id"], str) and unit["id"]
        assert isinstance(unit["start"], int) and isinstance(unit["end"], int)
        assert 0 < unit["end"] - unit["start"] <= 400
        assert unit["text"] == content[unit["start"]:unit["end"]]
    assert all(left["end"] == right["start"] for left, right in zip(ordered, ordered[1:]))
    assert "".join(unit["text"] for unit in ordered) == content


@pytest.mark.asyncio
async def test_selected_same_document_unit_restores_exact_unicode_original_and_offsets():
    body = "  作者说“iPhone 18 Pro续航很稳”。\n🙂 60\t瓦充电体验不错。\n" * 19 + "\t结尾保留空白。  "
    materials = {"doc-a": body}
    saved = dict(materials)
    judge = UnitJudge()
    result = await admission.classify_materials(judge, "fixture-model", {"topic": "iPhone18系列"}, materials)
    assert result.valid and len(result.accepted) == 1
    decision = result.accepted[0]
    units = seen_units(judge.calls)["doc-a"]
    assert_exact_partition(body, units)
    indexed = {unit["text"]: unit for unit in units}
    assert decision.evidence_spans and all(span in indexed and span in body for span in decision.evidence_spans)
    assert decision.observed_spans[0][0] == 0 and decision.observed_spans[-1][1] == len(body)
    assert materials == saved
    for call in judge.calls:
        for row in material_prompt(call["messages"])["materials"]:
            assert "content" not in row, "raw body must not be duplicated beside source units"
            assert row["source_units"]


@pytest.mark.asyncio
@pytest.mark.parametrize("bad_kind,classification,category", [
    ("unknown", "direct", "evidence_not_in_document"),
    ("cross_document", "direct", "evidence_not_in_document"),
    ("nonarray", "direct", "invalid_evidence_shape"),
    ("nonstring", "direct", "invalid_evidence_shape"),
    ("missing", "direct", "missing_evidence"),
    ("empty", "context", "missing_evidence"),
])
async def test_invalid_unit_references_fail_twice_with_only_known_document_diagnostics(bad_kind, classification, category):
    marker = "untrusted-ref-and-model-text-never-echo"
    def invalid(rows, _attempt):
        decisions = [unit_decision(row) for row in rows]
        first = decisions[0]
        first["classification"] = classification
        first["reason"] = marker
        if bad_kind == "unknown":
            refs = [marker]
            # A fabricated/unknown unit cannot be rescued by supplying the old
            # quote-output field beside it, even when those words are real.
            first["evidence_spans"] = ["iPhone18 Pro原始材料。🙂"]
        elif bad_kind == "cross_document":
            other = rows[1].get("source_units", [])
            refs = [other[0]["id"]] if other else [marker]
        elif bad_kind == "nonarray":
            refs = marker
        elif bad_kind == "nonstring":
            refs = [123]
        elif bad_kind == "missing":
            first.pop("evidence_unit_ids", None)
            refs = None
        else:
            refs = []
        if refs is not None:
            first["evidence_unit_ids"] = refs
        return {"decisions": decisions}
    judge = UnitJudge(invalid)
    with pytest.raises(admission.SemanticAdmissionIncomplete) as captured:
        await admission.classify_materials(judge, "fixture-model", {"topic": "同一主题"},
            {"doc-a": "iPhone18 Pro原始材料。🙂", "doc-b": "雅阁后座另一篇原始材料。"})
    assert len(judge.calls) == 2
    error = captured.value
    assert error.decisions == ()
    assert error.batch_document_ids == ("doc-a", "doc-b")
    assert {"category": category, "document_id": "doc-a"} in error.protocol_errors
    assert all(row.get("document_id") in {None, "doc-a", "doc-b"} for row in error.protocol_errors)
    assert marker not in json.dumps(error.protocol_errors)
    assert marker not in json.dumps(judge.calls[1]["messages"])


@pytest.mark.asyncio
async def test_unit_ids_bind_document_and_unicode_offsets_and_stay_stable_across_batches():
    text = "🙂同一段中文和引号“原文”\n" * 90
    first = UnitJudge()
    second = UnitJudge()
    await admission.classify_materials(first, "fixture-model", {"topic": "主题"}, {"a": text, "b": text})
    # Reordering documents and changing independent metadata must not renumber
    # an immutable document's source positions.
    await admission.classify_materials(second, "fixture-model", {"topic": "主题"}, {"b": text, "a": text},
        material_metadata={"a": {"title": "新的辅助标题"}})
    left, right = seen_units(first.calls), seen_units(second.calls)
    for document_id in ("a", "b"):
        assert_exact_partition(text, left[document_id])
        assert {unit["id"]: unit for unit in left[document_id]} == {unit["id"]: unit for unit in right[document_id]}
    assert {unit["id"] for unit in left["a"]}.isdisjoint({unit["id"] for unit in left["b"]})


@pytest.mark.asyncio
async def test_long_unicode_source_units_cover_every_original_character_across_material_batches():
    materials = {
        "long-a": (" \n中文😀“引号”与iPhone18 Pro的实际文字。\t" * 1800) + "末尾\n ",
        "long-b": ("雅阁后座🙂\n第二排实际反馈‘靠背偏直’。 " * 1600) + "终点\t",
    }
    judge = UnitJudge()
    result = await admission.classify_materials(judge, "fixture-model", {"topic": "关联研究主题"}, materials,
        deadline=asyncio.get_running_loop().time() + 20)
    assert result.valid and len(judge.calls) > 1
    units = seen_units(judge.calls)
    for document_id, text in materials.items():
        assert_exact_partition(text, units[document_id])
    for call in judge.calls:
        assert len(json.dumps(call["messages"], ensure_ascii=False).encode("utf-8")) <= 80_000
        assert all("content" not in row for row in material_prompt(call["messages"])["materials"])
    decisions = {item.document_id: item for item in result.accepted}
    for document_id, text in materials.items():
        observed = sorted(decisions[document_id].observed_spans)
        assert observed[0][0] == 0 and observed[-1][1] == len(text)
        assert all(right[0] <= left[1] for left, right in zip(observed, observed[1:]))


@pytest.mark.asyncio
async def test_complete_serialized_prompt_budget_includes_intent_metadata_units_and_correction():
    attempt_per_batch = {}
    def one_correction(rows, _attempt):
        key = tuple((row["document_id"], tuple(unit["id"] for unit in row.get("source_units", []))) for row in rows)
        attempt_per_batch[key] = attempt_per_batch.get(key, 0) + 1
        decisions = [unit_decision(row) for row in rows]
        if attempt_per_batch[key] == 1:
            for decision in decisions:
                decision["evidence_unit_ids"] = ["unknown-unit"]
        return {"decisions": decisions}
    intent = {"topic": "同一研究主题", "focus": ["不允许静默裁掉的研究范围。" * 1350]}
    materials = {f"doc-{index}": (f"第{index}篇原文😀，包含中文“引号”和实际观察。\n" * 500) for index in range(4)}
    metadata = {key: {"title": "辅助标题" * 120, "content_kind": "body"} for key in materials}
    judge = UnitJudge(one_correction)
    result = await admission.classify_materials(judge, "fixture-model", intent, materials, material_metadata=metadata,
        deadline=asyncio.get_running_loop().time() + 20)
    assert result.valid and len(judge.calls) >= 4
    assert all(count == 2 for count in attempt_per_batch.values())
    for call in judge.calls:
        assert len(json.dumps(call["messages"], ensure_ascii=False).encode("utf-8")) <= 80_000
        assert material_prompt(call["messages"])["research_intent"] == intent
        assert call["kwargs"]["max_tokens"] >= 16384
    for key, text in materials.items():
        assert_exact_partition(text, seen_units(judge.calls)[key])


@pytest.mark.asyncio
async def test_intent_alone_exceeding_prompt_budget_spends_no_provider_call():
    judge = UnitJudge()
    with pytest.raises((admission.SemanticAdmissionIncomplete, ValueError)):
        await admission.classify_materials(judge, "fixture-model", {"topic": "不能丢弃的意图😀" * 20_000},
            {"known": "真实的原文资料，不能用截掉意图来继续收费调用。"})
    assert judge.calls == []


@pytest.mark.asyncio
async def test_ref_correction_keeps_two_attempts_same_phase_deadline_and_supplier_timeout(monkeypatch):
    calls = []
    original = admission.chat_json_bounded
    async def observe(*args, **kwargs):
        calls.append(dict(kwargs))
        return await original(*args, **kwargs)
    monkeypatch.setattr(admission, "chat_json_bounded", observe)
    def recover(rows, attempt):
        decisions = [unit_decision(row) for row in rows]
        if attempt == 1:
            decisions[0]["evidence_unit_ids"] = ["unknown"]
        return {"decisions": decisions}
    judge = UnitJudge(recover)
    started = asyncio.get_running_loop().time()
    result = await admission.classify_materials(judge, "fixture-model", {"topic": "统一主题"}, {"known": "实际原文😀。"})
    assert result.valid and len(calls) == 2
    assert all(call["attempt_timeout"] == 60 for call in calls)
    assert calls[0]["deadline"] == calls[1]["deadline"]
    assert 239 <= calls[0]["deadline"] - started <= 241
    assert calls[0]["attempt_budget"] is calls[1]["attempt_budget"]
    assert calls[0]["attempt_budget"].limit == calls[0]["attempt_budget"].used == 2
    assert [call["kwargs"]["max_tokens"] for call in judge.calls] == [16384, 16384]


@pytest.mark.asyncio
async def test_actual_llm_client_receives_units_and_returns_exact_external_quote_after_one_ref_correction():
    requests = []
    body = "  iPhone18 Pro实际续航“不错”😀。\n"
    def provider(request):
        sent = json.loads(request.content)
        requests.append(sent)
        row = material_prompt(sent["messages"])["materials"][0]
        decision = unit_decision(row)
        if len(requests) == 1:
            decision["evidence_unit_ids"] = ["unknown-unit"]
        content = json.dumps({"decisions": [decision]}, ensure_ascii=False)
        return httpx.Response(200, json={"choices": [{"message": {"content": content}, "finish_reason": "stop"}]})
    client = LLMClient("https://supplier.test/v1", transport=httpx.MockTransport(provider))
    result = await admission.classify_materials(client, "fixture-model", {"topic": "iPhone18系列"}, {"known": body},
        deadline=asyncio.get_running_loop().time() + 3)
    assert result.valid and len(requests) == 2
    assert result.accepted[0].evidence_spans and all(span in body for span in result.accepted[0].evidence_spans)
    assert all(request["max_tokens"] == 16384 for request in requests)
    assert all("attempt_budget" not in request and "attempt_timeout" not in request for request in requests)
    assert all("content" not in row for request in requests for row in material_prompt(request["messages"])["materials"])
