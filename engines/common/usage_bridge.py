"""Authenticated loopback-only bridge to the platform's accepted run ledger."""
import asyncio
from contextlib import asynccontextmanager
from datetime import datetime, timezone
import ipaddress
import logging
import os
from urllib.parse import urlsplit

import httpx

from engines.common.usage_outbox import acknowledge, pending_records

logger = logging.getLogger(__name__)


def configuration() -> tuple[str, str]:
    url = os.environ.get("YUQING_BILLING_URL", "")
    secret = os.environ.get("YUQING_BILLING_SERVICE_TOKEN", "")
    parsed = urlsplit(url)
    try:
        loopback = ipaddress.ip_address(parsed.hostname or "").is_loopback
    except ValueError:
        loopback = parsed.hostname == "localhost"
    if parsed.scheme not in {"http", "https"} or not loopback or parsed.username or parsed.password or not secret:
        raise RuntimeError("authenticated loopback billing bridge is not configured")
    return url.rstrip("/"), secret


async def authorize_call(context: dict, call_id: str, model: str, attempt: int) -> dict:
    if not context.get("run_id"):
        raise RuntimeError("provider call requires an accepted analysis run")
    url, secret = configuration()
    body = {"call_id": call_id, "run_id": context["run_id"], "engine": context["engine"], "phase": context["phase"], "attempt": attempt, "model": model}
    async with httpx.AsyncClient(timeout=15, trust_env=False) as client:
        response = await client.post(url + "/internal/v1/billing/llm-authorizations", json=body, headers={"X-Billing-Service-Token": secret})
        response.raise_for_status()
        permit = response.json()
    expires = datetime.fromisoformat(permit["expires_at"].replace("Z", "+00:00"))
    if permit.get("call_id") != call_id or not permit.get("permit") or expires <= datetime.now(timezone.utc):
        raise RuntimeError("provider authorization is invalid or expired")
    return permit


async def deliver_pending_usage_events() -> int:
    url, secret = configuration()
    delivered = 0
    async with httpx.AsyncClient(timeout=15, trust_env=False) as client:
        for path, record in pending_records():
            try:
                response = await client.post(url + "/internal/v1/billing/usage-events", json=record["event"], headers={"X-Billing-Service-Token": secret, "X-LLM-Call-Permit": record["permit"]})
                if response.status_code not in {200, 201} or response.json().get("committed") is not True:
                    continue
                acknowledge(path)
                delivered += 1
            except (httpx.HTTPError, ValueError, OSError):
                # The original event ID and permit survive ACK loss and restart.
                logger.warning("usage delivery deferred; durable event retained")
    return delivered


async def delivery_loop() -> None:
    while True:
        try:
            await deliver_pending_usage_events()
        except (RuntimeError, OSError, ValueError):
            logger.warning("usage outbox delivery unavailable; pending records retained")
        await asyncio.sleep(15)


@asynccontextmanager
async def usage_lifespan(app):
    task = None
    if os.environ.get("YUQING_BILLING_URL"):
        task = asyncio.create_task(delivery_loop())
        app.state.usage_delivery_task = task
    try:
        yield
    finally:
        if task:
            task.cancel()
            try:
                await task
            except asyncio.CancelledError:
                pass
