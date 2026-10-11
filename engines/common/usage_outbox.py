"""Durable per-attempt usage records; only a committed Go ACK permits removal."""
import json
import os
from pathlib import Path
import re
import tempfile
import time


class AccountingDurabilityError(RuntimeError):
    """Observed accounting could not be made durable; never retry a provider."""


def directory() -> Path:
    configured = os.environ.get("YUQING_USAGE_OUTBOX_DIR", "")
    if not configured:
        raise RuntimeError("persistent usage outbox is not configured")
    result = Path(configured)
    result.mkdir(parents=True, exist_ok=True, mode=0o700)
    return result


def _sync_directory(root: Path) -> None:
    descriptor = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _process_identity(pid: int) -> str | None:
    try:
        stat = Path(f"/proc/{pid}/stat").read_text()
        start = stat.rsplit(")", 1)[1].split()[19]
        boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
        return f"{boot}:{start}"
    except FileNotFoundError:
        return None


def _persist_usage_event(event: dict, permit: str, *, pending: bool = False) -> str:
    event_id = event["event_id"]
    if not re.fullmatch(r"[a-zA-Z0-9_-]{1,128}", event_id):
        raise ValueError("invalid usage event identity")
    root = directory()
    destination = root / f"{event_id}.json"
    record = {"event": event, "permit": permit, "pending": pending, "owner_pid": os.getpid(), "owner_identity": _process_identity(os.getpid()), "written_at": time.time()}
    descriptor, temporary = tempfile.mkstemp(prefix=".usage-", dir=root)
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
    return str(destination)


def persist_usage_event(event: dict, permit: str, *, pending: bool = False) -> str:
    try:
        return _persist_usage_event(event, permit, pending=pending)
    except Exception as error:
        raise AccountingDurabilityError("usage persistence unavailable") from error


def pending_records():
    for path in sorted(directory().glob("*.json")):
        try:
            record = json.loads(path.read_text(encoding="utf-8"))
        except FileNotFoundError:
            continue  # Another engine completed the same safe replay.
        if record.get("pending"):
            # A live provider attempt must not be ACKed as zero/unknown by a
            # second engine's periodic flusher. After its process dies, the
            # durable pre-call intent is delivered as explicitly unknown usage.
            identity = _process_identity(int(record["owner_pid"]))
            if identity is not None and identity == record.get("owner_identity"):
                continue
            record["pending"] = False
            record["event"]["outcome"] = "process_interrupted"
        yield path, record


def acknowledge(path: Path) -> None:
    try:
        path.unlink()
    except FileNotFoundError:
        return
    _sync_directory(path.parent)
