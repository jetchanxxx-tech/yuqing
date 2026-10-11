import { Navigate, useLocation } from 'react-router-dom';
import { Alert, Button, Spin } from 'antd';
import { useAuth } from '../stores/auth';

export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { principal, loading, identityError, reloadIdentity } = useAuth();
  const location = useLocation();
  if (!principal && loading) return <div style={{ padding: 48, textAlign: 'center' }}><Spin /></div>;
  if (!principal && identityError) return (
    <div style={{ maxWidth: 600, margin: '80px auto', padding: 24 }}>
      <Alert type="error" showIcon message="登录状态暂时不可用" description={identityError} />
      <Button style={{ marginTop: 16 }} onClick={() => void reloadIdentity().catch(() => {})}>重新加载登录状态</Button>
    </div>
  );
  if (!principal) return <Navigate to="/login" replace />;
  if (principal.user_status === 'closure_pending' && location.pathname !== '/account-closure') return <Navigate to="/account-closure" replace />;
  return <>{children}</>;
}
