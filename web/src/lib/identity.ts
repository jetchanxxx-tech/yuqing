import axios from 'axios';

export const acceptedMessage = '请求已受理；若账户符合条件，将发送验证信息。送达尚未确认。';
export function identityError(error: unknown): string {
  if (!axios.isAxiosError(error)) return '操作失败，请稍后重试';
  const status = error.response?.status;
  const state = error.response?.data?.details?.state;
  if (state === 'expired') return '链接已过期，请重新请求验证信息';
  if (state === 'used') return '链接已使用，请重新请求验证信息';
  if (state === 'invalid') return '链接无效或账户状态已变更，请重新请求验证信息';
  if (status === 429) return `请求过于频繁，请在 ${error.response?.headers['retry-after'] || '60'} 秒后重试`;
  if (!error.response || status === 503) return '服务暂时不可用，请稍后重试；已填写的信息会保留';
  if (status === 409) {
    if (error.config?.url?.includes('/phone/')) return '手机号已被占用或操作发生冲突，请核对后重试';
    if (error.config?.url?.includes('password-reset')) return '新密码需至少 8 位，并同时包含字母和数字';
    return '目标邮箱已被占用或操作发生冲突，请核对后重试';
  }
  if (status === 401) return '密码、验证码不正确，或凭据已失效，请核对后重试';
  if (status === 403) return '当前账户无权执行此操作';
  return '信息无效，请核对输入或重新请求验证信息';
}
export const passwordRules = [
  { required: true, message: '请输入新密码' },
  { pattern: /^(?=.*[A-Za-z])(?=.*\d)\S{8,}$/, message: '至少 8 位，需同时包含字母和数字' },
];
