# 盘古舆情 Wiki

欢迎来到 **盘古舆情** 官方文档！

## 📖 什么是盘古舆情？

盘古舆情是一个 **AI 原生 SaaS 舆情监测平台**，面向中小企业与个人品牌。通过 Scrapling 自适应爬虫 + Bocha AI 搜索实现真实数据采集，用多 Agent 辩论协作将"监测"升级为"研判"。

**核心差异化**：
- ✅ 真实数据采集（Scrapling 自适应爬虫 + Bocha 搜索引擎）
- ✅ 五维研判体系（背景/热度/情感/分群/深层原因）
- ✅ AI 原生工作流（多 Agent 辩论 + 批判重写）
- ✅ 多平台热榜聚合（微博/B站/知乎实时快照）
- ✅ 灵活计费模型（额度包 + 按次扣减 + 渗透定价）

## 🚀 快速导航

### 新手入门
- [[快速开始|Quick-Start]] - 5 分钟上手指南
- [[产品演示|Demo]] - 在线体验（demo/index.html）
- [[套餐选择|Pricing]] - 了解订阅计划

### 开发者文档
- [[架构设计|Architecture]] - 模块化单体架构
- [[API 参考|API-Reference]] - 完整 REST API
- [[本地开发|Local-Development]] - 开发环境搭建
- [[测试指南|Testing-Guide]] - TDD 实践与测试分层

### 部署运维
- [[生产部署|Production-Deployment]] - Ubuntu 24.04 + yuqing2 CentOS Stream 9
- [[配置参考|Configuration]] - 环境变量与配置文件
- [[故障排查|Troubleshooting]] - 常见问题与解决方案
- [[监控告警|Monitoring]] - 系统监控与告警策略

### 功能特性
- [[数据采集|Data-Collection]] - Scrapling + Bocha 工作原理
- [[五维研判|Five-Dimension-Analysis]] - 深度分析引擎
- [[收费体系|Billing-System]] - F18 方案 B 额度包模型
- [[热榜聚合|Trending-Topics]] - F21 多平台热榜
- [[报告生成|Report-Generation]] - HTML 报告与格式限制

### 贡献指南
- [[开发规范|Development-Conventions]] - 编码规范与提交流程
- [[测试规范|Testing-Conventions]] - TDD 红绿重构
- [[安全规范|Security-Guidelines]] - 凭据管理与代码审查

## 🔗 外部资源

- **GitHub 仓库**：https://github.com/jetchanxxx-tech/yuqing
- **生产环境**：https://yuqing2.pangu-cloud.com
- **管理后台**：https://yuqing2.pangu-cloud.com/admin
- **问题反馈**：[GitHub Issues](https://github.com/jetchanxxx-tech/yuqing/issues)

## 📊 当前版本

**Beta 0.1.0** (2026-09-22)

已实现功能：
- ✅ F01-F10 核心监测功能
- ✅ F17 五维研判 + 情感分析 + 报告生成
- ✅ F18 收费体系（额度包 + 三支付渠道）
- ✅ F21 热榜聚合（微博/B站/知乎）
- ✅ PostgreSQL 持久化
- ✅ 生产环境全栈部署

计划中功能：
- 📋 F19 手机号注册/登录
- 📋 F20 企业实名认证
- 📋 F22 成本计算器

## 💡 技术栈

| 层次 | 技术选型 |
|------|---------|
| 平台层 | Go 1.25 + Gin + pgx v5 + goose |
| 引擎层 | Python 3.11 + FastAPI + Scrapling |
| 前端 | React 19 + TypeScript + Vite 8 + Ant Design 5 |
| 数据库 | PostgreSQL 15 + Redis 7 |
| 部署 | systemd + nginx (无容器) |

## 📝 文档更新

本 Wiki 与代码仓库同步更新。如发现文档过期或错误，欢迎：
1. 提交 [Issue](https://github.com/jetchanxxx-tech/yuqing/issues)
2. 直接提交 Pull Request 修正

---

最后更新：2026-09-22 | 版本：Beta 0.1.0
