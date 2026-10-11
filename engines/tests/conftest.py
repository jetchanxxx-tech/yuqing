"""让测试从任意目录运行时都能 import engines.* 包。"""
import sys
from pathlib import Path

# engines/tests → engines → 仓库根：仓库根加入 sys.path，使 `engines.*` 包可见
sys.path.insert(0, str(Path(__file__).resolve().parent.parent.parent))

import ipaddress
import socket

import pytest


@pytest.fixture(autouse=True)
def loopback_connections_only(monkeypatch):
    original = socket.socket.connect

    def connect(sock, address):
        if isinstance(address, tuple):
            try:
                loopback = ipaddress.ip_address(address[0]).is_loopback
            except ValueError:
                loopback = address[0] == "localhost"
            if not loopback:
                raise AssertionError("sandbox attempted a non-loopback network connection")
        return original(sock, address)

    monkeypatch.setattr(socket.socket, "connect", connect)
