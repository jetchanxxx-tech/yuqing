"""Shared LLM client (OpenAI-compatible API, DeepSeek)."""
import json
import os
import re

import httpx


class LLMClient:
    """Thin wrapper around an OpenAI-compatible chat completions endpoint.

    transport 是测试注入点（httpx.MockTransport），生产环境为 None。
    """

    def __init__(self, base_url: str, api_key: str = "", timeout: float = 120.0, transport=None):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout
        self._transport = transport

    async def chat(self, model: str, messages: list[dict], **kwargs) -> dict:
        """Send a chat completion request. Returns the raw JSON response."""
        async with httpx.AsyncClient(timeout=self.timeout, transport=self._transport) as client:
            resp = await client.post(
                f"{self.base_url}/chat/completions",
                headers={"Authorization": f"Bearer {self.api_key}"},
                json={"model": model, "messages": messages, **kwargs},
            )
            resp.raise_for_status()
            return resp.json()

    async def chat_json(self, model: str, messages: list[dict], **kwargs) -> dict:
        """Chat with JSON output mode; returns the parsed content as a dict."""
        kwargs.setdefault("response_format", {"type": "json_object"})
        resp = await self.chat(model, messages, **kwargs)
        content = (resp.get("choices") or [{}])[0].get("message", {}).get("content", "")
        return _parse_json_content(content)


def _parse_json_content(content: str) -> dict:
    """Parse LLM JSON output, tolerating markdown code fences."""
    text = content.strip()
    fence = re.search(r"```(?:json)?\s*(.+?)```", text, re.DOTALL)
    if fence:
        text = fence.group(1).strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError as exc:
        raise ValueError(f"LLM returned invalid JSON: {text[:120]}") from exc


# ── DeepSeek 工厂（insight/report 引擎共用）─────────────────

DEEPSEEK_BASE_URL = os.environ.get("DEEPSEEK_BASE_URL", "https://api.deepseek.com")
DEEPSEEK_MODEL = os.environ.get("DEEPSEEK_MODEL", "deepseek-chat")


def build_client(api_key: str = "", base_url: str = "") -> LLMClient:
    """Build an LLMClient for DeepSeek. api_key 为空时回退环境变量。"""
    key = api_key or os.environ.get("DEEPSEEK_API_KEY", "")
    return LLMClient(base_url=base_url or DEEPSEEK_BASE_URL, api_key=key)
