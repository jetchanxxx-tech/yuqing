"""Durable Search/Fetch accounting, deliberately separate from token usage."""
import asyncio
from contextlib import asynccontextmanager
from datetime import datetime, timezone
import json
import logging
import os
from pathlib import Path
import re
import tempfile
import time

import httpx

from engines.common.usage_bridge import configuration
from engines.common.usage_outbox import AccountingDurabilityError, _process_identity, _sync_directory

logger = logging.getLogger(__name__)


class ToolAuthorizationError(RuntimeError):
    """The accepted-run permission protocol failed; never downgrade to delivery."""


def directory():
    configured = os.getenv("YUQING_TOOL_USAGE_OUTBOX_DIR", "")
    if not configured:
        raise AccountingDurabilityError("persistent tool usage outbox is not configured")
    root = Path(configured)
    token_root = os.getenv("YUQING_USAGE_OUTBOX_DIR", "")
    if token_root and root.resolve() == Path(token_root).resolve():
        raise AccountingDurabilityError("tool and token outboxes must be separate")
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    return root


def persist_tool_event(event, permit, *, pending=False):
    try:
        if not re.fullmatch(r"[a-zA-Z0-9_-]{1,128}", event["event_id"]):
            raise ValueError("invalid tool event identity")
        root = directory()
        destination = root / (event["event_id"] + ".json")
        record = {"event": event, "permit": permit, "pending": pending, "owner_pid": os.getpid(),
                  "owner_identity": _process_identity(os.getpid()), "written_at": time.time()}
        descriptor, temporary = tempfile.mkstemp(prefix=".tool-", dir=root)
        try:
            with os.fdopen(descriptor, "w", encoding="utf-8") as output:
                json.dump(record, output, ensure_ascii=False, sort_keys=True)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, destination)
            _sync_directory(root)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
    except AccountingDurabilityError:
        raise
    except Exception as error:
        raise AccountingDurabilityError("tool usage persistence unavailable") from error


async def authorize_tool_call(run_id, call_id, operation, attempt, request_hash, *, transport=None):
    if not run_id:
        raise ToolAuthorizationError("tool call requires an accepted run")
    base, token = configuration()
    async with httpx.AsyncClient(timeout=15, trust_env=False, transport=transport) as client:
        response = await client.post(base + "/internal/v1/billing/tool-authorizations",
            headers={"X-Billing-Service-Token": token}, json={"call_id": call_id, "run_id": run_id,
                "provider": "deepseek", "operation": operation, "attempt": attempt, "request_hash": request_hash})
        response.raise_for_status()
        try:
            permit = response.json()
            expires = datetime.fromisoformat(permit["expires_at"].replace("Z", "+00:00"))
            if permit.get("call_id") != call_id or not permit.get("permit") or expires <= datetime.now(timezone.utc):
                raise ValueError("invalid permit")
        except (KeyError, ValueError, TypeError, AttributeError) as error:
            raise ToolAuthorizationError("tool authorization is invalid or expired") from error
    return permit


async def deliver_pending_tool_events(*, transport=None):
    base, token = configuration()
    delivered = 0
    async with httpx.AsyncClient(timeout=15, trust_env=False, transport=transport) as client:
        for path in sorted(directory().glob("*.json")):
            try:
                record = json.loads(path.read_text(encoding="utf-8"))
                if record.get("pending"):
                    identity = _process_identity(int(record["owner_pid"]))
                    if identity is not None and identity == record.get("owner_identity"):
                        continue
                    record["event"].update(outcome="process_interrupted", usage_status="unknown")
                    persist_tool_event(record["event"], record["permit"])
                response = await client.post(base + "/internal/v1/billing/tool-usage-events",
                    json=record["event"], headers={"X-Billing-Service-Token": token, "X-Tool-Call-Permit": record["permit"]})
                if response.status_code in {200, 201} and response.json().get("committed") is True:
                    try:
                        path.unlink()
                    except FileNotFoundError:
                        continue
                    _sync_directory(path.parent)
                    delivered += 1
            except FileNotFoundError:
                continue
            except (httpx.HTTPError, ValueError, OSError):
                logger.warning("tool usage delivery deferred; durable event retained")
    return delivered


async def _delivery_loop():
    while True:
        try:
            await deliver_pending_tool_events()
        except (RuntimeError, ValueError, OSError):
            logger.warning("tool usage outbox unavailable; pending records retained")
        await asyncio.sleep(15)


@asynccontextmanager
async def tool_usage_lifespan(app):
    task = None
    if os.getenv("YUQING_BILLING_URL") and os.getenv("YUQING_TOOL_USAGE_OUTBOX_DIR"):
        task = asyncio.create_task(_delivery_loop())
        app.state.tool_usage_delivery_task = task
    try:
        yield
    finally:
        if task:
            task.cancel()
            await asyncio.gather(task, return_exceptions=True)
