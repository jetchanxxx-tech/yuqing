# Forum Engine - 多Agent辩论系统

**版本**: v0.3.0  
**状态**: ✅ 生产就绪

## 概述

Forum Engine是盘古舆情的核心差异化功能，通过4个专家Agent进行3轮辩论，提供深度舆情研判。

### 核心特性

- ✅ **4专家Agent系统**：事实核查员/情绪分析师/传播路径专家/处置建议官
- ✅ **3轮辩论流程**：议题设定 → 交叉质询 → 综合研判
- ✅ **三层差异化设计**：人设对立 + 维度分工 + 温度差异
- ✅ **完整审计链路**：所有LLM调用记录到数据库
- ✅ **生产环境标准**：错误重试、性能监控、日志系统

## 技术指标

| 指标 | 数值 |
|------|------|
| 单次成本 | ¥0.021 (14k tokens) |
| 平均耗时 | 38秒 |
| 毛利率 | 99.98% |
| 观点相似度 | < 0.6 (目标) |
| 成功率 | 100% (集成测试) |

## 目录结构

```
engines/forum_engine/
├── main.py              # FastAPI服务入口
├── agents.py            # 4个Agent角色定义
├── prompts.py           # 15个Prompt模板
├── llm_client.py        # GLM-4-flash客户端
├── orchestrator.py      # 辩论协调器
├── test_cases.py        # 10个测试用例
├── database.py          # 数据库模型（3张表）
├── logging_system.py    # 日志和性能监控
├── retry.py             # 错误重试机制
├── optimization.py      # 性能优化（缓存/批处理）
└── test_integration.py  # 集成测试套件
```

## 快速开始

### 1. 安装依赖

```bash
pip install httpx scikit-learn fastapi uvicorn
```

### 2. 设置环境变量

```bash
export ZHIPU_API_KEY="your_glm_api_key"
```

### 3. 运行测试

```bash
cd engines/forum_engine
python test_integration.py
```

输出：
```
Total: 6, Passed: 6, Failed: 0
Success Rate: 100.0%
🎉 All tests passed! Ready for deployment.
```

### 4. 启动服务

```bash
cd engines/forum_engine
uvicorn main:app --host 0.0.0.0 --port 8002
```

### 5. 测试API

```bash
curl -X POST http://localhost:8002/run_forum \
  -H "Content-Type: application/json" \
  -d '{
    "topic": "测试舆情主题",
    "documents": [],
    "analysis_id": "test_001",
    "max_rounds": 3
  }'
```

## 数据库部署

### 创建3张新表

在Go Platform的PostgreSQL数据库中执行：

```sql
-- 1. debates 表
CREATE TABLE IF NOT EXISTS debates (
    id SERIAL PRIMARY KEY,
    analysis_id VARCHAR(50) NOT NULL,
    topic TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    rounds JSONB NOT NULL DEFAULT '[]',
    verdict TEXT,
    confidence FLOAT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_analysis FOREIGN KEY (analysis_id)
        REFERENCES analyses(id) ON DELETE CASCADE
);

-- 2. debate_metrics 表
CREATE TABLE IF NOT EXISTS debate_metrics (
    id SERIAL PRIMARY KEY,
    debate_id INTEGER NOT NULL,
    total_tokens INTEGER NOT NULL,
    cost_cny DECIMAL(10, 4) NOT NULL,
    duration_ms INTEGER NOT NULL,
    similarity_avg FLOAT,
    similarity_max FLOAT,
    retry_count INTEGER DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_debate FOREIGN KEY (debate_id)
        REFERENCES debates(id) ON DELETE CASCADE
);

-- 3. llm_call_logs 表
CREATE TABLE IF NOT EXISTS llm_call_logs (
    id SERIAL PRIMARY KEY,
    debate_id INTEGER NOT NULL,
    agent_name VARCHAR(50) NOT NULL,
    round_number INTEGER NOT NULL,
    prompt TEXT NOT NULL,
    response TEXT NOT NULL,
    tokens_prompt INTEGER NOT NULL,
    tokens_completion INTEGER NOT NULL,
    tokens_total INTEGER NOT NULL,
    cost_cny DECIMAL(10, 6) NOT NULL,
    duration_ms INTEGER NOT NULL,
    status VARCHAR(20) NOT NULL,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_debate FOREIGN KEY (debate_id)
        REFERENCES debates(id) ON DELETE CASCADE
);

-- 创建索引
CREATE INDEX idx_debates_analysis_id ON debates(analysis_id);
CREATE INDEX idx_debates_status ON debates(status);
CREATE INDEX idx_debate_metrics_debate_id ON debate_metrics(debate_id);
CREATE INDEX idx_llm_call_logs_debate_id ON llm_call_logs(debate_id);
```

