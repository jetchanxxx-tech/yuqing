import client from './client';
import { readEnvelope } from './error';
import type { CreditsInfo, Order } from './billing';

export interface AdminPage<T> { items: T[]; total: number; page: number; page_size: number; }
export interface AdminQuery {
  page: number; page_size: number; q?: string; status?: string;
  platform_role?: string; verified?: string; plan_code?: string;
}
export interface AdminUser {
  id: string; name: string; email: string; phone_masked: string; status: string;
  email_verified: boolean; phone_verified: boolean; platform_roles: string[];
  tenant_count: number; created_at: string; last_login_at: string | null; row_version: number;
}
export interface Membership {
  tenant_id: string; tenant_name: string; tenant_status: string; role: string; row_version: number;
}
export interface AuditEntry {
  id: number; actor_id: string; action: string; target_type: string; target_id: string;
  tenant_id: string; reason: string; before: Record<string, unknown>; after: Record<string, unknown>;
  request_id: string; created_at: string;
}
export interface AdminUserDetail extends AdminUser { memberships: Membership[]; audit_logs: AuditEntry[]; }
export interface Tenant {
  id: string; name: string; slug: string; plan_code: string; status: string; created_at: string;
  row_version: number; user_count: number; effective_plan_code: string; plan_source: string;
}
export interface TenantMember { user_id: string; name: string; email: string; role: string; row_version: number; }
export interface AdminTenantDetail extends Tenant { members: TenantMember[]; credit: CreditsInfo | null; orders: Order[]; audit_logs: AuditEntry[]; }
export interface AdminChange { reason: string; expected_version: number; }
export type MemberRole = 'tenant_admin' | 'analyst' | 'viewer';

export async function listUsers(query: AdminQuery, signal?: AbortSignal): Promise<AdminPage<AdminUser>> {
  return (await client.get('/admin/users', { params: query, signal })).data;
}
export async function getAdminUser(id: string, signal?: AbortSignal): Promise<AdminUserDetail> {
  return (await client.get(`/admin/users/${encodeURIComponent(id)}`, { signal })).data;
}
export async function listTenants(query: AdminQuery, signal?: AbortSignal): Promise<AdminPage<Tenant>> {
  return (await client.get('/admin/tenants', { params: query, signal })).data;
}
export async function getAdminTenant(id: string, signal?: AbortSignal): Promise<AdminTenantDetail> {
  return (await client.get(`/admin/tenants/${encodeURIComponent(id)}`, { signal })).data;
}
export async function changeUserStatus(id: string, action: 'disable' | 'enable', change: AdminChange): Promise<void> {
  await client.post(`/admin/users/${encodeURIComponent(id)}/${action}`, change);
}
export async function changePlatformRole(id: string, platformAdmin: boolean, change: AdminChange): Promise<void> {
  await client.put(`/admin/users/${encodeURIComponent(id)}/platform-role`, { ...change, platform_admin: platformAdmin });
}
export async function changeTenantStatus(id: string, action: 'suspend' | 'resume', change: AdminChange): Promise<void> {
  await client.post(`/admin/tenants/${encodeURIComponent(id)}/${action}`, change);
}
export async function changeMemberRole(tenantID: string, userID: string, role: MemberRole, change: AdminChange): Promise<void> {
  await client.put(`/admin/tenants/${encodeURIComponent(tenantID)}/members/${encodeURIComponent(userID)}/role`, { ...change, role });
}
export function adminErrorStatus(error: unknown): number {
  return (error as { response?: { status?: number } } | null)?.response?.status ?? 0;
}
export function adminErrorMessage(error: unknown): string {
  const reason = readEnvelope(error)?.message;
  switch (adminErrorStatus(error)) {
    case 403: return `无操作权限：${reason ?? '当前账号不是平台管理员，请重新确认登录状态。'}`;
    case 409: return `操作冲突：数据版本或状态已更新，或违反最后管理员保护。${reason ?? ''} 请刷新最新数据后重新确认。`;
    case 401: return '登录凭据已失效，请重新登录。';
    default: return reason ?? '服务暂时不可用，请稍后重试。';
  }
}
export function adminStatusLabel(status: string): string {
  return ({ active: '正常', disabled: '已禁用', suspended: '已挂起', provisioning: '准备中', pending_activation: '待激活', closure_pending: '注销处理中', closed: '已关闭' } as Record<string, string>)[status] ?? status;
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
