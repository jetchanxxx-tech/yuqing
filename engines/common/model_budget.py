"""Complete JSON output with one shared supplier-attempt and time budget."""
import asyncio
import json
import os

import httpx

from engines.common.llm_client import LLMClient, LLMOutputTruncated, _is_transient

OUTPUT_TOKENS = 16384
TRUNCATED_OUTPUT_TOKENS = 32768


class ModelContextExceeded(ValueError):
    pass


def _check_capacity(model, messages, output_tokens):
    """Explicit operator capabilities; never silently reduce requested output.

    UTF-8 bytes are a conservative input envelope, not a claimed exact token
    count. The real provider's usage remains the billing source of truth.
    """
    catalog = json.loads(os.environ.get("YUQING_MODEL_CAPABILITIES_JSON", "{}"))
    limits = catalog.get(model) or catalog.get(model.lower())
    if limits is None:
        return
    if output_tokens > limits["max_output_tokens"]:
        raise ModelContextExceeded("configured model cannot support the requested output budget")
    input_envelope = len(json.dumps(messages, ensure_ascii=False).encode("utf-8"))
    if input_envelope + output_tokens + 4096 > limits["context_tokens"]:
        raise ModelContextExceeded("material batch exceeds the verified model context; split the batch")


async def chat_json_bounded(client, model, messages, *, deadline=None, **kwargs):
    """At most two supplier attempts, all bounded by the same deadline.

    A truncation or malformed JSON gets a larger output allowance. Transient
    transport failures and explicitly rejected optional GLM parameters share
    that same second attempt; nested client retries are disabled.
    """
    loop = asyncio.get_running_loop()
    if deadline is None:
        deadline = loop.time() + 900
    parameters = dict(kwargs)
    parameters.pop("max_tokens", None)
    output_tokens = OUTPUT_TOKENS
    for attempt in (1, 2):
        remaining = deadline - loop.time()
        if remaining <= 0:
            raise TimeoutError("model phase deadline expired")
        _check_capacity(model, messages, output_tokens)
        options = {**parameters, "max_tokens": output_tokens}
        if isinstance(client, LLMClient):
            options.update(_max_attempts=1, _attempt_offset=attempt - 1)
        try:
            return await asyncio.wait_for(client.chat_json(model, messages, **options), timeout=min(900, remaining))
        except ModelContextExceeded:
            raise
        except (httpx.HTTPError, ValueError) as exc:
            if attempt == 2:
                raise
            retry = False
            if isinstance(exc, httpx.HTTPStatusError):
                detail = exc.response.text.lower()
                optional = any(k in parameters for k in ("thinking", "reasoning_effort"))
                if exc.response.status_code == 400 and optional and any(k in detail for k in ("thinking", "reasoning_effort")):
                    parameters.pop("thinking", None)
                    parameters.pop("reasoning_effort", None)
                    retry = True
            if isinstance(exc, ValueError):
                output_tokens = TRUNCATED_OUTPUT_TOKENS
                retry = True
            elif _is_transient(exc):
                retry = True
                remaining = deadline - loop.time()
                if remaining <= 2:
                    raise
                await asyncio.sleep(2)
            if not retry:
                raise
    raise RuntimeError("unreachable model retry state")
