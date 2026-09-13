"""让测试从任意目录运行时都能 import engines.* 包。"""
import sys
from pathlib import Path

# engines/tests → engines → 仓库根：仓库根加入 sys.path，使 `engines.*` 包可见
sys.path.insert(0, str(Path(__file__).resolve().parent.parent.parent))
