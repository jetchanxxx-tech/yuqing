"""Deterministic source provenance and sample membership validation."""
import math
from collections import Counter

SOURCE_LABELS = {
    "news": "新闻", "weibo": "微博", "wechat": "公众号", "douyin": "抖音",
    "toutiao": "今日头条", "xigua": "西瓜视频", "xiaohongshu": "小红书", "bilibili": "B站",
}
WRAPPER_PAIRS = {"\"": "\"", "'": "'", "“": "”", "‘": "’", "「": "」", "『": "』", "«": "»"}


def normalize_quote(value: str) -> str:
    text = "".join(value.split())
    while len(text) >= 2 and WRAPPER_PAIRS.get(text[0]) == text[-1]:
        text = text[1:-1]
    return text


def resolve_quote(quote: dict, documents: list[dict]) -> dict | None:
    text = quote.get("text")
    source = quote.get("source", "")
    document_id = quote.get("document_id", "")
    if not isinstance(text, str) or not isinstance(source, str) or not isinstance(document_id, str):
        return None
    normalized = normalize_quote(text)
    if not normalized:
        return None
    matches = []
    for doc in documents:
        if not isinstance(doc, dict) or not isinstance(doc.get("id"), str) or not doc["id"]:
            continue
        if document_id and doc["id"] != document_id:
            continue
        labels = {doc.get("source_name", ""), doc.get("source_type", ""), SOURCE_LABELS.get(doc.get("source_type", ""), "")}
        if source.strip() and source.strip() not in labels:
            continue
        # Never create an artificial span by concatenating separate fields.
        if any(normalized in normalize_quote(value) for key in ("title", "content")
               if isinstance(value := doc.get(key), str) and value):
            matches.append(doc)
    if len(matches) != 1:
        return None
    doc = matches[0]
    return {
        "text": text,
        "source": doc.get("source_name") or SOURCE_LABELS.get(doc.get("source_type", "")) or doc.get("source_type", ""),
        "document_id": doc["id"],
    }


def sample_stats(documents: list[dict]) -> dict:
    docs = [d for d in documents if isinstance(d, dict)]
    sources = Counter(d.get("source_name") or d.get("source_type") or "来源未知" for d in docs)
    return {"sample_document_count": len(docs), "source_counts": dict(sorted(sources.items())),
            "scope": "本次输入样本；计数包含因素材预算未展示的文档，不代表全网声量"}


def validate_classification(data: dict, documents: list[dict]) -> tuple[list[dict], list[dict], list[str]]:
    ids = {d["id"] for d in documents if isinstance(d, dict) and isinstance(d.get("id"), str) and d["id"]}
    warnings = []
    sentiments = []
    seen = set()
    raw_sentiments = data.get("sentiments", [])
    if not isinstance(raw_sentiments, list):
        raw_sentiments = []
        warnings.append("情感结果不是数组")
    for item in raw_sentiments:
        doc_id = item.get("document_id") if isinstance(item, dict) else None
        valid = isinstance(doc_id, str) and doc_id in ids and doc_id not in seen
        if valid:
            valid = item.get("sentiment") in ("positive", "negative", "neutral")
        if valid:
            for key in ("score", "confidence"):
                value = item.get(key, 0)
                if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or not 0 <= value <= 1:
                    valid = False
        if not valid:
            warnings.append("情感结果含未知／重复文档或非法情感数值")
            continue
        seen.add(doc_id)
        sentiments.append(item)
    if seen != ids:
        warnings.append(f"情感结果缺少 {len(ids - seen)} 篇样本")

    topics = []
    assigned = set()
    topic_ids = set()
    raw_topics = data.get("topics", [])
    if not isinstance(raw_topics, list):
        raw_topics = []
        warnings.append("话题结果不是数组")
    for item in raw_topics:
        if not isinstance(item, dict) or not isinstance(item.get("id"), str) or not item["id"] or item["id"] in topic_ids:
            warnings.append("话题标识非法或重复")
            continue
        members = item.get("doc_ids")
        if not isinstance(members, list):
            warnings.append("话题缺少可核验文档成员")
            continue
        accepted = []
        for member in members:
            if not isinstance(member, str) or member not in ids or member in assigned:
                warnings.append("话题成员含未知、重复或跨话题重复文档")
                continue
            accepted.append(member)
            assigned.add(member)
        if not accepted:
            continue
        if item.get("doc_count") != len(accepted):
            warnings.append("话题文档数已按真实唯一成员纠正")
        name = item.get("name")
        keywords = item.get("keywords", [])
        if not isinstance(name, str) or not name.strip() or not isinstance(keywords, list) or any(not isinstance(k, str) for k in keywords):
            assigned.difference_update(accepted)
            warnings.append("话题名称或关键词非法")
            continue
        topic_ids.add(item["id"])
        topics.append({**item, "doc_ids": accepted, "doc_count": len(accepted)})
    return sentiments, topics, list(dict.fromkeys(warnings))
