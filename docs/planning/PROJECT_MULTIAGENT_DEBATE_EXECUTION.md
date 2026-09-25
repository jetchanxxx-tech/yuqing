# 多 Agent 辩论功能开发项目·CEO 决策与执行计划

**决策日期**：2026-09-25  
**项目代号**：MultiAgent-Debate-MVP  
**目标交付**：2 周内上线生产环境  
**项目优先级**：P0（最高）

---

## 📋 CEO 最终决策

### 1. 技术方案
✅ **批准方案 A：全力推进**
- 5 专家 × 3 轮完整辩论
- 2 周交付（10 个工作日）
- 真实 LLM 调用（GLM-4-flash）
- **严禁 Mock 数据**

### 2. 推广策略
✅ **方案 C：分层宣传**
- **官网**：主打"深度研判模式"（降低认知门槛）
- **产品内**：展示"5 位 AI 专家辩论"（让用户发现价值）
- **案例演示**：重点展示"雅阁后排"辩论过程

### 3. 套餐分层
✅ **后期调整**（现阶段不纠结细节）
- Free/Lite/Pro/Enterprise 的辩论功能分配待验证后优化

### 4. 核心要求
🔴 **CEO 特别强调**：
> "所有 Agent 的分析研判、测试、代码、数据存储过程，全部按照生产环境的要求来做，不要再出现虚假数据、Mock 数据来'展示'跳过功能的问题。"

**这意味着**：
- ❌ 禁止硬编码返回
- ❌ 禁止假装调用 LLM 但实际返回预置数据
- ✅ 必须真实调用 GLM-4-flash API
- ✅ 数据库必须真实存储辩论记录（新表 `debate_records`）
- ✅ 日志必须完整记录 LLM 调用链路（token 消耗、耗时、错误）
- ✅ 最终由运维经理部署和提交代码

---

## 🎯 战略决策背景

### 三位总监一致推荐

| 总监 | 核心观点 | 推荐方案 |
|------|---------|---------|
| **技术总监** | 成本极低（¥0.021/次）、2周可交付、差异化强 | ✅ All-in 推进 |
| **产品总监** | 用户价值高、差异化明显、符合toB信任逻辑 | ✅ 大力宣传 |
| **运营总监** | 认知门槛高、需A/B验证，但成本数据改变游戏规则 | ⚠️ 谨慎验证 |

### 关键数据支撑

| 指标 | 数据 | 状态 |
|------|------|------|
| **单次成本** | ¥0.021（2分钱） | ✅ 可忽略 |
| **Pro 月成本** | ¥0.21（10次） | ✅ 可忽略 |
| **毛利率** | 99.98% | ✅ 极高 |
| **开发周期** | 2 周（10天） | ✅ 可控 |
| **差异化** | 独家功能 | ✅ 强 |
| **核心风险** | Day 1-2 Prompt 验证 | ⚠️ 可管理 |

---

## 📅 项目时间表

### Week 1：核心开发

| Day | 技术开发 | 产品/运营 | 里程碑 |
|-----|---------|----------|--------|
| **1-2** | Prompt 工程验证（17个模板） | 官网文案更新 | 🔴 **生死线**：观点差异化验证 |
| **3-4** | LLM 并发调用 + 超时保护 | UI 设计稿输出 | - |
| **5-6** | Orchestrator 协调器开发 | 种子用户招募启动 | 🟡 完整辩论跑通 |
| **7** | 降级方案 + 数据库迁移 | A/B 测试埋点对接 | - |

### Week 2：测试部署

| Day | 技术开发 | 产品/运营 | 里程碑 |
|-----|---------|----------|--------|
| **8** | 前端辩论展示组件 | 案例库准备（5个行业） | - |
| **9** | 集成测试（10个真实舆情） | 种子用户激活 | - |
| **10** | 性能优化 + 交付运维 | 监控数据看板 | 🟢 **上线生产** |

### Week 3-4：市场验证

| Week | 技术 | 运营 | 里程碑 |
|------|------|------|--------|
| **Week 3** | Bug 修复 + 性能监控 | A/B 测试执行（100人） | - |
| **Week 4** | 成本数据复核 | 数据复盘 + 反馈收集 | 🎯 决定推广力度 |

---

## 👥 资源分配

### 核心团队

