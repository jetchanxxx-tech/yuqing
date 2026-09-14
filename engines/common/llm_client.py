"""Shared LLM client (OpenAI-compatible API; provider 可配置：智谱 GLM / DeepSeek / …)。

三级配置来源（高→低）：
  ① 请求参数（平台后台「数据源配置」在线修改，经 Go 管线透传 —— 零重启生效）
  ② 环境变量 LLM_API_KEY / LLM_BASE_URL / LLM_MODEL（engines.env）
  ③ 代码默认值（智谱 GLM）
"""
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
        """Chat with JSON output mode; returns the parsed content as a dict.

        思考型模型（如 glm-5.3-flash）的 content 之外的 reasoning 不参与解析；
        response_format=json_object 若被供应商拒绝，由调用方按 4xx 走降级。
        """
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


# ── 通用 LLM 工厂（insight/report 引擎共用）──────────────────
# 2026-09 起默认智谱 GLM（DeepSeek 余额弃用）；换供应商只需改环境变量
# 或后台配置，业务代码零改动 —— 这正是接口先行的验收。

LLM_BASE_URL = os.environ.get("LLM_BASE_URL", "https://open.bigmodel.cn/api/paas/v4")
LLM_MODEL = os.environ.get("LLM_MODEL", "glm-5.3-flash")


def build_client(api_key: str = "", base_url: str = "") -> LLMClient:
    """Build an LLMClient. api_key 为空时回退环境变量 LLM_API_KEY。

    兼容旧名：DEEPSEEK_API_KEY 仍被读取（迁移期），LLM_API_KEY 优先。
    """
    key = api_key or os.environ.get("LLM_API_KEY", "") or os.environ.get("DEEPSEEK_API_KEY", "")
    return LLMClient(base_url=base_url or LLM_BASE_URL, api_key=key)
