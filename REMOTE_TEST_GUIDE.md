# 远程服务器测试指南

由于本地环境SSH连接存在限制，需要手动在服务器上执行LLM配置测试。

## 服务器信息

- **IP**: 101.96.209.90
- **用户**: root
- **项目路径**: /root/yuqing

## 测试步骤

### 1. 连接到服务器

```bash
ssh root@101.96.209.90
# 输入密码后登录
```

### 2. 切换到测试分支

```bash
cd /root/yuqing
git fetch origin
git checkout worktree-test-llm-config
git pull origin worktree-test-llm-config
```

### 3. 运行LLM配置测试

```bash
cd engines/insight_engine
python3 test_llm_config.py
```

## 预期结果

### 成功情况

```
=== LLM Configuration Test Suite ===

Test 1: Basic Connection
✓ Basic connection test passed

Test 2: JSON Output
✓ JSON output test passed

Test 3: Concurrent Calls
✓ Concurrent calls test passed (5 dimensions)

Test 4: Error Recovery
✓ Error recovery test passed

Test 5: Fast Mode
✓ Fast mode test passed

=== All Tests Passed ===
```

### 失败情况

如果测试失败，会显示具体错误信息，例如：
- API密钥配置问题
- 网络连接问题
- 服务不可用

## 故障排查

### 检查环境变量

```bash
cd /root/yuqing/engines/insight_engine
cat .env | grep -E "ZHIPU_API_KEY|OPENAI_API_KEY"
```

### 检查Python依赖

```bash
cd /root/yuqing/engines/insight_engine
pip3 list | grep -E "zhipuai|openai"
```

### 查看详细配置文档

```bash
cd /root/yuqing/engines/insight_engine
cat TEST_LLM_README.md
```

## 本地执行的限制

由于Windows环境下的SSH客户端限制，无法直接从本地自动化执行远程命令。建议：

1. 使用SSH客户端（如PuTTY、MobaXterm）手动连接
2. 或在服务器上设置SSH密钥认证（推荐）

## 后续优化

如需实现自动化远程测试，可以：

1. 配置SSH密钥认证（无密码登录）
2. 使用Ansible等自动化工具
3. 将测试集成到CI/CD流水线
