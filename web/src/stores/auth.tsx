import { createContext, useContext, useState, useCallback } from 'react';
import client from '../api/client';

interface Principal {
  user_id: string;
  tenant_id: string;
  email: string;
  roles: string[];
  plan_code: string;
}

interface AuthState {
  principal: Principal | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string, name: string) => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthState>({} as AuthState);
export const useAuth = () => useContext(AuthContext);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [principal, setPrincipal] = useState<Principal | null>(() => {
    const stored = localStorage.getItem('principal');
    return stored ? JSON.parse(stored) : null;
  });
  const [loading, setLoading] = useState(false);

  const login = useCallback(async (email: string, password: string) => {
    setLoading(true);
    try {
      const { data } = await client.post('/auth/login', { email, password });
      localStorage.setItem('access_token', data.access_token);
      localStorage.setItem('refresh_token', data.refresh_token);
      localStorage.setItem('principal', JSON.stringify(data.user));
      setPrincipal(data.user);
    } finally {
      setLoading(false);
    }
  }, []);

  const register = useCallback(async (email: string, password: string, name: string) => {
    setLoading(true);
    try {
      const { data } = await client.post('/auth/register', { email, password, name });
      localStorage.setItem('access_token', data.access_token);
      localStorage.setItem('refresh_token', data.refresh_token);
      localStorage.setItem('principal', JSON.stringify(data.user));
      setPrincipal(data.user);
    } finally {
      setLoading(false);
    }
  }, []);

  const logout = useCallback(() => {
    localStorage.clear();
    setPrincipal(null);
  }, []);

  return (
    <AuthContext.Provider value={{ principal, loading, login, register, logout }}>
      {children}
    </AuthContext.Provider>
  );
}
