# 快速开始

本指南将帮助你在 5 分钟内完成盘古舆情的体验与部署。

## 🎯 在线演示（0 分钟）

无需安装，直接体验完整分析流程：

```bash
# 克隆仓库
git clone https://github.com/jetchanxxx-tech/yuqing.git
cd yuqing

# 双击打开
demo/index.html
```

**演示内容**："雅阁后排空间争议" 7 步完整分析
- 数据采集（17 条真实文档）
- 情感分析（负面 58% / 中性 29% / 正面 13%）
- 话题聚类（3 个核心话题）
- 五维研判（背景/热度/情感/分群/深层原因）
- HTML 报告生成

## 🚀 本地开发（5 分钟）

### 前置要求

- Go 1.25+
- Python 3.11+
- Node.js 18+
- PostgreSQL 15（可选，默认内存模式）
- Redis 7（可选）

### 1. 克隆并启动后端

```bash
# Go 平台层
cd platform
make test          # 确保测试通过（353 用例）
go run ./cmd/server

# 服务启动在 :8080
```

### 2. 配置 Python 引擎

```bash
cd engines
python3 -m venv venv
source venv/bin/activate  # Windows: venv\Scripts\activate
pip install -r requirements.txt

# 安装 Scrapling Chromium（首次 ~150MB）
scrapling install --chromium

# 测试
python3 -m pytest tests/ -v
```

**启动 5 个引擎**（新终端）：

```bash
# 端口分配：8000=query 8001=media 8002=insight 8003=report 8004=forum
cd engines/query_engine && uvicorn main:app --port 8000 &
cd engines/media_engine && uvicorn main:app --port 8001 &
cd engines/insight_engine && uvicorn main:app --port 8002 &
cd engines/report_engine && uvicorn main:app --port 8003 &
cd engines/forum_engine && uvicorn main:app --port 8004 &
```

### 3. 启动前端

```bash
cd web
npm ci
npm run dev

# 前端启动在 :5173
# API 自动代理到 http://127.0.0.1:8080
```

### 4. 访问应用

打开浏览器：http://localhost:5173

**首次注册自动获得**：
- 1 次免费试用额度
- `tenant_admin` 角色

## 🔑 配置 API Key（真实采集）

### Bocha 搜索引擎

1. 注册：https://open.bochaai.com
2. 获取 API Key（sk-xxx 格式）
3. 配置方式：

```bash
# 方式 1：环境变量（推荐）
export BOCHA_API_KEY=sk-your-key-here

# 方式 2：管理后台配置
# 访问 http://localhost:5173/admin
# 「数据源配置」→ 填写 Bocha Key
```

### LLM 供应商（五维研判）

支持智谱 GLM / DeepSeek / 任意 OpenAI 兼容端点：

```bash
# 智谱（默认，推荐）
export LLM_API_KEY=your-zhipu-key
export LLM_BASE_URL=https://open.bigmodel.cn/api/paas/v4
export LLM_MODEL=glm-5.3-flash

# 或 DeepSeek
export LLM_API_KEY=your-deepseek-key
export LLM_BASE_URL=https://api.deepseek.com/v1
export LLM_MODEL=deepseek-chat

# 或中转站
export LLM_API_KEY=sk-xxx
export LLM_BASE_URL=https://sub.geiliapi.com/v1
export LLM_MODEL=deepseek-v4.1-flash
```

**管理后台在线配置**（零重启生效）：
- 访问 `/admin` → 数据源配置
- 填写 Key / 端点 / 模型三字段

## 📝 创建第一个分析任务

1. **登录** → 注册账号
2. **新建分析** → 点击右上角 "新建分析"
3. **填写参数**：
   - 任务名称：`新品上线舆情`
   - 关键词：`新品 OR 上线 OR 发布`（支持布尔逻辑）
   - 数据源：勾选"微博"、"新闻"、"小红书"
   - 分析深度：选择"快速分析"（Free/Lite）或"深度研判"（Pro/Enterprise）
4. **提交** → 等待 2-5 分钟

**查看结果**：
- 实时进度（SSE 推送）
- 情感分析图表
- 五维研判结论
- 下载 HTML 报告

## 🎓 下一步

- [[本地开发|Local-Development]] - 完整开发环境配置
- [[API 参考|API-Reference]] - REST API 文档
- [[架构设计|Architecture]] - 理解系统架构
- [[测试指南|Testing-Guide]] - 运行完整测试套件

## ⚠️ 常见问题

### 1. 分析任务卡在 `queued` 状态

**原因**：引擎未启动或配置错误

**解决**：
```bash
# 检查引擎进程
ps aux | grep uvicorn

# 检查端口占用
netstat -tlnp | grep 800[0-4]

# 查看 server 日志
# 应该看到 5 行 "engine configured: query_engine at http://127.0.0.1:8000"
```

### 2. 采集到 0 条文档

**原因**：Bocha Key 未配置

**解决**：
```bash
# 确认环境变量
echo $BOCHA_API_KEY

# 或检查管理后台「数据源配置」
curl http://localhost:8080/api/v1/admin/settings \
  -H "Authorization: Bearer <your-token>"
```

### 3. 五维研判全部失败

**原因**：LLM API Key 未配置或额度不足

**解决**：
```bash
# 确认 LLM 配置
echo $LLM_API_KEY
echo $LLM_BASE_URL
echo $LLM_MODEL

# 测试 LLM 连通性
curl $LLM_BASE_URL/models \
  -H "Authorization: Bearer $LLM_API_KEY"
```

### 4. 前端 401 错误

**原因**：Token 过期

**解决**：前端自动刷新 Token，若持续报错则重新登录

---

**需要帮助？** [提交 Issue](https://github.com/jetchanxxx-tech/yuqing/issues)
