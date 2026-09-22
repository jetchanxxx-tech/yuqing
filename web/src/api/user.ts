import client from './client';

/** 用户资料（GET/PUT /user/profile） */
export interface UserProfile {
  id: string;
  email: string;
  name: string;
  avatar_url: string;
  timezone: string;
  phone: string;
  email_verified: boolean;
  phone_verified: boolean;
  trial_used: number;
  password_changed_at: string;
}

export async function getProfile() {
  const { data } = await client.get<UserProfile>('/user/profile');
  return data;
}

export async function updateProfile(patch: { name?: string; timezone?: string }) {
  const { data } = await client.put<UserProfile>('/user/profile', patch);
  return data;
}

export async function changePassword(oldPassword: string, newPassword: string) {
  await client.put('/auth/password', {
    old_password: oldPassword,
    new_password: newPassword,
  });
}

export async function sendVerificationEmail() {
  const { data } = await client.post<{ message: string }>('/auth/send-verification-email');
  return data.message;
}

/** 公开接口：邮件链接落地验证（无需登录态） */
export async function verifyEmailByToken(token: string) {
  const { data } = await client.get<{ message: string }>('/auth/verify-email', {
    params: { token },
  });
  return data.message;
}

export async function sendPhoneCode(phone: string) {
  const { data } = await client.post<{ message: string; expires_in: number }>(
    '/user/phone/send-code',
    { phone },
  );
  return data;
}

export async function bindPhone(phone: string, code: string) {
  const { data } = await client.post<UserProfile>('/user/phone/bind', { phone, code });
  return data;
}

export async function unbindPhone(password: string) {
  const { data } = await client.post<{ message: string }>('/user/phone/unbind', { password });
  return data.message;
}

/** 邮件/短信服务配置键（admin 后台「通知服务」Tab 用） */
export const NOTIFY_SETTINGS_KEYS = [
  'email_provider',
  'resend_api_key',
  'email_from_address',
  'email_from_name',
  'smtp_host',
  'smtp_port',
  'smtp_username',
  'smtp_password',
  'sms_provider',
  'sms_access_key_id',
  'sms_access_key_secret',
  'sms_sign_name',
  'sms_template_code',
  'sms_sdk_app_id',
] as const;
