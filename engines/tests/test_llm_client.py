"""LLMClient 测试 — OpenAI 兼容客户端（含 chat_json）。

测试通过 httpx.MockTransport 注入离线响应，不发起真实网络请求。
"""
import json

import httpx
import pytest

from engines.common.llm_client import LLMClient


def make_client(handler) -> LLMClient:
    """构造带 MockTransport 的 LLMClient（transport 参数为注入点）。"""
    return LLMClient(
        base_url="https://api.example.com/v1",
        api_key="sk-test",
        transport=httpx.MockTransport(handler),
    )


@pytest.mark.asyncio
async def test_chat_sends_openai_compatible_request():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["url"] = str(request.url)
        seen["auth"] = request.headers.get("Authorization")
        seen["body"] = json.loads(request.content)
        return httpx.Response(200, json={"choices": [{"message": {"content": "ok"}}]})

    client = make_client(handler)
    await client.chat("deepseek-chat", [{"role": "user", "content": "hi"}])

    assert seen["url"].endswith("/v1/chat/completions")
    assert seen["auth"] == "Bearer sk-test"
    assert seen["body"]["model"] == "deepseek-chat"
    assert seen["body"]["messages"] == [{"role": "user", "content": "hi"}]


@pytest.mark.asyncio
async def test_chat_json_requests_json_object_and_parses():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["body"] = json.loads(request.content)
        return httpx.Response(200, json={"choices": [{"message": {"content": '{"sentiments": []}'}}]})

    client = make_client(handler)
    result = await client.chat_json("deepseek-chat", [{"role": "user", "content": "分类"}])

    assert result == {"sentiments": []}
    assert seen["body"]["response_format"] == {"type": "json_object"}


@pytest.mark.asyncio
async def test_chat_json_strips_markdown_code_fences():
    def handler(request: httpx.Request) -> httpx.Response:
        content = "```json\n{\"topics\": [1, 2]}\n```"
        return httpx.Response(200, json={"choices": [{"message": {"content": content}}]})

    client = make_client(handler)
    result = await client.chat_json("deepseek-chat", [])

    assert result == {"topics": [1, 2]}


@pytest.mark.asyncio
async def test_chat_json_invalid_json_raises_value_error():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"choices": [{"message": {"content": "不是 JSON"}}]})

    client = make_client(handler)
    with pytest.raises(ValueError, match="JSON"):
        await client.chat_json("deepseek-chat", [])


@pytest.mark.asyncio
async def test_chat_http_error_propagates():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(500, text="boom")

    client = make_client(handler)
    with pytest.raises(httpx.HTTPStatusError):
        await client.chat("deepseek-chat", [])
