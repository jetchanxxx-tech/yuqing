# 产品决策会议议题

**会议时间**：待定  
**会议目标**：解决 P1 问题中的 4 个产品决策点  
**参会人员**：产品经理、技术负责人  

---

## 议题 1：权限边界定义（P1-1）

### 问题描述
当前系统存在权限中间件缺失问题：
- **已有权限控制**：`POST /analyses`（创建）、`GET /analyses`（列表）、`GET /analyses/:id/events`（SSE）
- **缺失权限控制**：
  - `GET /analyses/:id`（详情）
  - `POST /analyses/:id/cancel`（取消）
  - `POST /analyses/:id/rerun`（重跑）
  - `GET /analyses/:id/result`（结果）
  - `GET /reports/:id`（报告详情）
  - `GET /reports/:id/download`（报告下载）

### 现有角色体系
根据 `platform/internal/platform/auth/auth.go`，当前定义了 4 个角色：

| 角色 | 现有权限 |
|------|---------|
| **owner** | `analyses:create`, `analyses:list`, `analyses:delete`, `reports:read`, `reports:download`, `reports:delete`, `admin:settings` |
| **admin** | `analyses:create`, `analyses:list`, `reports:read`, `reports:download` |
| **analyst** | `analyses:create`, `analyses:list`, `reports:read` |
| **viewer** | `analyses:list`, `reports:read` |

### 决策建议

#### **方案 A：最小权限原则（推荐）**

基于 **toB SaaS 协作场景** 和 **成本控制** 两个核心考量：

| 操作 | Owner | Admin | Analyst | Viewer | 理由 |
|------|-------|-------|---------|--------|------|
| **查看分析详情** | ✅ | ✅ | ✅ | ✅ | 只读操作，所有角色可见 |
| **查看分析结果** | ✅ | ✅ | ✅ | ✅ | 只读操作，结果查看不消耗额度 |
| **取消分析** | ✅ | ✅ | ✅ | ❌ | 创建者可撤销，Viewer 纯观察者 |
| **重跑分析** | ✅ | ✅ | ❌ | ❌ | **消耗额度**，限 Owner/Admin |
| **查看报告详情** | ✅ | ✅ | ✅ | ✅ | 只读操作（已有 `reports:read`） |
| **下载报告** | ✅ | ✅ | ✅ | ❌ | Viewer 可查看但不可导出（防数据外泄） |

**核心逻辑**：
1. **只读操作全开放**：详情/结果查看不消耗资源，协作场景需要全员可见
2. **成本控制**：重跑消耗额度（69-999 元/次），仅管理层可操作
3. **数据保护**：Viewer 可在线查看但不可下载导出（防竞业/离职员工带走数据）

#### **方案 B：严格层级控制**

| 操作 | Owner | Admin | Analyst | Viewer | 理由 |
|------|-------|-------|---------|--------|------|
| 查看分析详情 | ✅ | ✅ | ✅ | ❌ | Viewer 只能看列表 |
| 查看分析结果 | ✅ | ✅ | ✅ | ❌ | 同上 |
| 取消分析 | ✅ | ✅ | ❌ | ❌ | 仅管理层可撤销 |
| 重跑分析 | ✅ | ❌ | ❌ | ❌ | 仅 Owner 控制成本 |
| 查看报告详情 | ✅ | ✅ | ✅ | ❌ | Viewer 无报告权限 |
| 下载报告 | ✅ | ✅ | ❌ | ❌ | 仅管理层可导出 |

**问题**：过度限制导致协作效率低（Analyst 无法独立完成分析闭环）

---

### 实施计划（方案 A）

**需要新增的权限标识符**：
```go
// auth/auth.go rolePermissions 补充
"owner": {
    // 新增
    "analyses:read",    // GET /analyses/:id + /analyses/:id/result
    "analyses:cancel",  // POST /analyses/:id/cancel
    "analyses:rerun",   // POST /analyses/:id/rerun
},
"admin": {
    // 新增
    "analyses:read",
    "analyses:cancel",
    "analyses:rerun",
},
"analyst": {
    // 新增
    "analyses:read",
    "analyses:cancel",
    // reports:download 已有
},
"viewer": {
    // 新增
    "analyses:read",
},
```

