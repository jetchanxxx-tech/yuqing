-- +goose Up
-- 收费体系（方案 B）：额度池 + 流水 + 订单。beta 期额度不过期（公测政策），
-- 正式商用再加批次有效期与订阅周期归零。

-- ── 报告额度池（每租户一行）──────────────────────────────────────
CREATE TABLE IF NOT EXISTS report_credits (
    tenant_id   TEXT PRIMARY KEY,
    balance     INTEGER NOT NULL DEFAULT 0 CHECK (balance >= 0),
    plan_code   TEXT NOT NULL DEFAULT 'free',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ── 额度流水：delta 正=入账（trial/grant/purchase/refund），负=消费 ──
CREATE TABLE IF NOT EXISTS credit_transactions (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    delta          INTEGER NOT NULL,
    reason         TEXT NOT NULL, -- trial|grant|purchase|consume|refund|admin_adjust
    analysis_id    TEXT,
    order_id       TEXT,
    consume_tx_id  TEXT,          -- refund 回指被退的那笔 consume（一扣至多一退）
    balance_after  INTEGER NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_credit_tx_tenant_time ON credit_transactions(tenant_id, created_at DESC);
-- 支付入账幂等：同一订单至多入账一次（回调重放/查单补偿并发时唯一约束兜底）
CREATE UNIQUE INDEX IF NOT EXISTS uniq_credit_tx_order_purchase ON credit_transactions(order_id) WHERE reason = 'purchase';
-- 回补幂等：一笔消费至多退一次（Rerun 的新一轮消费是新的 consume 行）
CREATE UNIQUE INDEX IF NOT EXISTS uniq_credit_tx_refund ON credit_transactions(consume_tx_id) WHERE reason = 'refund';

-- ── 订单 ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS orders (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL,
    sku_code         TEXT NOT NULL,
    kind             TEXT NOT NULL,          -- plan|addon
    credits          INTEGER NOT NULL,
    amount_cents     INTEGER NOT NULL,
    channel          TEXT NOT NULL,          -- alipay|wechat|unionpay
    state            TEXT NOT NULL DEFAULT 'pending', -- pending|paid|closed|refund_needed
    provider_txn_id  TEXT,
    qr_code_url      TEXT,
    txn_time         TEXT NOT NULL DEFAULT '',  -- 银联查单必需（下单时间 yyMMddHHmmss）
    granted          BOOLEAN NOT NULL DEFAULT FALSE,
    paid_at          TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_orders_tenant ON orders(tenant_id, created_at DESC);
-- 渠道流水号全局唯一：防「一个渠道支付成功凭据给多个订单入账」的刷单
CREATE UNIQUE INDEX IF NOT EXISTS uniq_orders_provider_txn ON orders(provider_txn_id) WHERE provider_txn_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS credit_transactions;
DROP TABLE IF EXISTS report_credits;
