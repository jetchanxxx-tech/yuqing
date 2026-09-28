from pathlib import Path


def test_forum_modules_compile():
    forum = Path(__file__).resolve().parents[1] / "forum_engine"
    for name in ("main.py", "orchestrator.py"):
        path = forum / name
        compile(path.read_text(encoding="utf-8"), str(path), "exec")
