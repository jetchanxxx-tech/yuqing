"""LLM client for calling GLM-4-flash API.

负责调用智谱AI的GLM-4-flash模型，支持并发调用和错误处理。
"""
import os
import asyncio
from typing import Optional, Dict, Any
import httpx


class LLMClient:
    """GLM-4-flash API client."""

    def __init__(self, api_key: Optional[str] = None, base_url: Optional[str] = None):
        """初始化LLM客户端。

        Args:
            api_key: 智谱AI API Key，默认从环境变量ZHIPU_API_KEY读取
            base_url: API base URL，默认为智谱AI官方地址
        """
        self.api_key = api_key or os.getenv("ZHIPU_API_KEY")
        if not self.api_key:
            raise ValueError("ZHIPU_API_KEY not found in environment variables")

        self.base_url = base_url or "https://open.bigmodel.cn/api/paas/v4"
        self.model = "glm-4-flash"
        self.timeout = 30.0  # 30秒超时

    async def call(
        self,
        prompt: str,
        temperature: float = 0.7,
        max_tokens: int = 2048,
    ) -> Dict[str, Any]:
        """调用LLM生成回复。

        Args:
            prompt: 输入Prompt
            temperature: 温度参数（0.0-1.0）
            max_tokens: 最大token数

        Returns:
            {
                "content": "生成的文本",
                "tokens": {
                    "prompt_tokens": 100,
                    "completion_tokens": 200,
                    "total_tokens": 300
                },
                "model": "glm-4-flash"
            }

        Raises:
            httpx.TimeoutException: 超时
            httpx.HTTPStatusError: HTTP错误
        """
        headers = {
            "Authorization": f"Bearer {self.api_key}",
            "Content-Type": "application/json"
        }

        payload = {
            "model": self.model,
            "messages": [
                {"role": "user", "content": prompt}
            ],
            "temperature": temperature,
            "max_tokens": max_tokens,
        }

        async with httpx.AsyncClient(timeout=self.timeout) as client:
            response = await client.post(
                f"{self.base_url}/chat/completions",
                headers=headers,
                json=payload
            )
            response.raise_for_status()

            data = response.json()

            # 解析响应
            choice = data["choices"][0]
            usage = data["usage"]

            return {
                "content": choice["message"]["content"],
                "tokens": {
                    "prompt_tokens": usage["prompt_tokens"],
                    "completion_tokens": usage["completion_tokens"],
                    "total_tokens": usage["total_tokens"]
                },
                "model": data["model"]
            }

    async def call_batch(
        self,
        prompts: list[tuple[str, float]],  # [(prompt, temperature), ...]
        max_tokens: int = 2048,
    ) -> list[Dict[str, Any]]:
        """并发调用多个Prompt。

        Args:
            prompts: [(prompt, temperature), ...] 列表
            max_tokens: 最大token数

        Returns:
            结果列表，顺序与输入一致
        """
        tasks = [
            self.call(prompt, temperature, max_tokens)
            for prompt, temperature in prompts
        ]
        return await asyncio.gather(*tasks, return_exceptions=True)


# ══════════════════════════════════════════════════════════
# 辅助函数
# ══════════════════════════════════════════════════════════

def calculate_cost_cny(total_tokens: int) -> float:
    """计算GLM-4-flash的成本（人民币）。

    GLM-4-flash定价：¥0.0015/1k tokens
    """
    return (total_tokens / 1000) * 0.0015


def format_llm_error(error: Exception) -> str:
    """格式化LLM错误信息。"""
    if isinstance(error, httpx.TimeoutException):
        return "LLM调用超时（30s）"
    elif isinstance(error, httpx.HTTPStatusError):
        return f"LLM API错误：HTTP {error.response.status_code}"
    else:
        return f"LLM调用失败：{str(error)}"


# ══════════════════════════════════════════════════════════
# 测试
# ══════════════════════════════════════════════════════════

async def test_llm_client():
    """测试LLM客户端。"""
    import sys
    import io

    # 修复Windows GBK编码问题
    if sys.platform == 'win32':
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')

    try:
        client = LLMClient()
        print("[OK] LLM Client initialized")

        # 测试单次调用
        result = await client.call(
            prompt="你好，请用一句话介绍你自己。",
            temperature=0.7
        )

        print(f"\n[OK] LLM Response:")
        print(f"  Content: {result['content'][:100]}...")
        print(f"  Tokens: {result['tokens']}")
        print(f"  Cost: ¥{calculate_cost_cny(result['tokens']['total_tokens']):.4f}")

        # 测试并发调用
        prompts = [
            ("用一句话描述春天", 0.7),
            ("用一句话描述夏天", 0.7),
        ]

        results = await client.call_batch(prompts)
        print(f"\n[OK] Batch call completed: {len(results)} results")

        for i, result in enumerate(results):
            if isinstance(result, Exception):
                print(f"  [{i}] Error: {format_llm_error(result)}")
            else:
                print(f"  [{i}] {result['content'][:50]}...")

    except Exception as e:
        print(f"[ERROR] {format_llm_error(e)}")
        raise


if __name__ == "__main__":
    asyncio.run(test_llm_client())
