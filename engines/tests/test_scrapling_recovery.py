"""Real pinned Scrapling parser/browser fixtures; no external platform claims."""
from pathlib import Path

import pytest

from engines.common.scraper import _extract_clean_body

FIXTURES = Path(__file__).parent / "fixtures" / "scrapling"


def test_real_scrapling_extractor_excludes_script_style_nav_and_recommendations():
    from scrapling import Selector
    page = Selector((FIXTURES / "wechat.html").read_text())
    text = _extract_clean_body(page, "wechat")
    assert "iPhone 18 Pro续航体验优秀" in text
    for noise in ("秘密脚本", "导航推荐", "样式噪音", "脚本文本", "样式当证据", "相关推荐", "页脚"):
        assert noise not in text


def test_real_scrapling_extractor_marks_capacity_and_published_metadata():
    from scrapling import Selector
    from engines.common.platform_extractors import extract_page
    text = "真实正文内容" * 40_000
    result = extract_page(Selector(f'<html><meta property="article:published_time" content="2026-10-12"><article>{text}</article></html>'), "toutiao")
    assert len(result["content"]) == 200_000
    assert result["truncated"] is True
    assert result["published_at"] == "2026-10-12"


@pytest.mark.asyncio
async def test_cancelled_isolated_fetch_reaps_the_entire_worker_group(monkeypatch, tmp_path):
    import asyncio
    import os
    import sys
    from engines.common.safe_fetch import run_fetch_subprocess
    worker = tmp_path / "blocked_worker.py"
    pidfile = tmp_path / "pid"
    worker.write_text("import os,time,pathlib\npathlib.Path(os.environ['FIXTURE_PIDFILE']).write_text(str(os.getpid()))\ntime.sleep(60)\n")
    monkeypatch.setenv("FIXTURE_PIDFILE", str(pidfile))
    task = asyncio.create_task(run_fetch_subprocess("https://weibo.com/one", "weibo", "dynamic",
        deadline=asyncio.get_running_loop().time() + 30, command=[sys.executable, str(worker)]))
    for _ in range(100):
        if pidfile.exists():
            break
        await asyncio.sleep(0.02)
    assert pidfile.exists()
    pid = int(pidfile.read_text())
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    with pytest.raises(ProcessLookupError):
        os.kill(pid, 0)
