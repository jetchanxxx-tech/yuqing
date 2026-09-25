"""Logging system for debate process.

记录辩论过程、LLM调用、性能指标到日志文件和数据库。
"""
import logging
import json
from typing import Dict, Any, Optional
from datetime import datetime
from pathlib import Path


class DebateLogger:
    """辩论日志记录器。"""

    def __init__(self, log_dir: str = "logs"):
        """初始化日志记录器。

        Args:
            log_dir: 日志目录
        """
        self.log_dir = Path(log_dir)
        self.log_dir.mkdir(exist_ok=True)

        # 设置Python logging
        self.logger = logging.getLogger("forum_engine")
        self.logger.setLevel(logging.INFO)

        # 文件handler
        log_file = self.log_dir / f"debate_{datetime.now().strftime('%Y%m%d')}.log"
        file_handler = logging.FileHandler(log_file, encoding='utf-8')
        file_handler.setLevel(logging.INFO)

        # 格式化
        formatter = logging.Formatter(
            '%(asctime)s - %(name)s - %(levelname)s - %(message)s'
        )
        file_handler.setFormatter(formatter)

        self.logger.addHandler(file_handler)

    def log_debate_start(self, analysis_id: str, topic: str):
        """记录辩论开始。"""
        self.logger.info(f"[DEBATE_START] analysis_id={analysis_id}, topic={topic}")

    def log_debate_complete(
        self,
        analysis_id: str,
        total_tokens: int,
        cost_cny: float,
        duration_ms: int,
        similarity_avg: float
    ):
        """记录辩论完成。"""
        self.logger.info(
            f"[DEBATE_COMPLETE] analysis_id={analysis_id}, "
            f"tokens={total_tokens}, cost=¥{cost_cny:.4f}, "
            f"duration={duration_ms}ms, similarity={similarity_avg:.3f}"
        )

    def log_debate_error(self, analysis_id: str, error: str):
        """记录辩论错误。"""
        self.logger.error(f"[DEBATE_ERROR] analysis_id={analysis_id}, error={error}")

    def log_llm_call(
        self,
        agent_name: str,
        round_number: int,
        tokens: int,
        duration_ms: int,
        status: str = "success"
    ):
        """记录LLM调用。"""
        self.logger.info(
            f"[LLM_CALL] agent={agent_name}, round={round_number}, "
            f"tokens={tokens}, duration={duration_ms}ms, status={status}"
        )

    def log_similarity_check(
        self,
        agent_pairs: Dict[str, float],
        avg_similarity: float,
        passed: bool
    ):
        """记录观点相似度检查。"""
        status = "PASS" if passed else "FAIL"
        self.logger.info(
            f"[SIMILARITY_CHECK] {status}, avg={avg_similarity:.3f}, "
            f"pairs={json.dumps(agent_pairs, ensure_ascii=False)}"
        )

    def save_debate_json(self, analysis_id: str, debate_result: Dict[str, Any]):
        """保存完整辩论结果到JSON文件（用于审计和调试）。"""
        json_file = self.log_dir / f"debate_{analysis_id}.json"

        with open(json_file, 'w', encoding='utf-8') as f:
            json.dump(debate_result, f, ensure_ascii=False, indent=2)

        self.logger.info(f"[DEBATE_SAVED] {json_file}")


# ══════════════════════════════════════════════════════════
# 全局单例
# ══════════════════════════════════════════════════════════

_debate_logger: Optional[DebateLogger] = None


def get_logger() -> DebateLogger:
    """获取全局DebateLogger实例。"""
    global _debate_logger
    if _debate_logger is None:
        _debate_logger = DebateLogger()
    return _debate_logger


# ══════════════════════════════════════════════════════════
# 性能监控
# ══════════════════════════════════════════════════════════

class PerformanceMonitor:
    """性能监控器。"""

    def __init__(self):
        """初始化监控器。"""
        self.metrics = {
            "total_debates": 0,
            "total_tokens": 0,
            "total_cost_cny": 0.0,
            "avg_duration_ms": 0.0,
            "avg_similarity": 0.0,
            "error_count": 0
        }

    def record_debate(
        self,
        tokens: int,
        cost_cny: float,
        duration_ms: int,
        similarity_avg: float,
        error: bool = False
    ):
        """记录一次辩论的性能指标。"""
        self.metrics["total_debates"] += 1
        self.metrics["total_tokens"] += tokens
        self.metrics["total_cost_cny"] += cost_cny

        # 更新平均值
        n = self.metrics["total_debates"]
        self.metrics["avg_duration_ms"] = (
            (self.metrics["avg_duration_ms"] * (n - 1) + duration_ms) / n
        )
        self.metrics["avg_similarity"] = (
            (self.metrics["avg_similarity"] * (n - 1) + similarity_avg) / n
        )

        if error:
            self.metrics["error_count"] += 1

    def get_metrics(self) -> Dict[str, Any]:
        """获取当前指标。"""
        return self.metrics.copy()

    def get_summary(self) -> str:
        """获取指标摘要（人类可读）。"""
        m = self.metrics
        success_rate = (
            (m["total_debates"] - m["error_count"]) / m["total_debates"] * 100
            if m["total_debates"] > 0 else 0
        )

        return f"""
Performance Summary:
  Total Debates: {m['total_debates']}
  Success Rate: {success_rate:.1f}%
  Total Tokens: {m['total_tokens']:,}
  Total Cost: ¥{m['total_cost_cny']:.2f}
  Avg Duration: {m['avg_duration_ms']:.0f}ms
  Avg Similarity: {m['avg_similarity']:.3f}
  Error Count: {m['error_count']}
""".strip()


# ══════════════════════════════════════════════════════════
# 全局性能监控实例
# ══════════════════════════════════════════════════════════

_performance_monitor: Optional[PerformanceMonitor] = None


def get_monitor() -> PerformanceMonitor:
    """获取全局PerformanceMonitor实例。"""
    global _performance_monitor
    if _performance_monitor is None:
        _performance_monitor = PerformanceMonitor()
    return _performance_monitor


# ══════════════════════════════════════════════════════════
# 测试
# ══════════════════════════════════════════════════════════

if __name__ == "__main__":
    import sys
    import io

    # 修复Windows GBK编码问题
    if sys.platform == 'win32':
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')

    # 测试日志记录器
    logger = get_logger()
    logger.log_debate_start("test_001", "测试舆情主题")
    logger.log_llm_call("事实核查员", 1, 1500, 2500)
    logger.log_debate_complete("test_001", 14500, 0.021, 38000, 0.42)

    print("[OK] Logger test completed")

    # 测试性能监控
    monitor = get_monitor()
    monitor.record_debate(14500, 0.021, 38000, 0.42, error=False)
    monitor.record_debate(15000, 0.023, 42000, 0.38, error=False)
    monitor.record_debate(13800, 0.020, 35000, 0.45, error=True)

    print("\n" + monitor.get_summary())
