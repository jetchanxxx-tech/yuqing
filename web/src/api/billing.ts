import client from './client';

/** GET /billing/plans 的元素（方案 B 套餐目录） */
export interface Plan {
  code: string;
  name: string;
  /** 分/月；0 表示免费档（不可购买） */
  price_monthly_cny: number;
  /** 套餐含报告额度（次）；0 = 不发售 */
  credits_per_cycle: number;
  /** quick = 3 维速览 / full = 5 维完整研判 */
  analysis_mode: string;
  priority_queue?: boolean;
  /** token 配额，单位：百万（0/负值表示无上限） */
  token_quota_m: number;
}

/** 加购 SKU（GET /billing/plans 的 addons） */
export interface SKU {
  code: string;
  name: string;
  kind: 'plan' | 'addon';
  price_cents: number;
  credits: number;
}

/** GET /billing/plans 响应 */
export interface PlanCatalog {
  plans: Plan[];
  addons: SKU[];
  /** 当前已配置且启用的支付渠道（alipay/wechat/unionpay） */
  channels: string[];
}

/** GET /billing/credits */
export interface CreditsInfo {
  balance: number;
  plan_code: string;
}

/** 额度流水 */
export interface CreditTransaction {
  id: string;
  delta: number;
  reason: 'trial' | 'grant' | 'purchase' | 'consume' | 'refund';
  analysis_id?: string;
  order_id?: string;
  balance_after: number;
  created_at: string;
}

/** 支付订单 */
export interface Order {
  id: string;
  sku_code: string;
  kind: 'plan' | 'addon';
  credits: number;
  amount_cents: number;
  channel: string;
  state: 'pending' | 'paid' | 'closed' | 'refund_needed';
  qr_code_url?: string;
  expires_at: string;
  created_at: string;
  granted: boolean;
  paid_at?: string;
}

export interface BillingUsage {
  tokens_used: number;
  tokens_quota: number;
  analyses_used: number;
  analyses_quota: number;
}

export interface Invoice {
  id: string;
  plan_code?: string;
  period?: string;
  amount_cny?: number;
  status?: string;
  created_at?: string;
}

export interface InvoicesResponse {
  invoices: Invoice[];
  total: number;
}

export async function getCatalog(): Promise<PlanCatalog> {
  const { data } = await client.get<PlanCatalog>('/billing/plans');
  return {
    plans: data.plans ?? [],
    addons: data.addons ?? [],
    channels: data.channels ?? [],
  };
}

/** 兼容旧调用方（仅套餐列表） */
export async function getPlans(): Promise<Plan[]> {
  return (await getCatalog()).plans;
}

export async function getCredits(): Promise<CreditsInfo> {
  const { data } = await client.get<CreditsInfo>('/billing/credits');
  return data;
}

export async function getCreditTransactions(): Promise<CreditTransaction[]> {
  const { data } = await client.get<{ transactions: CreditTransaction[] }>('/billing/transactions');
  return data.transactions ?? [];
}

export async function createOrder(skuCode: string, channel: string): Promise<Order> {
  const { data } = await client.post<{ order: Order }>('/billing/orders', {
    sku_code: skuCode,
    channel,
  });
  return data.order;
}

/** 订单详情；后端在 pending 时顺带向渠道查单对账（回调丢失自愈） */
export async function getOrder(orderId: string): Promise<Order> {
  const { data } = await client.get<{ order: Order }>(`/billing/orders/${orderId}`);
  return data.order;
}

export async function listOrders(): Promise<Order[]> {
  const { data } = await client.get<{ orders: Order[] }>('/billing/orders');
  return data.orders ?? [];
}

export async function getUsage(): Promise<BillingUsage> {
  const { data } = await client.get<BillingUsage>('/billing/usage');
  return data;
}

export async function getInvoices(): Promise<Invoice[]> {
  const { data } = await client.get<InvoicesResponse>('/billing/invoices');
  return data.invoices ?? data;
}

/** 订阅/更换套餐（占位端点，付费走 createOrder） */
export async function subscribePlan(planCode: string): Promise<void> {
  await client.post('/billing/subscribe', { plan_code: planCode });
}
