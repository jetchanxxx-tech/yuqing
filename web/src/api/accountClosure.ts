import client from './client';

export interface ClosureTenant {
  id: string; name: string; members: number; role: string; status: string;
  plan_code: string; retention_days: number | null;
}
export interface ClosurePreview { withdrawal_days: number; tenants: ClosureTenant[]; blockers: string[] }
export interface ClosureStatus {
  id: string; state: 'pending' | 'cancelled' | 'finalizing' | 'completed';
  requested_at: string; withdraw_until: string; completed_at: string | null;
  cleanup_status: string; error_code?: string;
}
export async function previewClosure() { return (await client.get<ClosurePreview>('/user/account-closure/preview')).data; }
export async function requestClosure(password: string, tenants: string[]) {
  return (await client.post<ClosureStatus>('/user/account-closure', { password, confirmed: true, close_tenant_ids: tenants })).data;
}
export async function closureStatus() { return (await client.get<ClosureStatus>('/user/account-closure/status')).data; }
export async function cancelClosure(password: string) {
  return (await client.post<ClosureStatus>('/user/account-closure/cancel', { password })).data;
}
