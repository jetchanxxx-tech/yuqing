#!/usr/bin/env python3
"""Hosted-only real Python -> Go -> PostgreSQL tool billing contract client.

The owning Go PG test supplies its actual accepted run and loopback bridge.
Only the external supplier is simulated; no authorization or usage ACK is mocked.
"""
import argparse
import asyncio
import json
import os
from pathlib import Path
import sys

import httpx

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from engines.common.deepseek_tools import DeepSeekTools
from engines.common.tool_usage import deliver_pending_tool_events


async def main(mode):
    run_id = os.environ["YUQING_TOOL_BRIDGE_RUN_ID"]
    supplier_calls = 0

    def supplier(request):
        nonlocal supplier_calls
        supplier_calls += 1
        if mode == "crash":
            # Authorization and fsync of the pending event precede the request.
            # A fresh interpreter must later settle this as unknown, never zero.
            os._exit(23)
        if request.url.path.endswith("/search"):
            result = {"results": [{"url": "https://www.chinanews.com.cn/cj/a.shtml",
                "title": "hosted fixture", "date": "2026-10-30T00:00:00Z",
                "content": "hosted supplier material"}]}
        else:
            result = {"title": "hosted fixture", "content": "hosted fetch body"}
        return httpx.Response(200, json=result, headers={"x-request-id": "hosted-tool-contract"})

    tools = DeepSeekTools("hosted-supplier-fixture-key", transport=httpx.MockTransport(supplier))
    if mode == "replay":
        await deliver_pending_tool_events()
    elif mode == "fetch":
        await tools.fetch("https://www.chinanews.com.cn/cj/a.shtml", run_id=run_id)
        await deliver_pending_tool_events()
    elif mode == "denied":
        try:
            await tools.search("手机", source="news", run_id=run_id)
        except (httpx.HTTPError, RuntimeError):
            pass
        else:
            raise AssertionError("canceled run obtained a new tool permit")
        assert supplier_calls == 0, "authorization failure reached the supplier"
    else:
        await tools.search("手机", source="news", run_id=run_id,
            date_from="2026-10-30", date_to="2026-10-30")
        await deliver_pending_tool_events()
    print(json.dumps({"mode": mode, "supplier_calls": supplier_calls}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--mode", choices=["spend", "replay", "fetch", "crash", "denied"], required=True)
    asyncio.run(main(parser.parse_args().mode))
