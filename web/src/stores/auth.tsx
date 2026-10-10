import { createContext, useContext, useState, useCallback, useEffect, useRef } from 'react';
import { TimezoneContext, DEFAULT_TIMEZONE } from '../lib/format';
import { useQueryClient } from '@tanstack/react-query';
import client, {
  AUTH_CLEARED_EVENT, AUTH_CONFIRMED_EVENT, AUTH_PENDING_EVENT, AUTH_UNAVAILABLE_EVENT,
  clearSession, confirmIdentity, sessionRevision, storeSessionTokens,
} from '../api/client';

interface Principal {
  user_id: string;
  tenant_id: string;
  email: string;
  roles: string[];
  plan_code: string;
  tenant_status: string;
  timezone: string;
  avatar_url: string;
  name: string;
}
interface AuthState {
  principal: Principal | null;
  loading: boolean;
  identityError: string | null;
  reloadIdentity: () => Promise<void>;
  login: (email: string, password: string) => Promise<void>;
  loginPhone: (phone: string, code: string) => Promise<void>;
  register: (email: string, password: string, name: string) => Promise<void>;
  logout: () => void;
}
const AuthContext = createContext<AuthState>({} as AuthState);
export const useAuth = () => useContext(AuthContext);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  // Cached principal is never authorization input, including malformed JSON.
  const [principal, setPrincipal] = useState<Principal | null>(null);
  const [loading, setLoading] = useState(() => !!(localStorage.getItem('access_token') || localStorage.getItem('refresh_token')));
  const [identityError, setIdentityError] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const confirmedUser = useRef<string | null>(null);

  const reloadIdentity = useCallback(async () => {
    const expected = sessionRevision();
    setLoading(true);
    setIdentityError(null);
    try {
      const { data } = await client.get<Principal>('/auth/me');
      if (expected !== sessionRevision()) return;
      confirmIdentity(data);
    } catch (error) {
      if (expected === sessionRevision() && (localStorage.getItem('access_token') || localStorage.getItem('refresh_token'))) {
        setPrincipal(null);
        setIdentityError('暂时无法确认登录状态，请稍后重试。');
      }
      throw error;
    } finally { if (expected === sessionRevision()) setLoading(false); }
  }, []);

  useEffect(() => {
    const clear = () => {
      setPrincipal(null);
      setIdentityError(null);
      setLoading(false);
      confirmedUser.current = null;
      void queryClient.cancelQueries();
      queryClient.clear();
    };
    const confirm = (event: Event) => {
      const identity = (event as CustomEvent<Principal>).detail;
      if (confirmedUser.current && confirmedUser.current !== identity.user_id) {
        void queryClient.cancelQueries();
        queryClient.clear();
      }
      confirmedUser.current = identity.user_id;
      setPrincipal(identity);
      setIdentityError(null);
      setLoading(false);
    };
    const pending = () => { setPrincipal(null); setLoading(true); setIdentityError(null); };
    const unavailable = () => {
      setPrincipal(null);
      setLoading(false);
      setIdentityError('暂时无法确认登录状态，请稍后重试。');
    };
    window.addEventListener(AUTH_CLEARED_EVENT, clear);
    window.addEventListener(AUTH_CONFIRMED_EVENT, confirm);
    window.addEventListener(AUTH_PENDING_EVENT, pending);
    window.addEventListener(AUTH_UNAVAILABLE_EVENT, unavailable);
    if (localStorage.getItem('access_token') || localStorage.getItem('refresh_token')) void reloadIdentity().catch(() => {});
    return () => {
      window.removeEventListener(AUTH_CLEARED_EVENT, clear);
      window.removeEventListener(AUTH_CONFIRMED_EVENT, confirm);
      window.removeEventListener(AUTH_PENDING_EVENT, pending);
      window.removeEventListener(AUTH_UNAVAILABLE_EVENT, unavailable);
    };
  }, [queryClient, reloadIdentity]);

  const authenticate = useCallback(async (path: string, body: Record<string, string>) => {
    setLoading(true);
    try {
      const { data } = await client.post(path, body);
      clearSession();
      storeSessionTokens(data.access_token, data.refresh_token);
      await reloadIdentity();
    } finally { setLoading(false); }
  }, [reloadIdentity]);
  const login = useCallback((email: string, password: string) => authenticate('/auth/login', { email, password }), [authenticate]);
  const loginPhone = useCallback((phone: string, code: string) => authenticate('/auth/phone/login', { phone, code }), [authenticate]);
  const register = useCallback((email: string, password: string, name: string) => authenticate('/auth/register', { email, password, name }), [authenticate]);
  const logout = useCallback(() => clearSession(), []);

  return <AuthContext.Provider value={{ principal, loading, identityError, reloadIdentity, login, loginPhone, register, logout }}><TimezoneContext.Provider value={principal?.timezone || DEFAULT_TIMEZONE}>{children}</TimezoneContext.Provider></AuthContext.Provider>;
}
