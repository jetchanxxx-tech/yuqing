#!/usr/bin/env python3
"""
LLM配置测试脚本 - 验证insight_engine的LLM调用能力

测试场景：
1. 基础连接测试（简单对话）
2. JSON输出测试（情感分类）
3. 并发调用测试（模拟维度分析）
4. 错误恢复测试（超时/重试）
5. 降级模式测试（quick mode）
"""

import asyncio
import json
import os
import sys
import time
from pathlib import Path

# 添加common到Python路径
sys.path.insert(0, str(Path(__file__).parent.parent / "common"))

from llm_client import build_client


# ==================== 测试配置 ====================

API_KEY = os.getenv("LLM_API_KEY", "")
BASE_URL = os.getenv("LLM_BASE_URL", "")
MODEL = os.getenv("LLM_MODEL", "gpt-4o-mini")

# 测试数据
SAMPLE_DOCS = [
    {
        "id": "1",
        "content": "这款车的后排空间真的很大，坐三个成年人完全没问题，长途旅行很舒适。",
        "source": "xiaohongshu"
    },
    {
        "id": "2",
        "content": "动力响应太慢了，踩油门半天才有反应，高速超车很吃力。",
        "source": "weibo"
    },
    {
        "id": "3",
        "content": "内饰做工精致，用料考究，中控屏幕操作流畅，科技感十足。",
        "source": "douyin"
    }
]

# ==================== 测试用例 ====================

async def test_basic_connection(client):
    """测试1: 基础连接 - 简单对话"""
    print("\n" + "="*60)
    print("测试1: 基础连接测试")
    print("="*60)

    try:
        start = time.time()
        response = await client.chat(
            messages=[
                {"role": "system", "content": "你是一个汽车评论分析助手。"},
                {"role": "user", "content": "请用一句话介绍你自己。"}
            ],
            temperature=0.7
        )
        elapsed = time.time() - start

        print(f"[PASS] 连接成功")
        print(f"  响应时间: {elapsed:.2f}秒")
        print(f"  响应内容: {response[:100]}...")
        return True
    except Exception as e:
        print(f"[FAIL] 连接失败: {e}")
        return False


async def test_json_output(client):
    """测试2: JSON输出 - 情感分类"""
    print("\n" + "="*60)
    print("测试2: JSON格式输出测试（情感分类）")
    print("="*60)

    doc = SAMPLE_DOCS[0]
    prompt = f"""分析以下汽车评论的情感倾向：

评论: {doc['content']}

请以JSON格式返回分析结果：
{{
  "sentiment": "positive/neutral/negative",
  "confidence": 0.0-1.0,
  "keywords": ["关键词1", "关键词2"]
}}"""

    try:
        start = time.time()
        response = await client.chat(
            messages=[
                {"role": "system", "content": "你是专业的汽车评论分析师。"},
                {"role": "user", "content": prompt}
            ],
            temperature=0.3,
            response_format={"type": "json_object"}
        )
        elapsed = time.time() - start

        # 尝试解析JSON
        result = json.loads(response)

        print(f"[PASS] JSON输出成功")
        print(f"  响应时间: {elapsed:.2f}秒")
        print(f"  解析结果: {json.dumps(result, ensure_ascii=False, indent=2)}")
        return True
    except json.JSONDecodeError as e:
        print(f"[FAIL] JSON解析失败: {e}")
        print(f"  原始响应: {response[:200]}")
        return False
    except Exception as e:
        print(f"[FAIL] 测试失败: {e}")
        return False


async def test_concurrent_calls(client):
    """测试3: 并发调用 - 模拟维度分析"""
    print("\n" + "="*60)
    print("测试3: 并发调用测试（3个维度并行分析）")
    print("="*60)

    dimensions = ["空间舒适性", "动力性能", "内饰质感"]

    async def analyze_dimension(dim: str, doc: dict):
        prompt = f"""分析评论中关于"{dim}"的观点：

评论: {doc['content']}

返回JSON格式：
{{
  "dimension": "{dim}",
  "mentioned": true/false,
  "sentiment": "positive/neutral/negative",
  "quote": "相关原文片段"
}}"""

        response = await client.chat(
            messages=[
                {"role": "system", "content": "你是汽车评论分析专家。"},
                {"role": "user", "content": prompt}
            ],
            temperature=0.3,
            response_format={"type": "json_object"}
        )
        return json.loads(response)

    try:
        start = time.time()

        # 并发执行3个维度分析
        tasks = [
            analyze_dimension(dim, SAMPLE_DOCS[i % len(SAMPLE_DOCS)])
            for i, dim in enumerate(dimensions)
        ]
        results = await asyncio.gather(*tasks)

        elapsed = time.time() - start

        print(f"[PASS] 并发调用成功")
        print(f"  总耗时: {elapsed:.2f}秒")
        print(f"  平均每个维度: {elapsed/len(dimensions):.2f}秒")
        print(f"  结果预览:")
        for result in results:
            print(f"    - {result.get('dimension')}: {result.get('mentioned')} ({result.get('sentiment')})")

        return True
    except Exception as e:
        print(f"[FAIL] 并发测试失败: {e}")
        return False


