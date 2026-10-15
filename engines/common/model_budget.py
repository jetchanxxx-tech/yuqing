"""Complete JSON output with one shared supplier-attempt and time budget."""
import asyncio
import json
import os
from dataclasses import dataclass

import httpx

from engines.common.llm_client import LLMClient, LLMOutputTruncated, _is_transient

OUTPUT_TOKENS = 16384
TRUNCATED_OUTPUT_TOKENS = 32768


class ModelContextExceeded(ValueError):
    pass


@dataclass
class AttemptBudget:
    """Supplier allowance shared by transport and semantic output recovery."""

    limit: int = 2
    used: int = 0
    output_tokens: int = OUTPUT_TOKENS

    def __post_init__(self):
        if self.limit not in (1, 2) or not 0 <= self.used <= self.limit:
            raise ValueError("supplier attempt budget must be one or two")

    @property
    def remaining(self):
        return self.limit - self.used


def _check_capacity(client, model, messages, output_tokens):
    """Explicit operator capabilities; never silently reduce requested output.

    UTF-8 bytes are a conservative input envelope, not a claimed exact token
    count. The real provider's usage remains the billing source of truth.
    """
    catalog = json.loads(os.environ.get("YUQING_MODEL_CAPABILITIES_JSON", "{}"))
    limits = catalog.get(model) or catalog.get(model.lower())
    if limits is None:
        # Offline provider-boundary fixtures cannot contact a live supplier.
        # Real clients must have an operator-verified entry before spending.
        if not isinstance(client, LLMClient) or client._transport is not None:
            return
        raise ModelContextExceeded("model capabilities have not been verified for this configured model")
    if output_tokens > limits["max_output_tokens"]:
        raise ModelContextExceeded("configured model cannot support the requested output budget")
    input_envelope = len(json.dumps(messages, ensure_ascii=False).encode("utf-8"))
    if input_envelope + output_tokens + 4096 > limits["context_tokens"]:
        raise ModelContextExceeded("material batch exceeds the verified model context; split the batch")


async def chat_json_bounded(client, model, messages, *, deadline=None,
                            attempt_budget=None, validate_result=None, **kwargs):
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
    budget = attempt_budget if attempt_budget is not None else AttemptBudget()
    if not budget.remaining:
        raise ValueError("supplier attempt budget exhausted")
    while budget.remaining:
        remaining = deadline - loop.time()
        if remaining <= 0:
            raise TimeoutError("model phase deadline expired")
        _check_capacity(client, model, messages, budget.output_tokens)
        budget.used += 1
        options = {**parameters, "max_tokens": budget.output_tokens}
        if isinstance(client, LLMClient):
            options.update(_max_attempts=1, _attempt_offset=budget.used - 1)
        try:
            result = await asyncio.wait_for(client.chat_json(model, messages, **options), timeout=min(900, remaining))
            if validate_result is not None:
                validate_result(result)
            return result
        except ModelContextExceeded:
            raise
        except (httpx.HTTPError, ValueError) as exc:
            if not budget.remaining:
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
                budget.output_tokens = TRUNCATED_OUTPUT_TOKENS
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
