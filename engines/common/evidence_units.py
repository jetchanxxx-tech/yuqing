"""Immutable source units shared by semantic admission and quotation producers."""
from engines.common.evidence import resolve_quote


MAX_UNIT_CHARS = 400


def source_units(document_id, text, *, offset=0, field="content"):
    """Partition every Unicode character; never trim or reconstruct source text."""
    start = 0
    while start < len(text):
        end = min(start + MAX_UNIT_CHARS, len(text))
        if end < len(text):
            end = max((index + 1 for index in range(start + 149, end)
                       if text[index] in "\n。！？!?"), default=end)
        prefix = f"{document_id}@" + ("title:" if field == "title" else "")
        yield {"id": f"{prefix}{start + offset}:{end + offset}", "text": text[start:end],
               "start": start + offset, "end": end + offset}
        start = end


def document_units(document):
    """Only expose title and the current, already bounded original body chunk."""
    document_id = document.get("id", "")
    for field in ("title", "content"):
        text = str(document.get(field) or "")
        offset = document.get("_content_start", 0) if field == "content" else 0
        for unit in source_units(document_id, text, offset=offset, field=field):
            yield {**unit, "field": field}


def resolve_unit_quote(reference, documents):
    """Resolve exact current-batch IDs, then apply the original source validator.

    A supplied unit field is authoritative: callers must never fall back to a
    parallel model-written quotation when its reference is invalid.
    """
    unit_id = reference.get("unit_id")
    document_id = reference.get("document_id")
    if not isinstance(unit_id, str) or not unit_id or not isinstance(document_id, str) or not document_id:
        return None
    matches = [(document, unit) for document in documents if document.get("id") == document_id
               for unit in document_units(document) if unit["id"] == unit_id]
    if len(matches) != 1:
        return None
    document, unit = matches[0]
    citation = resolve_quote({"document_id": document_id, "text": unit["text"],
                              "source": reference.get("source", "")}, documents)
    if citation is None:
        return None
    location = {"document_id": document_id, "field": unit["field"], "start": unit["start"], "end": unit["end"],
                "material_class": document.get("_material_class", document.get("semantic_classification", "direct")),
                "quote_text": citation["text"]}
    return citation, location
