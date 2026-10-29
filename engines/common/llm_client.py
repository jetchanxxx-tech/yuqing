"""Shared LLM client (OpenAI-compatible API; provider 可配置：智谱 GLM / DeepSeek / …)。

三级配置来源（高→低）：
  ① 请求参数（平台后台「数据源配置」在线修改，经 Go 管线透传 —— 零重启生效）
  ② 环境变量 LLM_API_KEY / LLM_BASE_URL / LLM_MODEL（engines.env）
  ③ 代码默认值（智谱 GLM）
"""
import asyncio
import json
import logging
import os
import re
import uuid

from engines.common.usage_bridge import authorize_call, deliver_pending_usage_events
from engines.common.usage_outbox import persist_usage_event, AccountingDurabilityError

import httpx


logger = logging.getLogger(__name__)


class LLMOutputTruncated(ValueError):
    """The provider stopped before completing the requested JSON output."""


class LLMSupplierTimeout(TimeoutError):
    """Only the supplier request exceeded its bounded attempt allowance."""


class LLMContentRejected(RuntimeError):
    """Terminal provider moderation decision, independent of JSON validity."""

    code = "llm_content_rejected"

    def __init__(self):
        super().__init__("llm_content_rejected: 模型供应商内容审核拒绝处理本次材料")


def _content_rejected(response):
    return any(choice.get("finish_reason") in {"sensitive", "content_filter"}
               for choice in response.get("choices") or [])


# 调用层重试（借鉴 BettaFish 思路，按本项目预算收紧）：Go→insight 只等 420s，
# 只对快速失败的瞬时错误重试一次；504/524/读超时已耗掉大段预算，不重试。
_TRANSIENT_STATUS = {429, 500, 502, 503}
RETRY_DELAY_SECONDS = 2


def _is_transient(exc: Exception) -> bool:
    if isinstance(exc, httpx.TimeoutException):
        return False
    if isinstance(exc, httpx.HTTPStatusError):
        return exc.response.status_code in _TRANSIENT_STATUS
    return isinstance(exc, httpx.TransportError)


class LLMClient:
    """Thin wrapper around an OpenAI-compatible chat completions endpoint.

    transport 是测试注入点（httpx.MockTransport），生产环境为 None。
    """

    def __init__(self, base_url: str, api_key: str = "", timeout: float = 120.0, transport=None):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout
        self._transport = transport
        self.usage_context = None

    async def chat(self, model: str, messages: list[dict], **kwargs) -> dict:
        """流式请求（持续出字节，避免 Cloudflare 中转 125s 读超时报 524），返回非流式结构。"""
        usage_context = kwargs.pop("usage_context", None) or self.usage_context
        if not usage_context and self._transport is None:
            raise RuntimeError("provider call requires an accepted run and usage bridge")
        max_attempts = kwargs.pop("_max_attempts", 2)
        attempt_offset = kwargs.pop("_attempt_offset", 0)
        supplier_timeout = kwargs.pop("_supplier_timeout", None)
        if max_attempts not in (1, 2):
            raise ValueError("supplier attempt budget must be one or two")
        for index in range(1, max_attempts + 1):
            attempt = index + attempt_offset
            try:
                return await self._metered_attempt(model, messages, usage_context, attempt,
                    supplier_timeout=supplier_timeout, **kwargs)
            except Exception as exc:
                if index == max_attempts or not _is_transient(exc):
                    raise
                logger.warning("LLM transient provider failure; retrying once")
                await asyncio.sleep(RETRY_DELAY_SECONDS)

    async def _supplier_attempt(self, model, messages, supplier_timeout=None, **kwargs):
        if supplier_timeout is None:
            return await self._chat_once(model, messages, **kwargs)
        observed_response = None

        async def request():
            nonlocal observed_response
            try:
                return await self._chat_once(model, messages, **kwargs)
            except BaseException as error:
                # wait_for translates the child cancellation into a new error.
                # Capture observed usage before that translation, including when
                # the parent phase or the user cancels this timed request.
                observed_response = getattr(error, "observed_response", None)
                raise

        try:
            return await asyncio.wait_for(request(), timeout=supplier_timeout)
        except TimeoutError as error:
            failure = LLMSupplierTimeout("supplier attempt deadline expired")
            if observed_response:
                failure.observed_response = observed_response
            raise failure from error
        except BaseException as error:
            if observed_response:
                error.observed_response = observed_response
            raise

    async def _metered_attempt(self, model, messages, usage_context, attempt, *, supplier_timeout=None, **kwargs):
        if not usage_context:
            # Explicit MockTransport-only unit clients cannot contact suppliers.
            return await self._supplier_attempt(model, messages, supplier_timeout, **kwargs)
        call_id = uuid.uuid4().hex
        permission = await authorize_call(usage_context, call_id, model, attempt)
        event = {"event_id": uuid.uuid4().hex, "call_id": call_id, "event_version": 1, "attempt": attempt,
                 "actual_model": model, "provider_request_id": "", "usage_status": "unknown",
                 "prompt_tokens": 0, "completion_tokens": 0, "cache_tokens": 0, "outcome": "provider_pending"}
        # Validate durable storage before spending. A crash leaves an explicit
        # unknown intent, never a guessed token amount or a verified zero cost.
        persist_usage_event(event, permission["permit"], pending=True)
        try:
            response = await self._supplier_attempt(model, messages, supplier_timeout, **kwargs)
        except BaseException as error:
            observed = getattr(error, "observed_response", None)
            if observed:
                _observe_usage(event, observed)
            rejected = bool(observed and _content_rejected(observed))
            event["outcome"] = "provider_rejected" if rejected else "provider_error"
            persist_usage_event(event, permission["permit"])
            if rejected and isinstance(error, Exception):
                # External cancellation remains cancellation; an observed
                # refusal remains terminal even if the stream later breaks.
                raise LLMContentRejected() from None
            raise
        _observe_usage(event, response)
        event["outcome"] = "provider_rejected" if _content_rejected(response) else "provider_response"
        # This fatal error is intentionally outside the supplier exception
        # handler: failure to persist must not retry or relabel observed facts.
        persist_usage_event(event, permission["permit"])
        await deliver_pending_usage_events()
        return response

    async def _chat_once(self, model: str, messages: list[dict], **kwargs) -> dict:
        async with httpx.AsyncClient(timeout=self.timeout, transport=self._transport) as client:
            async with client.stream("POST",
                f"{self.base_url}/chat/completions",
                headers={"Authorization": f"Bearer {self.api_key}"},
                json={"model": model, "messages": messages, "stream": True, "stream_options": {"include_usage": True}, **kwargs},
            ) as resp:
                if resp.status_code >= 400:
                    await resp.aread()
                    resp.raise_for_status()
                if "text/event-stream" not in resp.headers.get("content-type", ""):
                    await resp.aread()
                    return resp.json()
                parser = _StreamAccumulator()
                try:
                    async for line in resp.aiter_lines():
                        if line.startswith("data:"):
                            payload = line[5:].strip()
                            if payload == "[DONE]":
                                break
                            if payload:
                                parser.feed(json.loads(payload))
                except BaseException as error:
                    error.observed_response = parser.result()
                    raise
                return parser.result()

    async def chat_json(self, model: str, messages: list[dict], **kwargs) -> dict:
        """Chat with JSON output mode; returns the parsed content as a dict.

        思考型模型（如 glm-5.3-flash）会先产出 reasoning_content 再输出 content
        —— max_tokens 是两者之和，给小了 content 直接为空（生产实测踩坑）。
        finish_reason=length 时显式报「输出被截断」，而不是模糊的 invalid JSON。
        """
        kwargs.setdefault("response_format", {"type": "json_object"})
        resp = await self.chat(model, messages, **kwargs)
        if _content_rejected(resp):
            # The response is already durably accounted. Do not parse partial
            # content or spend the malformed-JSON recovery allowance on a
            # supplier's explicit content decision.
            raise LLMContentRejected()
        choice = (resp.get("choices") or [{}])[0]
        content = (choice.get("message") or {}).get("content", "")
        if choice.get("finish_reason") == "length":
            raise LLMOutputTruncated(
                "LLM 输出被截断（思考内容或 JSON 内容耗尽 max_tokens）—— 增大 max_tokens 或换非思考型模型")
        return _parse_json_content(content)


