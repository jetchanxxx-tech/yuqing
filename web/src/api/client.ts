import axios from 'axios';
import { notifyApiError } from './error';

/** 请求配置扩展：标记是否已做过 token 刷新重试 */
type RetriableConfig = {
  _retry?: boolean;
  headers?: Record<string, unknown>;
  url?: string;
  method?: unknown;
  baseURL?: string;
  data?: unknown;
  params?: unknown;
  timeout?: number;
};

const client = axios.create({
  baseURL: '/api/v1',
  timeout: 30000,
});

// 注入 access token
client.interceptors.request.use((config) => {
  const token = localStorage.getItem('access_token');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

// 401 自动刷新 token 重试；其余错误解析信封并广播（登录/注册接口除外，由页面自处理）
client.interceptors.response.use(
  (res) => res,
  async (error: unknown) => {
    const axiosError = error as { config?: RetriableConfig; response?: { status: number } };
    if (
      axiosError.config &&
      !axiosError.config._retry &&
      axiosError.response?.status === 401
    ) {
      axiosError.config._retry = true;
      const refreshToken = localStorage.getItem('refresh_token');
      if (refreshToken) {
        try {
          const { data } = await axios.post('/api/v1/auth/refresh', {
            refresh_token: refreshToken,
          });
          localStorage.setItem('access_token', data.access_token);
          localStorage.setItem('refresh_token', data.refresh_token);
          if (axiosError.config.headers) {
            axiosError.config.headers.Authorization = `Bearer ${data.access_token}`;
          }
          return client(axiosError.config as never);
        } catch {
          // 刷新失败：清空会话并回到登录页
          localStorage.clear();
          window.location.href = '/login';
          return Promise.reject(error);
        }
      }
    }
    notifyApiError(error);
    return Promise.reject(error);
  }
);

export default client;
