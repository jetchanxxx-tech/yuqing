# Insight Engine LLM配置测试

## 测试目的

验证insight_engine的LLM调用能力，确保所有关键场景正常工作。

## 测试场景

1. **基础连接测试** - 验证LLM API可达性和基本对话能力
2. **JSON输出测试** - 验证结构化数据输出（情感分类场景）
3. **并发调用测试** - 验证多维度并行分析能力
4. **错误恢复测试** - 验证超时和重试机制
5. **快速模式测试** - 验证降级场景下的简化分析

## 使用方法

### 1. 设置环境变量

```bash
# Linux/Mac
export LLM_API_KEY="your-api-key-here"
export LLM_BASE_URL="https://api.example.com/v1"
export LLM_MODEL="gpt-4o-mini"  # 可选，默认gpt-4o-mini

# Windows PowerShell
$env:LLM_API_KEY="your-api-key-here"
$env:LLM_BASE_URL="https://api.example.com/v1"
$env:LLM_MODEL="gpt-4o-mini"
```

### 2. 运行测试

```bash
# 在insight_engine目录下
python test_llm_config.py
```

### 3. 查看结果

测试会输出详细的执行过程和结果：

```
🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗
  Insight Engine LLM配置测试
🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗🚗

📋 配置信息:
  API Key: ✓ 已设置
  Base URL: https://api.example.com/v1
  Model: gpt-4o-mini

...

📊 测试结果汇总
============================================================
  ✓ PASS  basic_connection
  ✓ PASS  json_output
  ✓ PASS  concurrent_calls
  ✓ PASS  error_recovery
  ✓ PASS  quick_mode

总计: 5/5 通过

🎉 所有测试通过！LLM配置正常。
```

## 常见问题

### Q: 测试失败怎么办？

**A:** 根据失败的测试场景排查：

- **basic_connection失败**: 检查API_KEY和BASE_URL是否正确
- **json_output失败**: 检查模型是否支持JSON模式
- **concurrent_calls失败**: 检查API速率限制
- **error_recovery失败**: 网络或超时配置问题
- **quick_mode失败**: 模型响应延迟问题

### Q: 如何调整超时时间？

**A:** 编辑`test_llm_config.py`中的timeout参数：

```python
client = build_client(
    api_key=API_KEY,
    base_url=BASE_URL,
    model=MODEL,
    timeout=60.0  # 改为60秒
)
```

### Q: 如何测试其他模型？

**A:** 修改环境变量`LLM_MODEL`：

```bash
export LLM_MODEL="gpt-4"  # 或其他支持的模型
```

## 与生产环境的关系

此测试脚本使用与`main.py`完全相同的：
- LLM客户端构建逻辑（`build_client`）
- 环境变量配置
- 异步调用模式
- 错误处理机制

**测试通过 = 生产环境可用**

## 下一步

测试通过后，可以：

1. 启动insight_engine服务：`python main.py`
2. 检查服务日志确认LLM调用正常
3. 通过API创建分析任务验证端到端流程
