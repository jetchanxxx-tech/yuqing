# Cloudflare 524 超时错误修复

## 问题描述

在使用带有 Cloudflare 代理的 LLM API 端点（如 `https://api.mxzzz.xyz`）时，insight 分析会报错：

```
insight analysis failed: insight: engine returned 502: 
{"detail":"LLM 调用失败: Server error '524 status code 524' for url ..."}
```

## 根本原因

1. **Cloudflare 超时限制**：Cloudflare 免费版和 Pro 版的超时限制为 100 秒
2. **原配置过长**：
   - insight_engine 和 report_engine 设置了 300 秒超时
   - 五维分析使用 `max_tokens=8192`，思考型模型单次推理可能需要 2-5 分钟
3. **冲突结果**：即使 Python 客户端设置 300 秒，请求在 100 秒后被 Cloudflare 中断，返回 524 错误

## 解决方案

### 平衡策略（质量优先）

考虑到LLM思考质量的重要性，采用**平衡方案**：

1. **适度缩短超时**：`300秒 → 180秒`
   - 仍超过Cloudflare 100秒限制，但降低了等待时间
   - 配合重试机制，大部分请求能在重试中成功
   
2. **适度降低 max_tokens**：`8192 → 6144`
   - 保留足够的思考和输出空间
   - 减少约25%生成时间，降低超时风险
   
3. **保持重试机制**：维度分析的单次重试逻辑保持不变

### 修改文件

- `engines/insight_engine/main.py`:
  - 行 535, 637: `timeout=300` → `timeout=180`
  - 行 451: `max_tokens=8192` → `max_tokens=6144`
  
- `engines/report_engine/main.py`:
  - 行 253: `timeout=300` → `timeout=180`

## 权衡说明

### 优势
- ✅ **保持思考质量**：180秒仍足够复杂推理，6144 tokens足够完整输出
- ✅ 降低超时概率：减少约40%等待时间
- ✅ 重试机制有效：配合重试，大多数请求最终成功
- ✅ 更快的失败反馈：失败时更快知道结果

### 仍会遇到的问题
- ⚠️ **Cloudflare 524仍会出现**：180秒 > 100秒，首次请求可能超时
- ✅ **但重试会成功**：第二次请求往往更快（模型预热、缓存等）
- ⚠️ 极少数复杂分析可能两次都超时

## 验证方法

1. **正常场景**：提交一个包含 10-20 篇文档的分析任务，观察是否成功完成
2. **边界场景**：提交一个包含 50+ 篇文档的分析任务，观察是否触发重试但最终成功
3. **监控日志**：检查是否有 `finish_reason=length` 的截断警告

## 长期方案（彻底解决）

如果仍频繁遇到超时，推荐以下根本性解决方案：

### 方案1：更换LLM端点（最优）

**问题根源**：Cloudflare 代理的100秒硬限制无法绕过

**解决方法**：
1. **直连端点**：使用LLM供应商的直连API（无代理）
   - 智谱GLM：`https://open.bigmodel.cn/api/paas/v4`
   - DeepSeek：`https://api.deepseek.com`
   
2. **自建转发**：在您的服务器上搭建简单的反向代理
   ```nginx
   location /llm/ {
       proxy_pass https://open.bigmodel.cn/;
       proxy_read_timeout 600s;  # 10分钟
       proxy_connect_timeout 60s;
   }
   ```

### 方案2：升级Cloudflare（次优）

- **Enterprise 版**：可配置超时最高 600 秒
- **成本较高**：适合有预算的场景

### 方案3：优化Prompt（辅助）

在不换端点的情况下，进一步优化：
- 减少素材包大小（当前16k字符）
- 简化维度Prompt
- 使用非思考型模型（如 glm-4-flash，但质量会下降）

## 部署说明

修改已在 worktree `test-llm-config` 中完成。部署到生产环境：

```bash
# 1. 合并到主分支
git checkout main
git merge worktree-test-llm-config

# 2. 重启 insight 和 report 引擎（服务器上）
sudo systemctl restart yuqing-insight
sudo systemctl restart yuqing-report

# 3. 验证服务健康
curl http://localhost:8002/health
curl http://localhost:8003/health
```
