import client from './client';

/** GET /billing/plans 的元素 */
export interface Plan {
  code: string;
  name: string;
  /** 元/月；0 表示免费档 */
  price_monthly_cny: number;
  /** token 配额，单位：百万（0/负值表示无上限） */
  token_quota_m: number;
}

/** GET /billing/usage */
export interface BillingUsage {
  tokens_used: number;
  tokens_quota: number;
  analyses_used: number;
  analyses_quota: number;
}

/**
 * 账单行（后端字段以实际为准，展示层对缺失字段兜底为 '-'）。
 * 金额以「元」为单位展示；后端若返回分再另行除以 100。
 */
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

export async function getPlans(): Promise<Plan[]> {
  const { data } = await client.get<{ plans: Plan[] }>('/billing/plans');
  return data.plans ?? data;
}

export async function getUsage(): Promise<BillingUsage> {
  const { data } = await client.get<BillingUsage>('/billing/usage');
  return data;
}

export async function getInvoices(): Promise<Invoice[]> {
  const { data } = await client.get<InvoicesResponse>('/billing/invoices');
  return data.invoices ?? data;
}

/** 订阅/更换套餐（后端并行开发中，接口以实际联调为准） */
export async function subscribePlan(planCode: string): Promise<void> {
  await client.post('/billing/subscribe', { plan_code: planCode });
}