def _observe_usage(event: dict, response: dict) -> None:
    usage = response.get("usage")
    event["actual_model"] = response.get("model") or event["actual_model"]
    event["provider_request_id"] = response.get("id") or event["provider_request_id"]
    if isinstance(usage, dict):
        prompt = usage.get("prompt_tokens")
        completion = usage.get("completion_tokens")
        cache = (usage.get("prompt_tokens_details") or {}).get("cached_tokens", usage.get("prompt_cache_hit_tokens", 0))
        if all(isinstance(value, int) and not isinstance(value, bool) and value >= 0 for value in (prompt, completion, cache)) and cache <= prompt:
            event.update(usage_status="reported", prompt_tokens=prompt, completion_tokens=completion, cache_tokens=cache)


class _StreamAccumulator:
    def __init__(self):
        self.content = []
        self.finish_reason = None
        self.observed = {"usage": None, "model": None, "id": None}

    def feed(self, chunk):
        for field in self.observed:
            self.observed[field] = chunk.get(field) or self.observed[field]
        if chunk.get("error"):
            # Supplier error text can contain request content or credentials.
            raise RuntimeError("LLM supplier returned an error during streaming")
        for choice in chunk.get("choices") or []:
            self.content.append((choice.get("delta") or {}).get("content") or "")
            self.finish_reason = choice.get("finish_reason") or self.finish_reason

    def result(self):
        return {"choices": [{"message": {"content": "".join(self.content)},
                             "finish_reason": self.finish_reason}], **self.observed}


def _parse_sse(body: str) -> dict:
    """Preserve received usage and request identity even if a later chunk fails."""
    content: list[str] = []
    finish_reason = None
    observed = {"usage": None, "model": None, "id": None}
    try:
        for line in body.splitlines():
            if not line.startswith("data:"):
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                break
            chunk = json.loads(data)
            # Error chunks can themselves carry the final usage facts.
            for field in ("usage", "model", "id"):
                observed[field] = chunk.get(field) or observed[field]
            if chunk.get("error"):
                error = chunk["error"]
                raise RuntimeError(f"LLM stream error: {error.get('message', error) if isinstance(error, dict) else error}")
            for choice in chunk.get("choices") or []:
                content.append((choice.get("delta") or {}).get("content") or "")
                finish_reason = choice.get("finish_reason") or finish_reason
    except Exception as error:
        error.observed_response = observed
        raise
    return {"choices": [{"message": {"content": "".join(content)}, "finish_reason": finish_reason}], **observed}


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


def build_client(api_key: str = "", base_url: str = "", timeout: float = 120.0) -> LLMClient:
    """Build an LLMClient. api_key 为空时回退环境变量 LLM_API_KEY。

    兼容旧名：DEEPSEEK_API_KEY 仍被读取（迁移期），LLM_API_KEY 优先。
    timeout：思考型模型单次推理可能 2-5 分钟，调用方按需放宽。
    """
    key = api_key or os.environ.get("LLM_API_KEY", "") or os.environ.get("DEEPSEEK_API_KEY", "")
    return LLMClient(base_url=base_url or LLM_BASE_URL, api_key=key, timeout=timeout)
