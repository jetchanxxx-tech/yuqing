# 收费体系（F18 方案 B）

盘古舆情采用 **额度包模型 + 渗透定价** 策略，降低用户首次付费门槛，通过加购包实现长期价值。

## 💰 套餐定价

| 套餐 | 价格 | 额度 | 分析模式 | 适用场景 |
|------|------|------|----------|----------|
| **体验版** | ¥0 | 1 次试用 | 快速分析 | 注册即赠，体验产品 |
| **速览版** | ¥99/月 | 4 次/月 | 快速分析 | 个人品牌、小微企业 |
| **研判版** | ¥999/月 | 10 次/月 | 深度研判 | 中小企业、PR 团队 |
| **旗舰版** | ¥4999/月 | 50 次/月 | 深度研判 + 优先队列 | 大型企业、危机公关 |

### 加购包

**¥69/次** - 套餐额度用完后按需购买，无时间限制

**渗透定价策略**：
- 首次付费门槛低（99 元 4 次）
- 加购单价高（69 元/次 vs 速览版 24.75 元/次）
- 驱动用户升档：研判版 99.9 元/次更划算

## 🎯 分析模式差异

### 快速分析（quick）

**适用套餐**：体验版、速览版

**特性**：
- 三维研判：热度分析 + 情感分析 + 深层原因
- LLM 思考档位：`low`（压缩成本）
- 平均耗时：1-2 分钟
- 报告格式：仅 HTML

### 深度研判（full）

**适用套餐**：研判版、旗舰版

**特性**：
- 五维研判：背景 + 热度 + 情感 + 分群 + 深层原因
- LLM 思考档位：默认（完整推理）
- 平均耗时：3-5 分钟
- 报告格式：HTML / Markdown / PDF / DOCX（按套餐 gating）

## 💳 支付渠道

### 支持渠道

- ✅ **支付宝**（扫码支付）
- ✅ **微信支付**（扫码支付）
- ✅ **银联**（收银台跳转）

### 支付流程

```
1. 用户选择套餐/加购包
   ↓
2. 前端 POST /api/v1/billing/orders
   {
     "sku_code": "lite",      // 或 "pro" / "enterprise" / "addon_report"
     "channel": "alipay"      // 或 "wechat" / "unionpay"
   }
   ↓
3. 后端返回订单
   {
     "order": {
       "id": "01ARZ3...",
       "amount_cents": 9900,
       "credits": 4,
       "state": "pending",
       "qr_code_url": "https://qr.alipay.com/...",
       "expires_at": "2026-09-22T08:00:00Z"
     }
   }
   ↓
4. 前端展示二维码（支付宝/微信）或跳转收银台（银联）
   ↓
5. 用户扫码支付
   ↓
6. 支付渠道异步回调
   POST /api/v1/callbacks/payment/alipay
   （免鉴权，验签保障安全）
   ↓
7. 后端验签 → 金额核验 → pending→paid 原子跃迁 → 发放额度
   ↓
8. 前端轮询订单状态（GET /api/v1/billing/orders/:id）
   每 3 秒查询一次，轮询即对账（回调丢失时主动查单）
   ↓
9. 订单 state=paid → 跳转成功页 → 余额更新
```

### 三防核验机制

**防止重复发放、金额篡改、伪造回调**：

```go
// 1. 验签（各渠道 SDK）
if !provider.VerifySignature(req) {
    return 400  // 签名不匹配，拒绝
}

// 2. 金额核验
if callbackAmount != order.AmountCents {
    logger.Warn("amount mismatch", "expect", order.AmountCents, "got", callbackAmount)
    return 400  // 金额不符，拒绝
}

// 3. 原子跃迁（provider_txn_id 唯一约束）
tx.Exec(`
    UPDATE orders 
    SET state='paid', provider_txn_id=$1, paid_at=NOW()
    WHERE id=$2 AND state='pending'
`)
// 若已 paid（重复回调），UPDATE 影响行数=0，跳过发放

// 4. 幂等发放
if rowsAffected == 1 {
    creditSvc.Grant(order.TenantID, order.Credits, "purchase", order.ID)
}
```

## 📊 额度管理

### 额度流水类型

| reason | 说明 | delta |
|--------|------|-------|
| `trial` | 新注册试用 | +1 |
| `grant` | 管理员手工发放 | +N |
| `purchase` | 套餐购买/加购 | +N |
| `consume` | 分析任务扣减 | -1 |
| `refund` | 管线失败回补 | +1 |

### 扣减与回补规则