| 角色 | 职责 | 投入 |
|------|------|------|
| **技术总监** | 架构设计、技术规划、风险把控 | 全程参与 |
| **后端工程师** | ForumEngine 开发（Orchestrator + Agent） | 全职 10 天 |
| **前端工程师** | 辩论展示组件（3层信息架构） | 1-2 天 |
| **产品总监** | UI/UX 设计、官网文案、案例演示 | 兼职 |
| **运营总监** | 种子用户招募、A/B 测试、数据分析 | 兼职 |
| **运维经理** | 部署、监控、日志分析 | Day 10 介入 |

### 待确认

技术总监需明确：
- [ ] 后端工程师是谁？可用性？
- [ ] 前端工程师是谁？可用性？
- [ ] 是否需要架构师评审？
- [ ] LLM API Key（GLM-4-flash）已配置？

---

## 🏗️ 技术架构要求

### 核心组件

```
┌──────────────────────────────────────────┐
│  Go Platform (port 8080)                 │
│    POST /analyses → Pipeline             │
│         ↓ HTTP 调用                       │
└─────────┬────────────────────────────────┘
          │
┌─────────▼────────────────────────────────┐
│  ForumEngine (Python, port 8004)         │
│    POST /debate                           │
│      ↓                                    │
│    DebateOrchestrator.run_debate()       │
│      ├─ Round 1: 主持人 + 4 专家 (并发)  │
│      ├─ Round 2: 交叉质询 (并发)         │
│      └─ Round 3: 共识形成 (串行)         │
│                                           │
│    存储: debate_records 表               │
│    返回: {rounds, verdict, confidence}   │
└──────────────────────────────────────────┘
```

### 数据库表设计（新增）

```sql
CREATE TABLE debate_records (
    id UUID PRIMARY KEY,
    analysis_id UUID NOT NULL REFERENCES analyses(id),
    tenant_id UUID NOT NULL,
    topic TEXT NOT NULL,
    
    -- 辩论内容（JSON）
    rounds JSONB NOT NULL,  -- [{round, agent, statement, evidence}]
    verdict TEXT NOT NULL,
    confidence FLOAT,
    
    -- 质量指标
    similarity_score FLOAT,  -- 观点相似度（检测雷同）
    total_tokens INT,
    llm_cost_cny DECIMAL(10,4),
    duration_ms INT,
    
    -- LLM 调用记录
    llm_calls JSONB,  -- [{agent, prompt_tokens, completion_tokens, model}]
    
    -- 状态
    status TEXT DEFAULT 'completed',  -- completed/failed/timeout
    error_message TEXT,
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_debate_records_analysis ON debate_records(analysis_id);
CREATE INDEX idx_debate_records_tenant ON debate_records(tenant_id);
CREATE INDEX idx_debate_records_created ON debate_records(created_at DESC);
```

### API 接口规范

**请求**：
```json
POST /debate
{
  "analysis_id": "uuid",
  "topic": "雅阁后排舆情事件",
  "analysis_summary": {
    "documents_count": 324,
    "sentiment": {"positive": 0.22, "negative": 0.78},
    "keywords": ["后排空间", "虚假宣传", "车主维权"],
    "platforms": ["weibo", "xiaohongshu"]
  }
}
```

**响应**：
```json
{
  "debate_id": "uuid",
  "rounds": [
    {
      "round": 1,
      "agent": "事实核查员",
      "statement": "根据微博数据...",
      "evidence": ["微博@XXX", "小红书笔记YYY"],
      "timestamp": "2026-09-25T10:00:01Z"
    },
    // ... 12-15 条记录（主持人3 + 4专家×3）
  ],
  "verdict": "建议48小时内官宣改进+补偿方案",
  "confidence": 0.85,
  "metadata": {
    "similarity_score": 0.42,
    "total_tokens": 14250,
    "cost_cny": 0.021,
    "duration_ms": 38500
  }
}
```

---

## 🛡️ 风险管理

### P0 风险：Prompt 工程质量

**风险**：5 个 Agent 观点雷同（相似度 > 0.7）

**缓解措施**：
1. **Day 1-2 重点投入**：17 个 Prompt 模板迭代
2. **三层差异化**：
   - 人设对立（"证据先行" vs "情绪更重要"）
   - 维度分工（显式约束"只关注 X"）
   - 温度参数（0.2-0.5）
3. **检测机制**：相似度 > 0.7 自动重试
4. **验收标准**：10 个真实舆情测试，相似度平均 < 0.6

**Plan B**：
- 如果 Day 2 验证失败，立即上报 CEO
- 保持 Mock 实现，标注"[演示模式]"
- 等待更成熟的 LLM（如 GPT-5）

### P1 风险：LLM 调用失败

**风险**：API 超时、限流、服务不可用

