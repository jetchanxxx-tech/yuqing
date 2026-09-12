# 盘古舆情 · Yuging Platform

AI 原生 SaaS 舆情监测平台 — 面向中小企业与个人品牌，用多 Agent 辩论协作将"监测"升级为"研判"。

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react)](https://react.dev)
[![Python](https://img.shields.io/badge/Python-3.11-3776AB?logo=python)](https://python.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15-4169E1?logo=postgresql)](https://postgresql.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue)](LICENSE)

---

## 核心差异化

| 传统舆情工具 | 盘古舆情 |
|-------------|---------|
| ¥3-8万/年，商务谈判 | **¥0-499/月**，自助注册 |
| 监测数据 → 人工总结 | **多 Agent 辩论** → AI 研判报告 |
| 文本为主 | 文本 + 短视频 + 图片 **多模态** |
| 模板固定，周期长 | LLM 驱动，注册后 **5 分钟出报告** |

## 架构

```
nginx → Go API(:8080) + Worker        ← 平台层（模块化单体）
              │
    Queue (memory / Redis / RabbitMQ)
              │
    Python FastAPI × 5                  ← 引擎层
    query / media / insight / report / forum
              │
    PostgreSQL 15 (DB-per-tenant) + Redis 7
```

**核心设计原则：**
- **接口优先**：每个模块暴露 Go interface，提取微服务只需换一个实现
- **配置驱动**：读写分离、队列驱动、存储引擎、搜索引擎均为配置开关
- **DB-per-tenant**：注册自动建库，物理隔离，合规友好
- **两层分层**：`platform/`（平台库）与 `business/`（租户库）互不越界

## 快速开始

### 环境要求

- Go 1.22+
- Node.js 18+
- Python 3.11+
- PostgreSQL 15
- Redis 7

### 本地开发

```bash
# 克隆
git clone https://github.com/jetchanxxx-tech/yuging-platform.git
cd yuging-platform

# Go 平台
cd platform
cp config.example.yaml config.yaml   # 编辑数据库连接等
go run ./cmd/server                    # API → :8080
go run ./cmd/worker                    # Worker

# 前端
cd web
npm install && npm run dev             # Vite → :5173

# Python 引擎（按需）
cd engines/query_engine
python -m venv venv && source venv/bin/activate
pip install -r ../requirements.txt
uvicorn main:app --port 8000
```

### 测试

```bash
cd platform
go test ./...          # 70 tests
go vet ./...           # lint
make build             # 交叉编译 linux/amd64
```

## 项目结构

```
yuging-platform/
├── platform/               Go 平台
│   ├── cmd/                server / worker / cli
│   ├── internal/
│   │   ├── platform/       auth, tenant, user, usage, billing
│   │   ├── business/       analysis, datasource, report, dashboard
│   │   ├── api/            router + v1 handlers + middleware
│   │   ├── engine/         引擎接口契约
│   │   └── pkg/            db, queue, llm, storage, id, errors, observ
│   └── migrations/         platform + tenant 两套迁移
├── web/                    React 18 + Ant Design 5
├── engines/                Python FastAPI × 5
│   ├── query_engine/       多源搜索 + 去重
│   ├── media_engine/       视频/图片多模态
│   ├── insight_engine/     情感分析 + 话题聚类
│   ├── report_engine/      模板 IR → HTML/PDF/MD/DOCX
│   └── forum_engine/       多 Agent 辩论协调
├── design/                 UI 设计稿 (12 HTML pages)
├── scripts/                deploy.sh + nginx + systemd
└── docs/                   architecture-plan, PRD
```

## 订阅套餐

| 功能 | Free | Pro (¥99/月) | Business (¥499/月) | Enterprise |
|------|------|-------------|-------------------|------------|
| 月度分析 | 5次 | 50次 | 500次 | 无限制 |
| LLM Token | 1M | 10M | 100M | 无限制 |
| 预算模式 | hard cap | hard cap | overage | 无 cap |
| 数据源 | 5个 | 全部 | 全部+优先 | 全部+自定义 |
| 导出格式 | HTML | HTML+MD | +PDF+DOCX | +PPTX |
| 辩论分析 | ✗ | ✓ | ✓ | ✓ |
| API | ✗ | ✗ | 只读 | 完整 |

## API

```
POST   /api/v1/auth/register         注册（自动创建租户DB）
POST   /api/v1/auth/login            登录
POST   /api/v1/auth/refresh          刷新 token

GET    /api/v1/analyses              分析任务列表
POST   /api/v1/analyses              创建分析
GET    /api/v1/analyses/:id          任务详情
GET    /api/v1/analyses/:id/result   分析结果
GET    /api/v1/analyses/:id/events   实时进度 (SSE)

GET    /api/v1/reports               报告列表
GET    /api/v1/reports/:id/download  下载报告

GET    /api/v1/dashboard/overview    概览数据
GET    /api/v1/dashboard/trend       趋势图数据

GET    /api/v1/billing/plans         套餐列表
GET    /api/v1/billing/usage         用量概览
GET    /api/v1/billing/invoices      账单记录

GET    /api/v1/admin/tenants         租户管理 (platform_admin)
```

所有响应格式：`{"code": "...", "message": "...", "details": {}, "request_id": "..."}`

## 部署到生产

```bash
# 一键部署（幂等，已安装组件自动跳过）
sudo bash scripts/deploy.sh

# 手动步骤
cd platform && make build              # Go → bin/
cd web && npm install && npm run build # 前端 → web/dist/
# Python engines
cd engines && python -m venv venv && pip install -r requirements.txt
# 配置
cp platform/config.example.yaml /opt/yuging/config/config.yaml
# 编辑 JWT secret、LLM API keys、DB 密码
# 启动
systemctl start yuging-server yuging-worker
```

访问 `https://your-domain.com`

## 开发路线

| Phase | 内容 | 状态 |
|-------|------|------|
| 1 | 核心平台 (auth/tenant/user/analysis) | ✅ |
| 2 | 计费 + LLM 计量 | ✅ |
| 3 | Queue + Usage Meter + 引擎契约 | ✅ |
| 4 | 前端 12 页面 | ✅ |
| 5 | Python 引擎实际实现 | 🔜 |
| 6 | 多 Agent 辩论 (ForumEngine) | 🔜 |
| 7 | 多模态 (MediaEngine) | 🔜 |
| 8 | 扩展能力 (RabbitMQ/ES/S3) | 🔜 |

## 技术栈

| 层 | 选择 |
|---|------|
| API | Go 1.22+ / Gin / pgx v5 / goose |
| 前端 | React 18 / TypeScript / Ant Design 5 / ECharts 5 |
| 引擎 | Python 3.11 / FastAPI |
| 数据库 | PostgreSQL 15 (database-per-tenant) |
| 缓存 | Redis 7 |
| 队列 | memory(MVP) → Redis/RabbitMQ/NATS |
| LLM | OpenAI 兼容 API (DeepSeek/Kimi/Moonshot) |
| 部署 | Go 二进制 + systemd（无容器） |

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=jetchanxxx-tech/yuging-platform&type=Date)](https://star-history.com/#jetchanxxx-tech/yuging-platform&Date)

---

灵感来源：[BettaFish/微舆](https://github.com/666ghj/BettaFish) — 感谢开源社区。
