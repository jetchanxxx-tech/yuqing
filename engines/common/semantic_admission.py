"""Versioned semantic evidence decisions and fail-closed validation."""

from dataclasses import dataclass, field
from typing import Iterable, Mapping


ADMISSION_VERSION = "semantic-v2"
DECISION_TYPES = frozenset({"direct", "context", "irrelevant", "uncertain"})


@dataclass(frozen=True)
class SemanticDecision:
    document_id: str
    classification: str
    reason: str = ""
    matched_entities: tuple[str, ...] = ()
    evidence_spans: tuple[str, ...] = ()


@dataclass(frozen=True)
class DecisionValidation:
    accepted: tuple[SemanticDecision, ...]
    context: tuple[SemanticDecision, ...]
    rejected: tuple[SemanticDecision, ...]
    errors: tuple[str, ...] = field(default_factory=tuple)

    @property
    def valid(self) -> bool:
        return not self.errors


def validate_decisions(
    decisions: Iterable[SemanticDecision | Mapping],
    materials: Mapping[str, str],
    eligible_ids: set[str] | None = None,
) -> DecisionValidation:
    """Validate model decisions against the exact material batch.

    Unknown/duplicate IDs, missing decisions, unsupported classes, and quotes
    absent from the supplied material fail closed. A caller must not turn an
    invalid response into an accepted document.
    """
    normalized: list[SemanticDecision] = []
    errors: list[str] = []
    seen: set[str] = set()
    for item in decisions:
        if isinstance(item, SemanticDecision):
            decision = item
        elif isinstance(item, Mapping):
            raw_spans = item.get("evidence_spans") or item.get("evidence") or []
            raw_entities = item.get("matched_entities") or []
            if isinstance(raw_spans, str):
                raw_spans = [raw_spans]
            if isinstance(raw_entities, str):
                raw_entities = [raw_entities]
            decision = SemanticDecision(
                document_id=str(item.get("document_id") or ""),
                classification=str(item.get("classification") or ""),
                reason=str(item.get("reason") or ""),
                matched_entities=tuple(str(x) for x in raw_entities if x is not None),
                evidence_spans=tuple(str(x) for x in raw_spans if x is not None),
            )
        else:
            errors.append("decision_not_object")
            continue
        if not decision.document_id or decision.document_id not in materials:
            errors.append(f"unknown_document_id:{decision.document_id}")
            continue
        if decision.document_id in seen:
            errors.append(f"duplicate_document_id:{decision.document_id}")
            continue
        seen.add(decision.document_id)
        if decision.classification not in DECISION_TYPES:
            errors.append(f"invalid_classification:{decision.document_id}")
            continue
        text = materials[decision.document_id]
        if any(not span or span not in text for span in decision.evidence_spans):
            errors.append(f"evidence_not_in_document:{decision.document_id}")
            continue
        if eligible_ids is not None and decision.document_id not in eligible_ids:
            errors.append(f"hard_boundary_violation:{decision.document_id}")
            continue
        normalized.append(decision)

    missing = set(materials) - seen
    errors.extend(f"missing_decision:{document_id}" for document_id in sorted(missing))
    if errors:
        return DecisionValidation((), (), (), tuple(errors))
    return DecisionValidation(
        accepted=tuple(x for x in normalized if x.classification == "direct"),
        context=tuple(x for x in normalized if x.classification == "context"),
        rejected=tuple(x for x in normalized if x.classification in {"irrelevant", "uncertain"}),
    )


async def classify_materials(llm, model: str, intent: Mapping, materials: Mapping[str, str],
                             usage_context: dict | None = None, timeout: float | None = None) -> DecisionValidation:
    """Ask an OpenAI-compatible model for bounded evidence decisions."""
    import asyncio
    import json

    prompt = {
        "research_intent": intent,
        "materials": [{"document_id": key, "content": value} for key, value in materials.items()],
        "output": {
            "decisions": [
                {"document_id": "", "classification": "direct|context|irrelevant|uncertain",
                 "reason": "", "matched_entities": [], "evidence_spans": []}
            ]
        },
        "rules": [
            "All keywords describe one research topic; one model variant can be direct evidence.",
            "Use only supplied material. Do not infer publication, identity, or truth from memory.",
            "Every evidence span must be copied verbatim from its document.",
            "Return exactly one decision for every document id.",
        ],
    }
    kwargs = {"temperature": 0, "max_tokens": 16384}
    if timeout is not None:
        response = await asyncio.wait_for(
            llm.chat_json(model, [
                {"role": "system", "content": "你是证据准入审查员，只输出JSON对象。"},
                {"role": "user", "content": json.dumps(prompt, ensure_ascii=False)},
            ], usage_context=usage_context, **kwargs), timeout=timeout)
    else:
        response = await llm.chat_json(model, [
            {"role": "system", "content": "你是证据准入审查员，只输出JSON对象。"},
            {"role": "user", "content": json.dumps(prompt, ensure_ascii=False)},
        ], usage_context=usage_context, **kwargs)
    return validate_decisions(response.get("decisions", []) if isinstance(response, dict) else [], materials)
