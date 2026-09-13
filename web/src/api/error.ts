/**
 * API 错误信封规范：{code, message, details, request_id}
 * axios 拦截器解析信封后通过 window 事件广播，由 <ApiErrorHandler/>
 * （antd App 上下文内）统一呈现 toast / 升级弹窗。
 */

export interface ApiErrorEnvelope {
  code: string;
  message: string;
  details?: unknown;
  request_id?: string;
}

export interface ApiErrorPayload {
  /** 解析出的业务信封；网络错误/超时等无响应体时为 null */
  envelope: ApiErrorEnvelope | null;
  /** HTTP 状态码，网络错误时为 0 */
  status: number;
}

export const API_ERROR_EVENT = 'api:error';

/** 登录/注册等页面内自处理错误的接口，不进入全局提示 */
const LOCAL_HANDLED_PREFIXES = ['/auth/', '/api/v1/auth/'];

export function readEnvelope(error: unknown): ApiErrorEnvelope | null {
  const data = (error as { response?: { data?: unknown } } | undefined)?.response?.data;
  if (data && typeof data === 'object' && 'code' in data && 'message' in data) {
    return data as ApiErrorEnvelope;
  }
  return null;
}

function readStatus(error: unknown): number {
  return (error as { response?: { status?: number } } | undefined)?.response?.status ?? 0;
}

function readUrl(error: unknown): string {
  return (error as { config?: { url?: string } } | undefined)?.config?.url ?? '';
}

/** 由 axios 响应拦截器调用：解析信封并广播 api:error 事件 */
export function notifyApiError(error: unknown): void {
  const url = readUrl(error);
  if (LOCAL_HANDLED_PREFIXES.some((p) => url.startsWith(p))) return;
  window.dispatchEvent(
    new CustomEvent<ApiErrorPayload>(API_ERROR_EVENT, {
      detail: { envelope: readEnvelope(error), status: readStatus(error) },
    })
  );
}

export function isTerminalHttpError(error: unknown): boolean {
  const status = readStatus(error);
  return status >= 500 || status === 0;
}
