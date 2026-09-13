import { Navigate } from 'react-router-dom';
import { useAuth } from '../stores/auth';

export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { principal } = useAuth();
  if (!principal) return <Navigate to="/login" replace />;
  return <>{children}</>;
}
