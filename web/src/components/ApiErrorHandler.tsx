import { useEffect, useRef } from 'react';
import { App } from 'antd';
import { useLocation, useNavigate } from 'react-router-dom';
import { API_ERROR_EVENT, type ApiErrorPayload, type ApiErrorEnvelope } from '../api/error';
import { useAuth } from '../stores/auth';

/**
 * 全局 API 错误呈现器：
 * - FORBIDDEN        → warning「套餐升级解锁此功能」
 * - UNAUTHORIZED     → 提示登录过期并回到登录页
 * - BUDGET_EXCEEDED  → 弹窗引导去 /plans 升级
 * - QUOTA_EXCEEDED   → warning 并引导去 /plans
 * - 其余信封 / 网络错误 → error 提示
 * 同一错误在 3s 内去重，避免并发请求刷屏。
 */
export function ApiErrorHandler() {
  const { message, modal } = App.useApp();
  const navigate = useNavigate();
  const location = useLocation();
  const { logout } = useAuth();
  const lastToast = useRef<{ key: string; at: number }>({ key: '', at: 0 });

  useEffect(() => {
    const handle = (e: Event) => {
      const { envelope, status } = (e as CustomEvent<ApiErrorPayload>).detail;

      // 3s 内重复错误只提示一次
      const dedupeKey = envelope ? `${status}:${envelope.code}:${envelope.message}` : `net:${status}`;
      const now = Date.now();
      if (dedupeKey === lastToast.current.key && now - lastToast.current.at < 3000) return;
      lastToast.current = { key: dedupeKey, at: now };

      if (envelope) {
        switch (envelope.code) {
          case 'UNAUTHORIZED': {
            message.warning('登录已过期，请重新登录');
            if (location.pathname !== '/login') {
              logout();
              navigate('/login', { replace: true });
            }
            return;
          }
          case 'FORBIDDEN': {
            message.warning('套餐升级解锁此功能');
            return;
          }
          case 'BUDGET_EXCEEDED': {
            modal.confirm({
              title: '额度不足',
              content: '当前套餐的 Token 预算已用完，升级套餐可继续使用全部功能。',
              okText: '去升级套餐',
              cancelText: '稍后再说',
              onOk: () => navigate('/plans'),
            });
            return;
          }
          case 'QUOTA_EXCEEDED': {
            modal.confirm({
              title: '次数已达上限',
              content: envelope.message || '本月可用次数已用完，升级套餐后可继续创建分析。',
              okText: '去升级套餐',
              cancelText: '稍后再说',
              onOk: () => navigate('/plans'),
            });
            return;
          }
          default: {
            message.error(envelope.message || '请求失败，请稍后重试');
          }
        }
      } else if (status === 0) {
        message.error('网络连接异常，请检查网络后重试');
      } else {
        message.error('服务暂时不可用，请稍后重试');
      }
    };
    window.addEventListener(API_ERROR_EVENT, handle);
    return () => window.removeEventListener(API_ERROR_EVENT, handle);
  }, [message, modal, navigate, location.pathname, logout]);

  return null;
}

export type { ApiErrorEnvelope };
