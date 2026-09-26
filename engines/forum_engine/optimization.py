"""Performance optimization utilities.

包括Prompt缓存、批处理优化、内存管理。
"""
from typing import Dict, Any, Optional, List
import hashlib
import json
from datetime import datetime, timedelta
from collections import OrderedDict


class PromptCache:
    """Prompt结果缓存（LRU）。

    用于缓存相同Prompt的LLM响应，减少重复调用。
    """

    def __init__(self, max_size: int = 100, ttl_minutes: int = 60):
        """初始化缓存。

        Args:
            max_size: 最大缓存条目数
            ttl_minutes: 缓存过期时间（分钟）
        """
        self.max_size = max_size
        self.ttl = timedelta(minutes=ttl_minutes)
        self.cache: OrderedDict[str, tuple[Any, datetime]] = OrderedDict()

    def _make_key(self, prompt: str, temperature: float) -> str:
        """生成缓存key（prompt + temperature的hash）。"""
        content = f"{prompt}|{temperature}"
        return hashlib.md5(content.encode()).hexdigest()

    def get(self, prompt: str, temperature: float) -> Optional[Any]:
        """获取缓存结果。"""
        key = self._make_key(prompt, temperature)

        if key not in self.cache:
            return None

        result, timestamp = self.cache[key]

        # 检查是否过期
        if datetime.now() - timestamp > self.ttl:
            del self.cache[key]
            return None

        # 移到末尾（LRU）
        self.cache.move_to_end(key)
        return result

    def put(self, prompt: str, temperature: float, result: Any):
        """存入缓存。"""
        key = self._make_key(prompt, temperature)

        # 如果已满，删除最旧的
        if len(self.cache) >= self.max_size:
            self.cache.popitem(last=False)

        self.cache[key] = (result, datetime.now())

    def clear(self):
        """清空缓存。"""
        self.cache.clear()

    def get_stats(self) -> Dict[str, Any]:
        """获取缓存统计。"""
        now = datetime.now()
        expired_count = sum(
            1 for _, timestamp in self.cache.values()
            if now - timestamp > self.ttl
        )

        return {
            "size": len(self.cache),
            "max_size": self.max_size,
            "expired": expired_count,
            "ttl_minutes": self.ttl.total_seconds() / 60
        }


# ══════════════════════════════════════════════════════════
# 批处理优化
# ══════════════════════════════════════════════════════════

class BatchOptimizer:
    """批处理优化器。

    优化并发LLM调用的批次大小和顺序。
    """

    @staticmethod
    def optimize_batch_order(
        prompts: List[tuple[str, float]],
        max_batch_size: int = 4
    ) -> List[List[tuple[str, float]]]:
        """优化批处理顺序。

        策略：按temperature分组，同温度的一起调用（更好的批处理效率）。

        Args:
            prompts: [(prompt, temperature), ...] 列表
            max_batch_size: 最大批次大小

        Returns:
            分批后的列表
        """
        # 按temperature分组
        temp_groups: Dict[float, List[tuple[str, float]]] = {}
        for prompt, temp in prompts:
            if temp not in temp_groups:
                temp_groups[temp] = []
            temp_groups[temp].append((prompt, temp))

        # 将每组分批
        batches = []
        for group in temp_groups.values():
            for i in range(0, len(group), max_batch_size):
                batches.append(group[i:i + max_batch_size])

        return batches

    @staticmethod
    def estimate_tokens(prompt: str) -> int:
        """估算Prompt的token数（粗略估计：中文1字≈1.5token，英文1词≈1.3token）。"""
        # 简单估算：字符数 / 2
        return len(prompt) // 2


# ══════════════════════════════════════════════════════════
# 内存管理
# ══════════════════════════════════════════════════════════

class MemoryManager:
    """内存管理器。

    监控和优化内存使用。
    """

    @staticmethod
    def truncate_long_text(text: str, max_length: int = 2000) -> str:
        """截断过长文本（用于日志和缓存）。"""
        if len(text) <= max_length:
            return text
        return text[:max_length] + f"... (truncated, original length: {len(text)})"

    @staticmethod
    def cleanup_debate_result(result: Dict[str, Any]) -> Dict[str, Any]:
        """清理辩论结果，减少内存占用（用于长期存储）。

        策略：
        - 保留rounds中的核心字段
        - 移除冗余的timestamp
        - 压缩重复文本
        """
        cleaned = result.copy()

        # 清理rounds
        if "rounds" in cleaned:
            cleaned_rounds = []
            for r in cleaned["rounds"]:
                cleaned_r = {
                    "round": r["round"],
                    "agent": r["agent"],
                    "statement": MemoryManager.truncate_long_text(r["statement"], 1000)
                }
                cleaned_rounds.append(cleaned_r)
            cleaned["rounds"] = cleaned_rounds

        return cleaned