async def test_error_recovery(client):
    """测试4: 错误恢复 - 超时重试"""
    print("\n" + "="*60)
    print("测试4: 错误恢复测试（超时重试）")
    print("="*60)

    # 创建一个短超时的客户端
    short_timeout_client = build_client(
        api_key=API_KEY,
        base_url=BASE_URL,
        model=MODEL,
        timeout=1.0  # 1秒超时
    )

    long_prompt = "请详细分析以下100条汽车评论..." + "评论内容..." * 100

    try:
        print("  尝试1秒超时请求...")
        start = time.time()

        try:
            response = await short_timeout_client.chat(
                messages=[
                    {"role": "user", "content": long_prompt}
                ],
                temperature=0.5
            )
            print(f"  意外成功（可能模型响应很快）")
        except asyncio.TimeoutError:
            print(f"  [OK] 正确触发超时（{time.time()-start:.2f}秒）")

        # 使用正常超时重试
        print("  使用正常超时重试...")
        start = time.time()
        response = await client.chat(
            messages=[
                {"role": "user", "content": "简单测试"}
            ],
            temperature=0.5
        )
        elapsed = time.time() - start

        print(f"[PASS] 错误恢复成功")
        print(f"  重试耗时: {elapsed:.2f}秒")
        return True

    except Exception as e:
        print(f"[FAIL] 错误恢复测试失败: {e}")
        return False


async def test_quick_mode(client):
    """测试5: 快速模式 - 降级分析"""
    print("\n" + "="*60)
    print("测试5: 快速模式测试（降级场景）")
    print("="*60)

    # 模拟quick模式的简化prompt
    quick_prompt = f"""快速分类以下评论的情感（仅回答positive/neutral/negative之一）：

{SAMPLE_DOCS[1]['content']}

情感:"""

    try:
        start = time.time()
        response = await client.chat(
            messages=[
                {"role": "user", "content": quick_prompt}
            ],
            temperature=0.1,
            max_tokens=10  # 限制输出长度
        )
        elapsed = time.time() - start

        sentiment = response.strip().lower()

        print(f"[PASS] 快速模式成功")
        print(f"  响应时间: {elapsed:.2f}秒")
        print(f"  分类结果: {sentiment}")
        print(f"  符合快速返回预期: {'是' if elapsed < 2.0 else '否'}")

        return True
    except Exception as e:
        print(f"[FAIL] 快速模式测试失败: {e}")
        return False


# ==================== 主测试流程 ====================

async def main():
    """主测试流程"""
    print("\n" + "=" * 60)
    print("  Insight Engine LLM配置测试")
    print("=" * 60)

    # 检查环境变量
    print("\n[CONFIG] 配置信息:")
    print(f"  API Key: {'[OK] 已设置' if API_KEY else '[ERROR] 未设置'}")
    print(f"  Base URL: {BASE_URL or '[ERROR] 未设置'}")
    print(f"  Model: {MODEL}")

    if not API_KEY or not BASE_URL:
        print("\n[ERROR] 请设置环境变量 LLM_API_KEY 和 LLM_BASE_URL")
        sys.exit(1)

    # 构建客户端
    print("\n[INIT] 初始化LLM客户端...")
    client = build_client(
        api_key=API_KEY,
        base_url=BASE_URL,
        model=MODEL,
        timeout=30.0
    )
    print("  [OK] 客户端创建成功")

    # 执行测试
    results = {}

    results["basic_connection"] = await test_basic_connection(client)
    results["json_output"] = await test_json_output(client)
    results["concurrent_calls"] = await test_concurrent_calls(client)
    results["error_recovery"] = await test_error_recovery(client)
    results["quick_mode"] = await test_quick_mode(client)

    # 汇总结果
    print("\n" + "="*60)
    print("[SUMMARY] 测试结果汇总")
    print("="*60)

    passed = sum(results.values())
    total = len(results)

    for test_name, result in results.items():
        status = "[PASS]" if result else "[FAIL]"
        print(f"  {status}  {test_name}")

    print(f"\n总计: {passed}/{total} 通过")

    if passed == total:
        print("\n[SUCCESS] 所有测试通过！LLM配置正常。")
        return 0
    else:
        print(f"\n[WARNING] {total - passed}个测试失败，请检查配置。")
        return 1


if __name__ == "__main__":
    exit_code = asyncio.run(main())
    sys.exit(exit_code)
