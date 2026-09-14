import client from './client';

export interface Tenant {
  id: string;
  name: string;
  plan_code: string;
  status: string;
  created_at?: string;
  user_count?: number;
}

export async function listTenants(): Promise<Tenant[]> {
  const { data } = await client.get<{ tenants: Tenant[] }>('/admin/tenants');
  return data.tenants ?? data;
}

/** 挂起 / 恢复租户（后端并行开发中，接口以实际联调为准） */
export async function suspendTenant(id: string): Promise<void> {
  await client.post(`/admin/tenants/${id}/suspend`);
}

// ── 平台配置（数据源 API Key 等）─────────────────────────
// 与后端 platform/internal/platform/settings 对应：
//   GET  /api/v1/admin/settings → {settings:{key:value}}
//   PUT  /api/v1/admin/settings ← {key:value}  （合并写入，零重启生效）

export interface AdminSettings {
  bocha_api_key?: string;
  /** LLM 供应商可配置（智谱/DeepSeek/任意 OpenAI 兼容端点） */
  llm_api_key?: string;
  llm_base_url?: string;
  llm_model?: string;
  [key: string]: string | undefined;
}

export async function getAdminSettings(): Promise<AdminSettings> {
  const { data } = await client.get<{ settings: AdminSettings }>('/admin/settings');
  return data.settings ?? {};
}

export async function updateAdminSettings(
  patch: Record<string, string>,
): Promise<AdminSettings> {
  const { data } = await client.put<{ settings: AdminSettings }>('/admin/settings', patch);
  return data.settings ?? {};
}