**路由加中间件**（`api/v1/analyses.go`）：
```go
analyses.GET("/:id", middleware.RequirePermission("analyses:read"), svcs.handleGetAnalysis)
analyses.GET("/:id/result", middleware.RequirePermission("analyses:read"), svcs.handleGetAnalysisResult)
analyses.POST("/:id/cancel", middleware.RequirePermission("analyses:cancel"), svcs.handleCancelAnalysis)
analyses.POST("/:id/rerun", middleware.RequirePermission("analyses:rerun"), svcs.handleRerunAnalysis)
```

**工作量**：2-3 小时

---

## 议题 2：套餐数据源统一（P1-2）

### 问题描述
当前存在两个套餐标记字段：
1. **`tenants.plan_code`**：注册时写入 `"free"`，购买后**不更新**
2. **`report_credits.plan_code`**：购买时写入实际套餐（`lite`/`pro`/`enterprise`）

**影响范围**：
- 分析模式判断（quick/full）依赖套餐
- 报告格式 gating（HTML/PDF/DOCX）依赖套餐
- JWT token 携带 `plan_code`（从 `tenants` 表读取）

**当前真相来源**：`report_credits.plan_code`（支付流程写入）

### 决策建议

#### **方案 A：全部改读 `report_credits.plan_code`（推荐）**

**优点**：
- ✅ **单一真相来源**：支付流程唯一写入点
- ✅ **逻辑清晰**：套餐信息属于"计费域"，与租户基础信息分离
- ✅ **改动最小**：只需修改读取逻辑

**缺点**：
- ⚠️ JWT token 不含最新套餐（需刷新 token 或改为动态查询）

**实施**：
```go
// app/container.go 的 planCodeFor 闭包改为：
planCodeFor := func(tenantID string) string {
    code, _ := creditSvc.PlanCode(ctx, tenantID)  // 从 report_credits 读
    if code == "" {
        return "free"  // 未购买默认 free
    }
    return code
}
```

**工作量**：1-2 小时

---

#### **方案 B：支付时同步更新两表**

**优点**：
- ✅ JWT token 实时包含套餐信息（无需刷新）
- ✅ 保持向后兼容（现有读 `tenants.plan_code` 的代码无需改）

**缺点**：
- 🔴 **双写一致性风险**：两表更新非事务（PG 跨表事务可解决，但增加复杂度）
- 🔴 **职责不清**：租户表混入计费信息

**实施**：
```go
// payment/service.go 的 processPaymentSucceeded 补充：
if sku.Kind == "plan" {
    if err := s.tenants.UpdatePlan(ctx, o.TenantID, o.SKUCode); err != nil {
        // 需要回滚 credits 表？
    }
}
```

**工作量**：3-4 小时

---

#### **方案 C：废弃 `tenants.plan_code`**

**优点**：
- ✅ 彻底消除歧义
- ✅ 符合 DDD 分层（租户 ≠ 计费）

**缺点**：
- 🔴 **迁移成本高**：需修改所有读该字段的代码（JWT/注册/查询）
- 🔴 **历史数据处理**：已有租户的 `plan_code` 列怎么办？

**工作量**：1-2 天

---

### 推荐方案：**A（单一真相来源）**

**理由**：
1. 符合当前架构（支付流程已是唯一写入点）
2. 改动最小，风险最低
3. JWT token 套餐信息"滞后"可接受（刷新 token 或页面刷新后更新）

**附加优化**：
- 前端在支付成功后主动调用 `/auth/refresh` 刷新 token
- 或者套餐敏感操作（创建分析/下载报告）改为实时查询而非依赖 JWT

---

## 议题 3：报告下载接口形式（P1-3）

### 问题描述
前端期待返回 `{download_url: "..."}` 并二次请求，后端当前直接返回文件流。

