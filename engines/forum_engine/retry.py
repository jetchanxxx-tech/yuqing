"""Retry mechanism and error handling for LLM calls.

实现指数退避重试、超时处理、降级策略。
"""
import asyncio
import time
from typing import Callable, Any, Optional, Tuple
from functools import wraps
import httpx


class RetryConfig:
    """重试配置。"""

    def __init__(
        self,
        max_retries: int = 3,
        base_delay: float = 1.0,
        max_delay: float = 10.0,
        exponential_base: float = 2.0
    ):
        """初始化重试配置。

        Args:
            max_retries: 最大重试次数
            base_delay: 基础延迟（秒）
            max_delay: 最大延迟（秒）
            exponential_base: 指数退避基数
        """
        self.max_retries = max_retries
        self.base_delay = base_delay
        self.max_delay = max_delay
        self.exponential_base = exponential_base

    def get_delay(self, retry_count: int) -> float:
        """计算第N次重试的延迟时间（指数退避）。"""
        delay = self.base_delay * (self.exponential_base ** retry_count)
        return min(delay, self.max_delay)


async def retry_with_backoff(
    func: Callable,
    *args,
    config: Optional[RetryConfig] = None,
    **kwargs
) -> Tuple[Any, int]:
    """带指数退避的重试包装器。

    Args:
        func: 异步函数
        *args: 函数参数
        config: 重试配置
        **kwargs: 函数关键字参数

    Returns:
        (result, retry_count): 函数结果和实际重试次数

    Raises:
        最后一次失败的异常
    """
    config = config or RetryConfig()
    last_exception = None

    for attempt in range(config.max_retries + 1):
        try:
            result = await func(*args, **kwargs)
            return result, attempt

        except (httpx.TimeoutException, httpx.HTTPStatusError, asyncio.TimeoutError) as e:
            last_exception = e

            if attempt < config.max_retries:
                delay = config.get_delay(attempt)
                await asyncio.sleep(delay)
                continue
            else:
                # 最后一次重试也失败了
                raise last_exception

        except Exception as e:
            # 非网络错误，不重试
            raise e

    # 理论上不会到达这里
    raise last_exception


def with_retry(config: Optional[RetryConfig] = None):
    """重试装饰器。

    Usage:
        @with_retry(RetryConfig(max_retries=3))
        async def my_func():
            ...
    """
    def decorator(func: Callable):
        @wraps(func)
        async def wrapper(*args, **kwargs):
            result, retry_count = await retry_with_backoff(
                func, *args, config=config, **kwargs
            )
            return result
        return wrapper
    return decorator


# ══════════════════════════════════════════════════════════
# 降级策略
# ══════════════════════════════════════════════════════════

class DegradationStrategy:
    """降级策略。"""

    @staticmethod
    async def reduce_agent_count(
        orchestrator,
        topic: str,
        data_summary: dict,
        max_rounds: int = 3
    ) -> dict:
        """降级策略1：减少Agent数量（4 → 3）。

        移除"传播路径专家"，保留事实核查员/情绪分析师/处置建议官。
        """
        # TODO: 实现3-Agent版本
        raise NotImplementedError("3-Agent degradation not implemented yet")

    @staticmethod
    async def reduce_rounds(
        orchestrator,
        topic: str,
        data_summary: dict
    ) -> dict:
        """降级策略2：减少轮次（3 → 2）。

        只进行Round 1和Round 3，跳过Round 2交叉质询。
        """
        # TODO: 实现2-Round版本
        raise NotImplementedError("2-Round degradation not implemented yet")

    @staticmethod
    def return_error_response(error: Exception) -> dict:
        """降级策略3：返回503错误（符合CEO要求：不返回Mock）。"""
        raise error  # 直接抛出异常，让FastAPI返回503


# ══════════════════════════════════════════════════════════
# 超时保护
# ══════════════════════════════════════════════════════════

async def with_timeout(
    coro,
    timeout_seconds: float,
    error_message: str = "Operation timed out"
):
    """为协程添加超时保护。

    Args:
        coro: 协程
        timeout_seconds: 超时时间（秒）
        error_message: 超时错误信息

    Raises:
        asyncio.TimeoutError: 超时
    """
    try:
        return await asyncio.wait_for(coro, timeout=timeout_seconds)
    except asyncio.TimeoutError:
        raise asyncio.TimeoutError(error_message)


# ══════════════════════════════════════════════════════════
# 错误分类
# ══════════════════════════════════════════════════════════

class ErrorCategory:
    """错误分类。"""

    @staticmethod
    def is_retryable(error: Exception) -> bool:
        """判断错误是否可重试。"""
        retryable_types = (
            httpx.TimeoutException,
            httpx.HTTPStatusError,
            asyncio.TimeoutError,
            ConnectionError
        )

        if isinstance(error, retryable_types):
            return True

        # HTTP 5xx错误可重试
        if isinstance(error, httpx.HTTPStatusError):
            return 500 <= error.response.status_code < 600

        return False

    @staticmethod
    def get_error_type(error: Exception) -> str:
        """获取错误类型（用于日志和监控）。"""
        if isinstance(error, httpx.TimeoutException):
            return "timeout"
        elif isinstance(error, httpx.HTTPStatusError):
            status = error.response.status_code
            if 400 <= status < 500:
                return "client_error"
            elif 500 <= status < 600:
                return "server_error"
            else:
                return "http_error"
        elif isinstance(error, asyncio.TimeoutError):
            return "timeout"
        elif isinstance(error, ConnectionError):
            return "connection_error"
        else:
            return "unknown_error"


# ══════════════════════════════════════════════════════════
# 测试
# ══════════════════════════════════════════════════════════

async def test_retry_mechanism():
    """测试重试机制。"""
    import sys
    import io

    # 修复Windows GBK编码问题
    if sys.platform == 'win32':
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')

    print("[TEST] Retry mechanism")

    # 模拟会失败2次然后成功的函数
    call_count = 0

    async def flaky_function():
        nonlocal call_count
        call_count += 1
        print(f"  Attempt {call_count}")

        if call_count < 3:
            raise httpx.TimeoutException("Simulated timeout")

        return "Success!"

    # 测试重试
    config = RetryConfig(max_retries=3, base_delay=0.1)
    result, retry_count = await retry_with_backoff(flaky_function, config=config)

    print(f"\n[OK] Result: {result}")
    print(f"[OK] Retry count: {retry_count}")

    # 测试错误分类
    errors = [
        httpx.TimeoutException("timeout"),
        httpx.HTTPStatusError("error", request=None, response=type('obj', (object,), {'status_code': 503})()),
        ValueError("value error")
    ]

    print("\n[TEST] Error classification:")
    for error in errors:
        retryable = ErrorCategory.is_retryable(error)
        error_type = ErrorCategory.get_error_type(error)
        print(f"  {type(error).__name__}: retryable={retryable}, type={error_type}")


if __name__ == "__main__":
    asyncio.run(test_retry_mechanism())
