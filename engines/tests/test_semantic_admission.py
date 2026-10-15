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