```go
// 1. 创建分析时扣减
POST /api/v1/analyses
  → creditSvc.Consume(tenantID, 1, "consume", analysisID)
  → 余额不足返回 402 NO_CREDITS

// 2. 管线失败/取消自动回补
pipeline.OnFailed(analysisID)
  → creditSvc.Refund(tenantID, 1, "refund", analysisID)

// 3. Rerun 重新扣减
POST /api/v1/analyses/:id/rerun
  → creditSvc.Consume(tenantID, 1, "consume", newAnalysisID)
```

### 防超卖机制

```go
// 乐观锁 + 原子递减
UPDATE credit_balance
SET balance = balance - 1
WHERE tenant_id = $1 AND balance >= 1
RETURNING balance;

// 若 balance < 1，UPDATE 影响行数=0
// 返回 402 NO_CREDITS
```

## 🔧 管理后台配置

### 支付渠道配置

访问 `/admin` → 数据源配置 → 支付渠道

**支付宝配置示例**：
```json
{
  "enabled": true,
  "app_id": "2021001234567890",
  "private_key": "MIIEvQIBADANBgkqhki...",
  "alipay_public_key": "MIIBIjANBgkqhkiG9w...",
  "notify_url": "https://yuqing2.pangu-cloud.com/api/v1/callbacks/payment/alipay"
}
```

**微信支付配置示例**：
```json
{
  "enabled": true,
  "appid": "wx1234567890abcdef",
  "mch_id": "1234567890",
  "mch_serial_no": "ABC123...",
  "private_key": "-----BEGIN PRIVATE KEY-----\n...",
  "api_v3_key": "32-char-key...",
  "notify_url": "https://yuqing2.pangu-cloud.com/api/v1/callbacks/payment/wechat"
}
```

**银联配置示例**：
```json
{
  "enabled": true,
  "mer_id": "012345678901234",
  "sign_cert_pfx": "<base64编码的.pfx证书>",
  "sign_cert_password": "cert-password",
  "notify_url": "https://yuqing2.pangu-cloud.com/api/v1/callbacks/payment/unionpay",
  "front_url": "https://yuqing2.pangu-cloud.com/billing/orders/{order_id}/result"
}
```

**配置即时生效**：
- 零重启生效（从 `platform_settings` 表读取）
- 未启用渠道不出现在 `GET /billing/plans` 的 `channels` 列表
- 前端购买页自动隐藏未配置渠道

### Admin 账号额度策略

**当前策略**（Beta 阶段）：
- Admin 账号仅供商务演示，**非无限额度**
- 额度通过 SQL 手工发放（现有 99 次 + enterprise 档）
- 发放走 `grant` 流水留痕

```sql
-- 手工发放示例
INSERT INTO credit_transactions (id, tenant_id, delta, reason, reference_id, balance_after, created_at)
VALUES ('01ARZ3...', 'admin-tenant-id', 100, 'grant', 'manual-2026-09-22', 199, NOW());

UPDATE credit_balance SET balance = balance + 100 WHERE tenant_id = 'admin-tenant-id';
```

**无无限额度机制**：避免免费滥用，所有账号遵循额度包约束。

## 📈 计费分析（计划中 F22）

### 成本计算器

**目标**：透明化单次分析成本，计算各套餐毛利率

**实现方案**：
1. 引擎响应透传 `usage`（LLM tokens + Bocha calls）
2. `analyses` 表新增列：
   - `llm_input_tokens INT`
   - `llm_output_tokens INT`
   - `bocha_api_calls INT`
   - `analysis_mode TEXT` (quick/full)
3. Admin 后台配置单价：
   - LLM 输入价：¥0.0001/token
   - LLM 输出价：¥0.0006/token
   - Bocha 调用价：¥0.02/次
4. 实测成本统计：
   ```sql
   SELECT 
     analysis_mode,
     AVG(llm_input_tokens * 0.0001 + llm_output_tokens * 0.0006 + bocha_api_calls * 0.02) AS avg_cost
   FROM analyses
   WHERE state='completed'
   GROUP BY analysis_mode;
   ```
5. 各套餐毛利换算：
   - 速览版：¥99 / 4 次 = ¥24.75/次，成本 ~¥3，毛利 87%
   - 研判版：¥999 / 10 次 = ¥99.9/次，成本 ~¥6，毛利 94%

**约 3-4 人天开发量**。历史分析无 `usage` 数据不可回填。

## 🔒 安全注意事项

### 回调端点安全

`POST /api/v1/callbacks/payment/:channel` 是**唯一免鉴权**业务端点。

**安全防线（顺序执行）**：
1. **验签** - 各渠道 SDK 验证签名（支付宝公钥/微信平台证书/银联证书）
2. **金额核验** - 回调金额必须等于订单金额
3. **原子跃迁** - `provider_txn_id` 唯一约束防重复发放
4. **幂等发放** - UPDATE 影响行数=0 时跳过额度发放

**改动此逻辑时必须保持防线顺序**，任何一步失败立即返回 400。

