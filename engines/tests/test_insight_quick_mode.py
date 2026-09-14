"""quick 模式（方案 B Lite 档）测试 —— 套餐裁剪的成本杠杆。

方案 B：Lite 99/月 跑 3 维速览（heat/sentiment/deep_cause），并尝试关闭
思考（thinking=disabled）压成本；供应商不认识 thinking 字段报 400 时
必须去掉重试（功能优先于成本优化）。
"""
import httpx
import pytest
from fastapi.testclient import TestClient

import engines.insight_engine.main as insight_engine
from engines.insight_engine.main import app

client = TestClient(app)

QUICK_IDS = ["heat", "sentiment", "deep_cause"]
FULL_IDS = ["background", "heat", "sentiment", "group", "deep_cause"]

DOCS = [
    {
        "id": "d1",
        "title": "雅阁后排空间实测翻车",
        "content": "后排腿部空间局促，身高178cm顶膝。",
        "source_type": "news",
        "source_name": "汽车之家",
        "published_at": "2026-09-01T10:00:00Z",
    },
    {
        "id": "d2",
        "title": "车主吐槽后排",
        "content": "评论区大量共鸣。",
        "source_type": "weibo",
        "source_name": "微博",
        "published_at": "2026-09-02T10:00:00Z",
    },
]

DIMENSION_PAYLOAD = {
    "findings": "核心发现：后排空间是焦点。",
    "data_points": ["2 篇文档中 1 篇直指后排"],
    "quotes": [{"text": "顶膝", "source": "汽车之家"}],
    "deep_read": "深层解读：预期错位。",
    "trend": "趋势：仍在扩散。",
}

SENT_TOPIC_RESP = {
    "sentiments": [
        {"document_id": "d1", "sentiment": "negative", "level": "负面",
         "score": 0.8, "confidence": 0.9},
        {"document_id": "d2", "sentiment": "negative", "level": "负面",
         "score": 0.7, "confidence": 0.85},
    ],
    "topics": [{"id": "t1", "name": "后排空间", "keywords": ["后排"],
                "doc_count": 2, "doc_ids": ["d1", "d2"], "trend": "stable"}],
}


class KwargsFakeLLM:
    """记录每次调用的 (prompt, kwargs)；可选：带 thinking 的调用抛 400。"""

    def __init__(self, reject_thinking: bool = False):
        self.calls: list[tuple[str, dict]] = []
        self.reject_thinking = reject_thinking

    async def chat_json(self, model: str, messages: list[dict], **kwargs) -> dict:
        prompt = str(messages)
        self.calls.append((prompt, kwargs))
        if self.reject_thinking and kwargs.get("thinking"):
            req = httpx.Request("POST", "http://llm.test/chat/completions")
            resp = httpx.Response(400, request=req,
                                  json={"error": "unknown field: thinking"})
            raise httpx.HTTPStatusError("bad request", request=req, response=resp)

        if "【分析维度】" in prompt:
            return dict(DIMENSION_PAYLOAD)
        if "批判" in prompt:
            return {"critique": "x", "revised_summary": "摘要"}
        return SENT_TOPIC_RESP

    def thinking_calls(self) -> list[tuple[str, dict]]:
        return [c for c in self.calls if c[1].get("thinking")]


@pytest.fixture(autouse=True)
def _no_retry_backoff(monkeypatch):
    monkeypatch.setattr(insight_engine, "_RETRY_DELAY_SECONDS", 0)


@pytest.fixture
def patch_client(monkeypatch):
    def _patch(llm: KwargsFakeLLM):
        monkeypatch.setattr(insight_engine, "build_client",
                            lambda api_key="", base_url="": llm)
    return _patch


def test_quick_mode_runs_only_three_dimensions(patch_client):
    """quick = 3 维速览：heat/sentiment/deep_cause，其余维度不调用。"""
    llm = KwargsFakeLLM()
    patch_client(llm)
    resp = client.post("/analyze", json={
        "documents": DOCS, "api_key": "sk-x", "mode": "quick"})

    assert resp.status_code == 200
    dims = resp.json()["dimensions"]
    assert [d["id"] for d in dims] == QUICK_IDS

    dim_calls = [c for c in llm.calls if "【分析维度】" in c[0]]
    assert len(dim_calls) == 3, f"quick 模式维度调用应 3 次，实际 {len(dim_calls)}"


def test_full_mode_keeps_five_dimensions(patch_client):
    """空串 / full / 未知值都按完整 5 维处理（既有行为零改动）。"""
    for mode in ("", "full", "whatever"):
        llm = KwargsFakeLLM()
        patch_client(llm)
        resp = client.post("/analyze", json={
            "documents": DOCS, "api_key": "sk-x", "mode": mode})
        assert resp.status_code == 200
        assert [d["id"] for d in resp.json()["dimensions"]] == FULL_IDS


def test_quick_mode_disables_thinking_on_all_llm_calls(patch_client):
    """quick 模式的每一次 LLM 调用都带 thinking=disabled（Lite 成本模型的关键）。"""
    llm = KwargsFakeLLM()
    patch_client(llm)
    client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x", "mode": "quick"})

    assert len(llm.calls) >= 5  # 情感话题 1 + 维度 3 + 摘要 1
    assert len(llm.thinking_calls()) == len(llm.calls), \
        "quick 模式存在未关闭思考的调用（Lite 档成本失控）"
    for _, kwargs in llm.thinking_calls():
        assert kwargs["thinking"] == {"type": "disabled"}


def test_full_mode_never_sends_thinking_param(patch_client):
    """完整模式不发送 thinking 字段（思考型模型照常工作）。"""
    llm = KwargsFakeLLM()
    patch_client(llm)
    client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x", "mode": "full"})

    assert llm.calls, "应有 LLM 调用"
    assert not llm.thinking_calls(), "full 模式不得发送 thinking 参数"


def test_provider_rejecting_thinking_falls_back_without_it(patch_client):
    """供应商不认识 thinking 字段报 400：去掉重试，功能不受损。"""
    llm = KwargsFakeLLM(reject_thinking=True)
    patch_client(llm)
    resp = client.post("/analyze", json={
        "documents": DOCS, "api_key": "sk-x", "mode": "quick"})

    assert resp.status_code == 200
    body = resp.json()
    assert [d["id"] for d in body["dimensions"]] == QUICK_IDS
    assert body["summary"], "400 回退后摘要仍应产出"