# ══════════════════════════════════════════════════════════
# 性能指标
# ══════════════════════════════════════════════════════════

class PerformanceMetrics:
    """性能指标计算。"""

    @staticmethod
    def calculate_throughput(
        total_debates: int,
        total_duration_ms: int
    ) -> float:
        """计算吞吐量（辩论/秒）。"""
        if total_duration_ms == 0:
            return 0.0
        return total_debates / (total_duration_ms / 1000)

    @staticmethod
    def calculate_token_efficiency(
        total_tokens: int,
        total_debates: int
    ) -> float:
        """计算token效率（tokens/辩论）。"""
        if total_debates == 0:
            return 0.0
        return total_tokens / total_debates

    @staticmethod
    def calculate_cost_per_debate(
        total_cost_cny: float,
        total_debates: int
    ) -> float:
        """计算单次辩论成本（元/辩论）。"""
        if total_debates == 0:
            return 0.0
        return total_cost_cny / total_debates

    @staticmethod
    def get_performance_report(metrics: Dict[str, Any]) -> str:
        """生成性能报告。"""
        throughput = PerformanceMetrics.calculate_throughput(
            metrics.get("total_debates", 0),
            int(metrics.get("avg_duration_ms", 0) * metrics.get("total_debates", 0))
        )

        token_efficiency = PerformanceMetrics.calculate_token_efficiency(
            metrics.get("total_tokens", 0),
            metrics.get("total_debates", 0)
        )

        cost_per_debate = PerformanceMetrics.calculate_cost_per_debate(
            metrics.get("total_cost_cny", 0.0),
            metrics.get("total_debates", 0)
        )

        return f"""
Performance Report:
  Throughput: {throughput:.2f} debates/sec
  Token Efficiency: {token_efficiency:.0f} tokens/debate
  Cost Efficiency: ¥{cost_per_debate:.4f} per debate
  Avg Similarity: {metrics.get('avg_similarity', 0.0):.3f}
  Success Rate: {(1 - metrics.get('error_count', 0) / max(metrics.get('total_debates', 1), 1)) * 100:.1f}%
""".strip()


# ══════════════════════════════════════════════════════════
# 全局缓存实例
# ══════════════════════════════════════════════════════════

_global_cache: Optional[PromptCache] = None


def get_cache() -> PromptCache:
    """获取全局Prompt缓存实例。"""
    global _global_cache
    if _global_cache is None:
        _global_cache = PromptCache()
    return _global_cache


# ══════════════════════════════════════════════════════════
# 测试
# ══════════════════════════════════════════════════════════

if __name__ == "__main__":
    import sys
    import io

    # 修复Windows GBK编码问题
    if sys.platform == 'win32':
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')

    # 测试缓存
    cache = get_cache()
    cache.put("test prompt", 0.7, {"content": "test result"})
    result = cache.get("test prompt", 0.7)
    print(f"[OK] Cache test: {result}")
    print(f"[OK] Cache stats: {cache.get_stats()}")

    # 测试批处理优化
    prompts = [
        ("prompt1", 0.2),
        ("prompt2", 0.5),
        ("prompt3", 0.2),
        ("prompt4", 0.3),
        ("prompt5", 0.5),
    ]
    batches = BatchOptimizer.optimize_batch_order(prompts, max_batch_size=2)
    print(f"\n[OK] Batch optimization: {len(batches)} batches")
    for i, batch in enumerate(batches):
        print(f"  Batch {i+1}: {[t for _, t in batch]}")

    # 测试性能指标
    test_metrics = {
        "total_debates": 10,
        "total_tokens": 145000,
        "total_cost_cny": 0.21,
        "avg_duration_ms": 38000,
        "avg_similarity": 0.42,
        "error_count": 1
    }
    print(f"\n{PerformanceMetrics.get_performance_report(test_metrics)}")
