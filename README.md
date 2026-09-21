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
| 定制开发热榜监测 | **内置多平台热榜聚合**（微博/B站/知乎实时快照） |

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
git clone https://github.com/jetchanxxx-tech/yuqing-platform.git
cd yuqing-platform

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
yuqing-platform/
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
└── docs/                   按用途归档：planning/（规划）user/（用户）ops/（运维）dev/（开发）
```

## 订阅套餐（方案 B：额度包 + 渗透定价）

| 套餐 | 价格 | 月度额度 | 分析模式 | 导出格式 | 支付方式 |
|------|------|---------|---------|---------|---------|
| **体验版** | ¥0 | 1 次试用 | 速览（3维） | HTML | — |
| **速览版** | ¥99/月 | 4 次 | 速览（3维） | HTML+MD | 支付宝/微信/银联 |
| **研判版** | ¥999/月 | 10 次 | 完整（5维） | HTML+MD+PDF+DOCX | 同上 |
| **旗舰版** | ¥4999/月 | 50 次 | 完整（5维）+ 优先队列 | 全格式+PPTX | 同上 + 对公转账 |

**加购包**：¥69/次（单次报告加购，适用任意套餐）

**计费规则**：
- 每次分析 Create/Rerun 各扣 1 次额度
- 管线失败/取消自动回补
- 额度不足返回 `402 NO_CREDITS`
- 新注册赠 1 次试用额度
- Beta 公测期额度不过期

**支付渠道**：支付宝扫码/微信扫码/银联网关（admin 后台配置商户参数即时生效）

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

GET    /api/v1/billing/plans         套餐列表 + 加购 SKU + 支付渠道
GET    /api/v1/billing/credits       额度余额
GET    /api/v1/billing/transactions  额度流水
POST   /api/v1/billing/orders        创建支付订单
GET    /api/v1/billing/orders/:id    订单详情（轮询即对账）

GET    /api/v1/trends                多平台热榜快照（微博/B站/知乎）

GET    /api/v1/admin/tenants         租户管理 (platform_admin)
GET    /api/v1/admin/settings        平台配置（含支付渠道）
PUT    /api/v1/admin/settings        更新配置
```

所有响应格式：`{"code": "...", "message": "...", "details": {}, "request_id": "..."}`

## 部署到生产

```bash
# 一键部署（幂等，已安装组件自动跳过）
sudo YUQING_DOMAIN=your-domain.com bash scripts/deploy.sh

# 当前生产环境：yuqing2.pangu-cloud.com (101.96.209.90:22352)
# CentOS Stream 9 / oneinstack 栈，详见 docs/ops/DEPLOYMENT_RUNBOOK.md §9

# 手动步骤
cd platform && make build              # Go → bin/
cd web && npm install && npm run build # 前端 → web/dist/
# Python engines
cd engines && python -m venv venv && pip install -r requirements.txt
# 配置
cp platform/config.example.yaml /opt/yuqing/config/config.yaml
# 编辑 JWT secret、LLM API keys、DB 密码
# 启动
systemctl start yuqing-server yuqing-worker
```

访问 `https://your-domain.com`

**部署注意事项**：
- **本地编译原则**：Go 交叉编译 + npm build + RSSHub 打包后传输，禁止服务器上 `go build`/`npm install`（防 OOM）
- **迁移顺序**：先 `yuqing-cli migrate platform` 再启动 server（收费体系需迁移 0006）
- **支付渠道配置**：Admin 后台填写商户参数（支付宝 app_id + 私钥、微信 mch_id + 证书、银联 mer_id + pfx）即时生效
- **RSSHub 热榜**：systemd unit 需 `ExecStart=/usr/local/node/bin/node` 全路径（oneinstack 安装的 Node.js 不在 sudo PATH）

## 开发路线

| Phase | 内容 | 状态 |
|-------|------|------|
| 1 | 核心平台 (auth/tenant/user/analysis) | ✅ |
| 2 | 计费 + LLM 计量 | ✅ |
| 3 | Queue + Usage Meter + 引擎契约 | ✅ |
| 4 | 前端 12 页面 | ✅ |
| 5 | Python 引擎实际实现 (query/insight/report) | ✅ |
| 6 | F18 收费体系（额度包 + 三支付渠道） | ✅ |
| 7 | F21 多平台热榜聚合（RSSHub） | ✅ |
| 8 | 多 Agent 辩论 (ForumEngine) | 🔜 |
| 9 | 多模态 (MediaEngine) | 🔜 |
| 10 | 扩展能力 (RabbitMQ/ES/S3) | 🔜 |
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

[![Star History Chart](https://api.star-history.com/svg?repos=jetchanxxx-tech/yuqing-platform&type=Date)](https://star-history.com/#jetchanxxx-tech/yuqing-platform&Date)

---

灵感来源：[BettaFish/微舆](https://github.com/666ghj/BettaFish) — 感谢开源社区。
