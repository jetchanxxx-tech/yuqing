"""Integration tests for Forum Engine.

集成测试套件，验证完整的多Agent辩论流程。
"""
import asyncio
import os
from typing import Dict, Any
import sys
import io

# 修复Windows GBK编码问题
if sys.platform == 'win32':
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')

# 导入测试模块
from agents import AGENTS, validate_agents
from prompts import get_all_prompts
from test_cases import TEST_CASES, get_test_case
from database import get_all_sql_statements
from logging_system import get_logger, get_monitor


class IntegrationTests:
    """集成测试类。"""

    def __init__(self):
        self.test_results = []
        self.logger = get_logger()

    def test_agents_config(self):
        """测试1: Agent配置验证。"""
        print("\n[TEST 1] Agent Configuration")
        try:
            validate_agents()
            assert len(AGENTS) == 4, "Should have 4 agents"

            # 验证温度参数差异化
            temps = [a.temperature for a in AGENTS]
            assert len(set(temps)) >= 3, "Temperature should be diverse"

            # 验证人设长度
            for agent in AGENTS:
                assert len(agent.persona) >= 100, f"{agent.name} persona too short"
                assert len(agent.focus_areas) >= 3, f"{agent.name} needs more focus areas"

            print("  ✓ PASS: 4 agents validated")
            print(f"  ✓ Temperature range: {min(temps):.1f} - {max(temps):.1f}")
            self.test_results.append(("Agent Configuration", True, None))

        except Exception as e:
            print(f"  ✗ FAIL: {e}")
            self.test_results.append(("Agent Configuration", False, str(e)))

    def test_prompts_templates(self):
        """测试2: Prompt模板验证。"""
        print("\n[TEST 2] Prompt Templates")
        try:
            prompts = get_all_prompts()
            assert len(prompts) == 15, f"Should have 15 prompts, got {len(prompts)}"

            # 验证主持人模板
            for r in [1, 2, 3]:
                key = f"moderator_r{r}"
                assert key in prompts, f"Missing {key}"

            # 验证专家模板
            agent_keys = ["fact_checker", "emotion_analyst", "propagation_expert", "action_advisor"]
            for agent_key in agent_keys:
                for r in [1, 2, 3]:
                    key = f"{agent_key}_r{r}"
                    assert key in prompts, f"Missing {key}"

            print(f"  ✓ PASS: {len(prompts)} prompt templates validated")
            self.test_results.append(("Prompt Templates", True, None))

        except Exception as e:
            print(f"  ✗ FAIL: {e}")
            self.test_results.append(("Prompt Templates", False, str(e)))

    def test_test_cases(self):
        """测试3: 测试用例验证。"""
        print("\n[TEST 3] Test Cases")
        try:
            assert len(TEST_CASES) == 10, f"Should have 10 test cases, got {len(TEST_CASES)}"

            # 验证每个测试用例的完整性
            for case in TEST_CASES:
                assert "id" in case, "Missing id"
                assert "topic" in case, "Missing topic"
                assert "summary" in case, "Missing summary"

                # 验证summary字段
                summary = case["summary"]
                required_fields = [
                    "doc_count", "time_range", "platforms",
                    "sentiment_positive", "sentiment_negative", "sentiment_neutral",
                    "top_keywords"
                ]
                for field in required_fields:
                    assert field in summary, f"Missing summary.{field} in {case['id']}"

            # 测试多样性
            negative_ratios = [c["summary"]["sentiment_negative"] for c in TEST_CASES]
            assert min(negative_ratios) < 50, "Need some positive cases"
            assert max(negative_ratios) > 70, "Need some negative cases"

            print(f"  ✓ PASS: {len(TEST_CASES)} test cases validated")
            print(f"  ✓ Sentiment diversity: {min(negative_ratios)}% - {max(negative_ratios)}% negative")
            self.test_results.append(("Test Cases", True, None))

        except Exception as e:
            print(f"  ✗ FAIL: {e}")
            self.test_results.append(("Test Cases", False, str(e)))

    def test_database_schema(self):
        """测试4: 数据库schema验证。"""
        print("\n[TEST 4] Database Schema")
        try:
            sql_statements = get_all_sql_statements()
            assert len(sql_statements) == 3, f"Should have 3 tables, got {len(sql_statements)}"

            # 验证表名
            expected_tables = ["debates", "debate_metrics", "llm_call_logs"]
            for i, table_name in enumerate(expected_tables):
                assert table_name in sql_statements[i].lower(), f"Missing table {table_name}"

            # 验证关键字段
            debates_sql = sql_statements[0]
            assert "analysis_id" in debates_sql.lower(), "Missing analysis_id in debates"
            assert "foreign key" in debates_sql.lower(), "Missing FK constraint"

            print(f"  ✓ PASS: {len(sql_statements)} tables validated")
            print(f"  ✓ Tables: {', '.join(expected_tables)}")
            self.test_results.append(("Database Schema", True, None))

        except Exception as e:
            print(f"  ✗ FAIL: {e}")
            self.test_results.append(("Database Schema", False, str(e)))

    def test_logging_system(self):
        """测试5: 日志系统验证。"""
        print("\n[TEST 5] Logging System")
        try:
            logger = get_logger()
            monitor = get_monitor()

            # 测试日志记录
            logger.log_debate_start("test_001", "测试主题")
            logger.log_llm_call("测试Agent", 1, 1000, 2000)
            logger.log_debate_complete("test_001", 10000, 0.015, 30000, 0.42)

            # 测试性能监控
            monitor.record_debate(10000, 0.015, 30000, 0.42, error=False)
            metrics = monitor.get_metrics()

            assert metrics["total_debates"] >= 1, "Should record debates"
            assert metrics["total_tokens"] >= 10000, "Should record tokens"

            print("  ✓ PASS: Logging system functional")
            print(f"  ✓ Recorded: {metrics['total_debates']} debates, {metrics['total_tokens']} tokens")
            self.test_results.append(("Logging System", True, None))

        except Exception as e:
            print(f"  ✗ FAIL: {e}")
            self.test_results.append(("Logging System", False, str(e)))

    def test_environment_check(self):
        """测试6: 环境检查。"""
        print("\n[TEST 6] Environment Check")
        try:
            # 检查依赖库
            try:
                import httpx
                import sklearn
                deps_ok = True
            except ImportError as e:
                deps_ok = False
                raise AssertionError(f"Missing dependency: {e}")

            # 检查API Key（可选）
            api_key = os.getenv("ZHIPU_API_KEY")
            if api_key:
                print("  ✓ ZHIPU_API_KEY found (LLM available)")
            else:
                print("  ⚠ ZHIPU_API_KEY not set (Mock mode only)")

            print("  ✓ PASS: Environment ready")
            self.test_results.append(("Environment Check", True, None))

        except Exception as e:
            print(f"  ✗ FAIL: {e}")
            self.test_results.append(("Environment Check", False, str(e)))

    def run_all_tests(self):
        """运行所有测试。"""
        print("=" * 60)
        print("Forum Engine Integration Tests")
        print("=" * 60)

        self.test_agents_config()
        self.test_prompts_templates()
        self.test_test_cases()
        self.test_database_schema()
        self.test_logging_system()
        self.test_environment_check()

        # 总结
        print("\n" + "=" * 60)
        print("Test Summary")
        print("=" * 60)

        total = len(self.test_results)
        passed = sum(1 for _, success, _ in self.test_results if success)
        failed = total - passed

        for test_name, success, error in self.test_results:
            status = "✓ PASS" if success else "✗ FAIL"
            print(f"  {status}: {test_name}")
            if error:
                print(f"    Error: {error}")

        print(f"\nTotal: {total}, Passed: {passed}, Failed: {failed}")
        print(f"Success Rate: {passed/total*100:.1f}%")

        if failed == 0:
            print("\n🎉 All tests passed! Ready for deployment.")
            return True
        else:
            print(f"\n⚠️  {failed} test(s) failed. Please fix before deployment.")
            return False


def main():
    """主测试入口。"""
    tests = IntegrationTests()
    success = tests.run_all_tests()
    sys.exit(0 if success else 1)


if __name__ == "__main__":
    main()