**缓解措施**：
1. **超时保护**：单次调用 30s 超时
2. **重试机制**：失败后重试 1 次（总计 2 次）
3. **降级方案**：全部失败 → 返回 Mock 数据
4. **用户提示**：UI 显示"[演示模式]"标签

### P2 风险：辩论耗时过长

**风险**：3 轮辩论 > 60s，用户体验差

**缓解措施**：
1. **并发优化**：Round 1/2 并发（4 个 Agent 同时调用）
2. **减少轮次**：如超时频繁，降级为 2 轮
3. **进度提示**：前端显示"专家正在辩论中...30%"

---

## 📊 成功标准

### 技术指标

| 指标 | 目标 | 测量方式 |
|------|------|---------|
| 观点相似度 | < 0.6 | TF-IDF 余弦相似度 |
| 辩论耗时 | < 60s | P95 响应时间 |
| 成功率 | > 95% | 成功次数 / 总次数 |
| 降级率 | < 5% | Mock 返回次数 / 总次数 |

### 业务指标（Week 4 复盘）

| 指标 | 目标 | 备注 |
|------|------|------|
| A/B 测试续费率差异 | A 组 ≥ B 组 +20% | 核心指标 |
| 报告满意度（NPS） | ≥ 40 | 1-10 分评价 |
| 辩论展开率 | > 30% | 用户点击"查看辩论依据" |
| 推荐率 | ≥ 0.3 | 邀请码使用数 / 种子用户数 |

**决策标准**：
- ✅ 如果达标 → 官网大力宣传"5 专家辩论"
- ⚠️ 如果不达标 → 降级为"可选功能"
- ❌ 如果远低于预期 → 暂停推广，转向其他差异化（多模态/竞品对比）

---

## 📝 交付物清单

### 技术总监（今天完成）

- [ ] 完整技术规划文档（`docs/technical/MULTIAGENT_ARCHITECTURE.md`）
- [ ] 开发任务清单（含工时估算）
- [ ] Prompt 模板库（17 个）
- [ ] 数据库迁移脚本（`migrations/XXX_add_debate_records.sql`）
- [ ] API 接口文档
- [ ] 测试与部署计划

### 后端工程师（2 周）

- [ ] `forum_engine/orchestrator.py`（辩论协调器）
- [ ] `forum_engine/agents/`（5 个 Agent 实现）
- [ ] `forum_engine/llm_client.py`（LLM 并发调用）
- [ ] `forum_engine/prompts.py`（17 个 Prompt 模板）
- [ ] 数据库存储逻辑
- [ ] 单元测试（覆盖率 > 80%）
- [ ] 集成测试（10 个真实舆情）

### 前端工程师（1-2 天）

- [ ] 辩论展示组件（React）
  - Level 1: 结论卡片
  - Level 2: 专家观点摘要
  - Level 3: 完整辩论时间线
- [ ] 埋点集成（A/B 测试）

### 产品总监（本周）

- [ ] 官网文案更新（`landing/index.html`）
- [ ] UI 设计稿（辩论展示组件）
- [ ] 案例演示脚本（"雅阁后排"视频）
- [ ] 营销话术（知乎/小红书）

### 运营总监（本周）

- [ ] 种子用户招募计划（邀请话术+渠道执行）
- [ ] A/B 测试执行方案
- [ ] 案例库建设（5 个行业）
- [ ] 数据监控看板需求

### 运维经理（Day 10）

- [ ] 生产环境部署（ForumEngine 新版本）
- [ ] 日志采集配置（LLM 调用链路）
- [ ] 监控告警（辩论失败率/耗时）
- [ ] 代码提交到 GitHub

---

## 🔗 相关文档

- 战略会议纪要：`docs/planning/STRATEGY_MEETING_MULTIAGENT.md`
- 技术方案（技术总监）：`docs/planning/MULTIAGENT_TECHNICAL_PROPOSAL.md`
- 产品方案（产品总监）：见战略会议纪要
- 运营方案（运营总监）：见战略会议纪要
- 官网设计：`landing/index.html`

---

## 📞 联系与上报

**项目负责人**：技术总监  
**汇报对象**：CEO

**每日站会**：每天早上 10:00（15 分钟）
**周报**：每周五晚提交（进度/风险/阻塞）
**紧急上报**：任何阻塞立即钉钉/邮件 CEO

**关键决策点**：
- Day 2：Prompt 验证结果（生死线）
- Day 6：完整辩论跑通
- Week 4：A/B 测试数据复盘

---

**最后更新**：2026-09-25  
**状态**：✅ CEO 已批准，等待技术总监详细规划
