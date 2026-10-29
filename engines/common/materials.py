"""Bounded batches with complete, original-text span accounting.

Characters are Python Unicode code points, measured against the stored content;
neither whitespace trimming nor generated summaries change that coordinate space.
"""
from collections import deque
import json

from engines.common.evidence_units import document_units


CHUNK_CHARS = 8000
BATCH_CHARS = 16000


def material_batches(documents: list[dict], context_documents: list[dict] = ()) -> list[list[dict]]:
    sources: dict[str, list[deque]] = {}
    for material_class, docs in (("direct", documents), ("context", context_documents)):
        for document in docs:
            if not isinstance(document, dict):
                continue
            content = str(document.get("content") or "")
            pieces = deque()
            for start in range(0, max(1, len(content)), CHUNK_CHARS):
                end = min(start + CHUNK_CHARS, len(content))
                pieces.append({**document, "content": content[start:end], "_content_start": start,
                               "_content_end": end, "_material_class": material_class})
            source = str(document.get("source_type") or document.get("source_name") or "unknown")
            sources.setdefault(source, []).append(pieces)

    # First give every platform and every document a turn, then read long tails.
    queues = deque(deque(documents) for documents in sources.values())
    ordered = []
    while queues:
        platform = queues.popleft()
        document = platform.popleft()
        ordered.append(document.popleft())
        if document:
            platform.append(document)
        if platform:
            queues.append(platform)

    batches, current, count, ids = [], [], 0, set()
    for item in ordered:
        size = len(materials_text([item]))
        doc_id = str(item.get("id", ""))
        if current and (count + size > BATCH_CHARS or doc_id in ids):
            batches.append(current)
            current, count, ids = [], 0, set()
        current.append(item)
        count += size
        ids.add(doc_id)
    if current:
        batches.append(current)
    return batches


def materials_text(documents: list[dict], *, numbered: bool = False) -> str:
    parts = []
    for document in documents:
        content = str(document.get("content") or "")
        start = document.get("_content_start", 0)
        end = document.get("_content_end", len(content))
        material_class = document.get("_material_class", document.get("semantic_classification", "direct"))
        label = "背景／比较材料，不计入目标评价统计" if material_class == "context" else "直接证据"
        completeness = "保存文本达到上限，后续原文未保存、未核验" if document.get("truncated") else "仅代表本次可用的保存文本，不保证原网页完整"
        parts.append(
            f"id={document.get('id', '')} 标题：{document.get('title', '')}\n"
            f"来源：{document.get('source_name') or document.get('source_type', '')} | {document.get('published_at', '')}\n"
            f"材料类别：{label}；内容类型：{document.get('content_kind') or '历史材料'}；原文片段[{start}:{end}]\n"
            f"保存范围：{completeness}\n"
            + ("编号原文 source_units：" + json.dumps(list(document_units(document)), ensure_ascii=False)
               if numbered else f"正文：{content}")
        )
    return "\n\n".join(parts) or "（无文档素材）"


def span_for(document: dict) -> dict:
    return {"document_id": str(document.get("id", "")),
            "start": document.get("_content_start", 0),
            "end": document.get("_content_end", len(str(document.get("content") or ""))),
            "material_class": document.get("_material_class", "direct")}


class MaterialCoverage:
    def __init__(self):
        self.phases: dict[str, dict] = {}

    def prepare(self, phase: str, batches: list[list[dict]]):
        self.phases.setdefault(phase, {"expected": [span_for(d) for batch in batches for d in batch],
                                       "read_spans": [], "failures": {}})

    def read(self, phase: str, batch: list[dict]):
        entry = self.phases[phase]
        for document in batch:
            span = span_for(document)
            if span not in entry["read_spans"]:
                entry["read_spans"].append(span)

    def fail(self, phase: str, batch: list[dict], reason: str, *, code: str = ""):
        if code:
            self.phases[phase]["failure_code"] = code
        for document in batch:
            span = span_for(document)
            self.phases[phase]["failures"][(span["document_id"], span["start"], span["end"])] = {
                "reason": reason, **({"code": code} if code else {})}

    def result(self) -> dict:
        phases = []
        for phase, entry in self.phases.items():
            missing = []
            for span in entry["expected"]:
                if span not in entry["read_spans"]:
                    key = (span["document_id"], span["start"], span["end"])
                    missing.append({**span, **entry["failures"].get(key, {"reason": "阶段时限内未处理"})})
            phases.append({"phase": phase, "read_spans": entry["read_spans"], "unprocessed": missing,
                           **({"failure_code": entry["failure_code"]} if entry.get("failure_code") else {})})
        return {"version": "complete-chunks-v1", "phases": phases}


def quote_location(quote: dict, documents: list[dict]) -> dict | None:
    """Locate a previously validated quote without inventing offsets in a summary."""
    from engines.common.evidence import normalize_quote

    needle = normalize_quote(str(quote.get("text", "")))
    for document in documents:
        if document.get("id") != quote.get("document_id"):
            continue
        for field in ("content", "title"):
            value = str(document.get(field) or "")
            positions = [i for i, char in enumerate(value) if not char.isspace()]
            normalized = "".join(value[i] for i in positions)
            start = normalized.find(needle)
            if needle and start >= 0:
                offset = document.get("_content_start", 0) if field == "content" else 0
                return {"document_id": document["id"], "field": field,
                        "start": positions[start] + offset, "end": positions[start + len(needle) - 1] + 1 + offset,
                        "material_class": document.get("_material_class", document.get("semantic_classification", "direct")),
                        "quote_text": quote["text"]}
    return None


def separate_materials(documents: list[dict], context_documents: list[dict]) -> tuple[list[dict], list[dict]]:
    direct, context = [], [{**document, "semantic_classification": "context"} for document in context_documents]
    for document in documents:
        classification = document.get("semantic_classification", "direct")
        if classification == "context":
            context.append({**document, "semantic_classification": "context"})
        elif classification in ("", "direct"):
            direct.append(document)
    return direct, context