**当前实现**（`api/v1/reports.go`）：
- HTML：从 `analyses.report_content` 直接返回
- DOCX：代理 Python `report_engine` 的流式响应

### 决策建议

#### **方案 A：改前端，直接消费文件流（推荐）**

**技术实现**：
```typescript
// web/src/api/reports.ts
export const downloadReport = async (reportId: string, format: 'html' | 'docx') => {
  const response = await axios.get(`/api/v1/reports/${reportId}/download`, {
    params: { format },
    responseType: 'blob',  // 关键：直接处理二进制流
  });
  
  const blob = new Blob([response.data], {
    type: format === 'html' ? 'text/html' : 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  });
  const url = window.URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `report_${reportId}.${format}`;
  a.click();
  window.URL.revokeObjectURL(url);
};
```

**优点**：
- ✅ **性能最优**：无中间环节，流式传输
- ✅ **后端不改**：当前实现符合 HTTP 语义（Content-Disposition: attachment）
- ✅ **安全性好**：无临时 URL 泄露风险

**缺点**：
- ⚠️ 前端需改动（但标准做法）

**工作量**：1 小时

---

#### **方案 B：改后端，返回 URL 二次请求**

**技术实现**：
```go
// api/v1/reports.go handleDownloadReport 改为：
func (s *Services) handleDownloadReport(c *gin.Context) {
    // ... 权限检查 ...
    
    // 生成临时下载 URL（带签名，5 分钟有效）
    token := generateSignedToken(reportID, format, 5*time.Minute)
    url := fmt.Sprintf("/api/v1/reports/%s/stream?format=%s&token=%s", reportID, format, token)
    
    c.JSON(http.StatusOK, gin.H{"download_url": url})
}

// 新增 /reports/:id/stream 端点，验证 token 后返回文件流
```

**优点**：
- ✅ 前端无需改动（符合期待的 API 形式）
- ✅ 可加入下载统计（两次请求中间插桩）

**缺点**：
- 🔴 **复杂度增加**：需实现 token 签名/验证逻辑
- 🔴 **性能损失**：多一次 HTTP 往返
- 🔴 **安全风险**：URL 泄露后 5 分钟内可被滥用

**工作量**：4-6 小时

---

#### **方案 C：混合模式**

HTML 返回 URL（轻量级，可内嵌预览），DOCX 直接流（大文件）。

**问题**：API 不一致，前端需双重处理逻辑。

---

### 推荐方案：**A（前端改为流式下载）**

**理由**：
1. 符合 HTTP 最佳实践（Content-Disposition: attachment）
2. 性能最优，安全性最好
3. 后端无需改动（已实现正确）

**前端改动清单**：
- `web/src/api/reports.ts`：`downloadReport` 函数改为 `responseType: 'blob'`
- `web/src/pages/ReportCenterPage.tsx`：下载按钮调用改后的 API

---

## 议题 4：Pro 套餐 DOCX 权限（P1-4）

### 问题描述
当前代码（`billing/billing.go:85`）：
```go
"pro": {
    // ...
    EnabledFeatures: map[string]bool{
        "reports:docx": false,  // ← Pro 不含 DOCX
    },
},
```

**定价对比**：
- **Pro（999 元/月）**：10 次分析，PDF 可下载
- **Enterprise（4999 元/月）**：50 次分析，DOCX 可下载

**行业对标**：
- 石墨文档：高级版含 DOCX 导出
- 飞书文档：专业版含 DOCX 导出
- Notion：Plus 计划（$10/月）含导出

### 决策建议

#### **方案 A：Pro 包含 DOCX（推荐）**

**定价逻辑重构**：
```
Lite (99/月)   → HTML only（在线查看）
Pro (999/月)   → HTML + PDF + DOCX（全格式导出）
Enterprise (4999/月) → 全格式 + API + 优先队列 + 自定义模型
```

**差异化策略**：
| 维度 | Lite | Pro | Enterprise |
|------|------|-----|------------|
| 报告次数 | 4 次 | 10 次 | 50 次 |
| 分析模式 | quick（3 维） | full（5 维） | full |
| 导出格式 | HTML | HTML/PDF/DOCX | 全格式 |
| API 访问 | ❌ | 只读 | 读写 |
| 优先队列 | ❌ | ❌ | ✅ |
| 自定义模型 | ❌ | ❌ | ✅ |