## API文档

### POST /run_forum

运行多Agent辩论。

**请求体**：
```json
{
  "topic": "舆情主题",
  "documents": [...],
  "analysis_id": "分析ID",
  "max_rounds": 3
}
```

**响应**：
```json
{
  "rounds": [
    {
      "round": 1,
      "agent": "主持人",
      "role": "议题设定",
      "statement": "...",
      "timestamp": 1234567890.123
    },
    ...
  ],
  "verdict": "最终研判",
  "confidence": 0.85
}
```

**错误码**：
- `503`: LLM服务不可用（符合CEO要求：不返回Mock）

### GET /health

健康检查。

**响应**：
```json
{
  "status": "ok",
  "engine": "forum",
  "version": "0.3.0"
}
```

## 配置

### 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `ZHIPU_API_KEY` | 智谱AI API Key | 无（必需） |

### 重试配置

在 `retry.py` 中修改 `RetryConfig`：

```python
RetryConfig(
    max_retries=3,        # 最大重试次数
    base_delay=1.0,       # 基础延迟（秒）
    max_delay=10.0,       # 最大延迟（秒）
    exponential_base=2.0  # 指数退避基数
)
```

### 缓存配置

在 `optimization.py` 中修改 `PromptCache`：

```python
PromptCache(
    max_size=100,      # 最大缓存条目
    ttl_minutes=60     # 过期时间（分钟）
)
```

## 监控

### 日志文件

- 位置：`logs/debate_YYYYMMDD.log`
- 格式：时间戳 + 级别 + 消息
- 内容：辩论开始/完成、LLM调用、错误

### 性能指标

使用 `PerformanceMonitor` 实时统计：

```python
from logging_system import get_monitor

monitor = get_monitor()
metrics = monitor.get_metrics()
print(monitor.get_summary())
```

输出：
```
Performance Summary:
  Total Debates: 100
  Success Rate: 98.0%
  Total Tokens: 1,450,000
  Total Cost: ¥2.18
  Avg Duration: 38000ms
  Avg Similarity: 0.42
```

## 故障排查

### 问题1：LLM调用超时

**症状**：`httpx.TimeoutException`

**解决**：
- 检查网络连接
- 增加超时时间（`llm_client.py` 中 `timeout` 参数）
- 检查API Key额度

### 问题2：观点相似度过高

**症状**：`similarity_avg > 0.6`

**解决**：
- 检查Prompt模板是否足够差异化
- 增加温度参数范围
- 强化人设对立性

### 问题3：成本过高

**症状**：单次成本 > ¥0.03

**解决**：
- 检查Prompt长度
- 启用缓存减少重复调用
- 优化批处理顺序

## 开发日志

- **Day 1**: Agent角色定义 + Prompt模板库
- **Day 2**: LLM客户端 + 辩论协调器
- **Day 3**: FastAPI集成
- **Day 4-5**: 数据库模型 + 日志系统
- **Day 6-7**: 错误重试 + 性能优化
- **Day 8-10**: 集成测试 + 部署准备

## 符合CEO要求

- ✅ 真实LLM调用（非Mock）
- ✅ LLM失败返回503（不返回Mock数据）
- ✅ 完整审计链路（所有调用记录到数据库）
- ✅ 生产环境标准（错误处理/日志/监控）
- ✅ 三层防观点雷同机制

## 许可

内部项目 - 盘古舆情系统
