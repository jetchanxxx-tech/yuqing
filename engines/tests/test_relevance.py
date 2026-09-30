"""Evidence admission: provider search results are candidates, not analysis documents."""
import pytest
from engines.common.relevance import admit, usable_excerpt


@pytest.mark.parametrize(("keyword", "title", "excerpt", "want", "reason"), [
    ("gs8", "传祺GS8越野版", "新车体验", True, "keyword_match"),
    ("GS8", "其他", "传祺 gs8 改装", True, "keyword_match"),
    ("gs8", "别克GL8试驾", "商务车", False, "irrelevant"),
    ("gs8", "DeepSeek FP8", "技术争议", False, "irrelevant"),
    ("gs8", "高达拼装模型", "万代HG评测", False, "irrelevant"),
    ("gs8", "GS80", "车型讨论", False, "irrelevant"),
    ("传祺GS8", "传祺GS80 新车", "越野车", False, "irrelevant"),
    ("传祺GS8", "传祺 GS8 越野版", "改装", True, "keyword_match"),
    ("雅阁 后排", "雅阁后排空间", "舒适性", True, "keyword_match"),
    ("雅阁 后排", "雅阁车身设计", "驾驶", False, "irrelevant"),
    ("新品", "新品发布", "产品资讯", True, "keyword_match"),
    ("gs8", "GS8越野版", "We're sorry but react app doesn't work properly without JavaScript enabled.", False, "unusable_excerpt"),
])
def test_admit(keyword, title, excerpt, want, reason):
    decision = admit(keyword, title, excerpt)
    assert (decision.accepted, decision.reason) == (want, reason)


def test_spa_excerpt_with_newlines_is_unusable():
    assert not usable_excerpt("We're\nsorry\nbut\nreact\napp\ndoesn't work properly without JavaScript enabled")


def test_empty_excerpt_is_unusable_even_with_relevant_title():
    decision = admit("gs8", "GS8改装", "")
    assert (decision.accepted, decision.reason) == (False, "unusable_excerpt")


def test_topic_evidence_ids_must_belong_to_admitted_documents():
    from engines.insight_engine.main import _validate_topic_evidence
    docs = [{"id":"d1"},{"id":"d2"}]
    topics = [
        {"id":"t1","name":"valid","doc_ids":["d1"],"doc_count":99},
        {"id":"t2","name":"invented","doc_ids":["d3"],"doc_count":1},
    ]
    valid, invalid = _validate_topic_evidence(topics,docs)
    assert [t["name"] for t in valid] == ["valid"]
    assert valid[0]["doc_count"] == 1
    assert invalid == 1