**改动**：
```go
// billing/billing.go:85
"pro": {
    // ...
    EnabledFeatures: map[string]bool{
        "reports:html": true,
        "reports:markdown": true,
        "reports:pdf": true,
        "reports:docx": true,  // ← 改为 true
        // ...
    },
},
```

**优点**：
- ✅ 提升 Pro 性价比（999 元/月包含完整导出能力）
- ✅ 简化对外宣传（"研判版=完整报告能力"）
- ✅ 降低 Enterprise 门槛压力（5 倍价格需体现在"企业级能力"而非基础功能）

**缺点**：
- ⚠️ 削弱 Enterprise 差异化（需强化 API/优先队列/自定义模型的价值）

**工作量**：5 分钟（改一行代码）

---

#### **方案 B：Pro 不含 DOCX，强化 PDF 能力**

保持现状，但增强 PDF 的专业性：
- PDF 含完整交互（超链接/书签/目录）
- PDF 支持加水印（企业 logo）
- PDF 导出速度优化（< 3 秒）

**问题**：
- 🔴 PDF 与 DOCX 用户认知差异小（都是"可下载的完整报告"）
- 🔴 Pro 定价 999 元/月，不含 DOCX 可能被用户质疑

---

#### **方案 C：新增"专业版 Plus"（1999 元/月）**

```
Pro (999/月)       → 10 次 + PDF
Pro Plus (1999/月) → 20 次 + DOCX + API 只读
Enterprise (4999/月) → 50 次 + API 读写 + 优先队列
```

**问题**：
- 🔴 SKU 过多，用户决策成本高
- 🔴 与"渗透型定价"战略冲突（加购 69/次 vs Plus 1999）

---

### 推荐方案：**A（Pro 包含 DOCX）**

**理由**：
1. **用户期待**：999 元/月的 SaaS 应包含完整导出能力
2. **竞品对标**：主流协作工具的中档套餐都含 DOCX
3. **差异化重构**：Enterprise 的价值在"企业级能力"（API/队列/定制），而非"多一个导出格式"

**后续优化方向（v0.2.0+）**：
- Enterprise 专属：报告模板定制、品牌水印、批量导出
- Enterprise 专属：Webhook 通知、SSO 集成、审计日志

---

## 会议决策总结表

| 议题 | 推荐方案 | 工作量 | 优先级 |
|------|---------|--------|--------|
| P1-1 权限边界 | 方案 A（最小权限原则） | 2-3 小时 | P0 |
| P1-2 套餐数据源 | 方案 A（统一读 report_credits） | 1-2 小时 | P0 |
| P1-3 下载接口 | 方案 A（前端改流式下载） | 1 小时 | P1 |
| P1-4 DOCX 权限 | 方案 A（Pro 包含 DOCX） | 5 分钟 | P0 |

**总工作量**：4-6 小时  
**建议发布**：v0.1.3-hotfix（本周内）

---

## 附录：客户需求与 P1 修复的关系

当前会议聚焦 **P1 技术债修复**，与客户三大需求（指数可视化/竞品对比/营销方案）**并行不冲突**：

| 客户需求 | 依赖 P1 修复？ | 备注 |
|---------|--------------|------|
| 需求 3：指数可视化 | ❌ 不依赖 | 可并行开发 |
| 需求 2：竞品对比 | ⚠️ 弱依赖 P1-1 | 项目管理需明确权限模型 |
| 需求 1：营销方案 | ❌ 不依赖 | 长期规划（v0.2.0） |

**建议路线图**：
```
Week 1: P1 修复（v0.1.3-hotfix）
Week 2-3: 需求 3 指数可视化（v0.1.3）
Week 4-6: 需求 2 竞品对比（v0.1.4）
Week 7+: 需求 1 营销方案 MVP（v0.2.0）
```