### 凭据管理

- ❌ **绝不**提交商户私钥/证书到代码仓库
- ✅ 私钥/证书仅存 `platform_settings` 表（JSON 字符串）
- ✅ 测试用 mock provider，生产用真实凭据

### 订单过期

订单创建 15 分钟后自动过期（二维码失效），前端轮询遇 `expired` 状态提示重新下单。

## 📊 API 参考

### 查询套餐目录

```bash
GET /api/v1/billing/plans
Authorization: Bearer <token>

# Response
{
  "plans": [
    { "code": "free", "name": "体验版", "price_monthly_cny": 0, "credits_per_cycle": 0, "analysis_mode": "quick" },
    { "code": "lite", "name": "速览版", "price_monthly_cny": 9900, "credits_per_cycle": 4, "analysis_mode": "quick" },
    { "code": "pro", "name": "研判版", "price_monthly_cny": 99900, "credits_per_cycle": 10, "analysis_mode": "full" },
    { "code": "enterprise", "name": "旗舰版", "price_monthly_cny": 499900, "credits_per_cycle": 50, "analysis_mode": "full", "priority_queue": true }
  ],
  "addons": [
    { "code": "addon_report", "name": "报告加购包", "kind": "addon", "price_cents": 6900, "credits": 1 }
  ],
  "channels": ["alipay", "wechat", "unionpay"]
}
```

### 查询额度余额

```bash
GET /api/v1/billing/credits
Authorization: Bearer <token>

# Response
{
  "balance": 7,
  "plan_code": "pro"
}
```

### 创建支付订单

```bash
POST /api/v1/billing/orders
Authorization: Bearer <token>
Content-Type: application/json

{
  "sku_code": "lite",
  "channel": "alipay"
}

# Response 201
{
  "order": {
    "id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    "amount_cents": 9900,
    "credits": 4,
    "state": "pending",
    "qr_code_url": "https://qr.alipay.com/bax08471a1ckc0hyqxxx",
    "channel": "alipay",
    "expires_at": "2026-09-22T08:15:00Z"
  }
}
```

### 轮询订单状态

```bash
GET /api/v1/billing/orders/:id
Authorization: Bearer <token>

# Response（轮询即对账：pending 订单顺带向渠道主动查单）
{
  "order": {
    "id": "01ARZ3...",
    "state": "paid",
    "paid_at": "2026-09-22T08:03:21Z",
    "provider_txn_id": "2024092222001234567890123456"
  }
}
```

### 查询额度流水

```bash
GET /api/v1/billing/transactions
Authorization: Bearer <token>

# Response（新→旧，最多 100 条）
{
  "transactions": [
    {
      "id": "01ARZ3...",
      "delta": -1,
      "reason": "consume",
      "analysis_id": "01ARZ3...",
      "balance_after": 6,
      "created_at": "2026-09-22T07:30:00Z"
    },
    {
      "id": "01ARZ2...",
      "delta": 4,
      "reason": "purchase",
      "reference_id": "order_01ARZ1...",
      "balance_after": 7,
      "created_at": "2026-09-22T07:00:00Z"
    }
  ],
  "total": 12
}
```

## 🧪 测试

### 契约测试

```bash
cd platform
go test ./internal/api/v1/ -run TestBillingContracts
```

覆盖：
- `/billing/plans` 结构锁定
- `/billing/orders` 创建流程
- `/billing/credits` 余额查询
- 402 NO_CREDITS 错误码

### 集成测试

```bash
go test ./test/integration/ -run TestBillingFlow
```

场景：
- 注册赠 1 次试用
- 购买套餐 → 额度增加
- 创建分析 → 扣减 1 次
- 管线失败 → 自动回补
- 余额不足 → 402 拒绝

### 支付渠道测试

**当前状态**：未真实联调（等商户账号）

**Mock 测试**：
```go
// platform/internal/platform/payment/mock.go
type MockProvider struct {
    autoSucceed bool  // true = 自动成功
}

// 模拟回调
func (m *MockProvider) SimulateCallback(orderID string) {
    // 触发 pending → paid 跃迁
}
```

**真实联调清单**（待商户参数）：
- [ ] 支付宝沙箱：小额充值测试（¥0.01）
- [ ] 微信支付沙箱：扫码支付测试
- [ ] 银联测试环境：收银台跳转测试
- [ ] 回调验签：真实签名验证
- [ ] 异常场景：金额篡改、重复回调、过期订单

## 📖 相关文档

- [[套餐选择|Pricing]] - 用户视角的套餐对比
- [[API 参考|API-Reference]] - 完整 REST API 文档
- [[架构设计|Architecture]] - 支付流程架构图
- [[配置参考|Configuration]] - 支付渠道配置详解
