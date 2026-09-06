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
