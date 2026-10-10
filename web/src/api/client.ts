import axios, { type InternalAxiosRequestConfig } from 'axios';
import { notifyApiError } from './error';

export const AUTH_CLEARED_EVENT = 'auth:cleared';
export const AUTH_CONFIRMED_EVENT = 'auth:confirmed';
export const AUTH_PENDING_EVENT = 'auth:pending';
export const AUTH_UNAVAILABLE_EVENT = 'auth:unavailable';

type RetriableConfig = InternalAxiosRequestConfig & { _retry?: boolean; _authRevision?: number };
let revision = 0;
let refreshFlight: Promise<void> | null = null;
export const sessionRevision = () => revision;

export function clearSession(): void {
  revision += 1;
  refreshFlight = null;
  for (const key of ['principal', 'access_token', 'refresh_token']) localStorage.removeItem(key);
  window.dispatchEvent(new Event(AUTH_CLEARED_EVENT));
}

export function storeSessionTokens(accessToken: string, refreshToken: string): void {
  revision += 1;
  refreshFlight = null;
  localStorage.setItem('access_token', accessToken);
  localStorage.setItem('refresh_token', refreshToken);
}

export function confirmIdentity(identity: unknown): void {
  localStorage.setItem('principal', JSON.stringify(identity));
  window.dispatchEvent(new CustomEvent(AUTH_CONFIRMED_EVENT, { detail: identity }));
}

function assertCurrent(expected: number): void {
  if (expected !== revision) throw new axios.CanceledError('Session changed');
}

const client = axios.create({ baseURL: '/api/v1', timeout: 30000 });
client.interceptors.request.use((config: RetriableConfig) => {
  if (config._authRevision !== undefined) assertCurrent(config._authRevision);
  config._authRevision = revision;
  const token = localStorage.getItem('access_token');
  if (token) config.headers.Authorization = `Bearer ${token}`;
  else delete config.headers.Authorization;
  return config;
});

async function refreshIdentity(): Promise<void> {
  if (refreshFlight) return refreshFlight;
  const expected = revision;
  const refreshToken = localStorage.getItem('refresh_token');
  if (!refreshToken) {
    clearSession();
    throw new Error('No refresh credential');
  }
  window.dispatchEvent(new Event(AUTH_PENDING_EVENT));
  const flight = (async () => {
    try {
      const { data } = await axios.post('/api/v1/auth/refresh', { refresh_token: refreshToken }, { timeout: 30000 });
      assertCurrent(expected);
      // Confirm current roles and plan before publishing the new credentials.
      // Raw axios prevents this request from recursively starting a refresh.
      const me = await axios.get('/api/v1/auth/me', {
        headers: { Authorization: `Bearer ${data.access_token}` }, timeout: 30000,
      });
      assertCurrent(expected);
      localStorage.setItem('access_token', data.access_token);
      localStorage.setItem('refresh_token', data.refresh_token);
      confirmIdentity(me.data);
    } catch (error) {
      if (expected === revision) {
        if (axios.isAxiosError(error) && error.response?.status === 401) clearSession();
        else window.dispatchEvent(new CustomEvent(AUTH_UNAVAILABLE_EVENT, { detail: error }));
      }
      throw error;
    }
  })();
  refreshFlight = flight;
  try { await flight; } finally { if (refreshFlight === flight) refreshFlight = null; }
}

client.interceptors.response.use(
  (response) => {
    const config = response.config as RetriableConfig;
    if (config._authRevision !== undefined) assertCurrent(config._authRevision);
    return response;
  },
  async (error: unknown) => {
    if (!axios.isAxiosError(error)) return Promise.reject(error);
    const config = error.config as RetriableConfig | undefined;
    if (config?._authRevision !== undefined && config._authRevision !== revision) {
      return Promise.reject(new axios.CanceledError('Session changed'));
    }
    const url = config?.url ?? '';
    const publicAuth = ['/auth/login', '/auth/register', '/auth/refresh', '/auth/phone/login', '/auth/phone/send-code', '/auth/password-reset/request', '/auth/password-reset/confirm', '/auth/verify-email'].includes(url);
    const identityInputRejected = error.response?.data?.code === 'IDENTITY_CHECK_FAILED';
    if (config && !publicAuth && !identityInputRejected && error.response?.status === 401) {
      if (config._retry) { clearSession(); return Promise.reject(error); }
      config._retry = true;
      // Delayed expired-token responses retry the already refreshed credential.
      const currentToken = localStorage.getItem('access_token');
      if (!currentToken || config.headers.Authorization === `Bearer ${currentToken}`) {
        try { await refreshIdentity(); } catch (refreshError) { return Promise.reject(refreshError); }
      }
      return client(config);
    }
    // Administrative views show permission/CAS errors in the affected context.
    if (!url.startsWith('/admin/')) notifyApiError(error);
    return Promise.reject(error);
  },
);
export default client;
