import { useEffect, useState } from 'react';
import { Alert, Button, Card, Form, Input, Result, Typography } from 'antd';
import { Link } from 'react-router-dom';
import { confirmEmailChange } from '../api/user';
import { useAuth } from '../stores/auth';
import { identityError } from '../lib/identity';

// Keep the bearer only in this page's memory; inline re-login preserves intent
// without putting it into URLs, persistent storage, referrers or login redirects.
export default function EmailChangePage() {
  const [token] = useState(() => new URLSearchParams(window.location.hash.slice(1)).get('token') || '');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [success, setSuccess] = useState(false);
  const { principal, login, logout, loading, identityError: sessionError, reloadIdentity } = useAuth();
  useEffect(() => { window.history.replaceState(window.history.state, '', window.location.pathname); }, []);
  const confirm = async () => {
    setError(''); setBusy(true);
    try { await confirmEmailChange(token); logout(); setSuccess(true); }
    catch (e) { setError(identityError(e)); }
    finally { setBusy(false); }
  };
  return <div style={{ maxWidth: 440, margin: '64px auto', padding: 16 }}><Card>
    <Typography.Title level={3}>确认更换邮箱</Typography.Title>
    {success ? <Result status="success" title="邮箱已更换，请重新登录" extra={<Link to="/login">返回登录</Link>} /> : <>
      {error && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}
      {!token ? <Alert type="error" message="链接无效，请回用户中心重新申请" action={<Link to="/settings">用户中心</Link>} /> : sessionError ? <Alert type="error" message={sessionError} action={<Button loading={loading} onClick={() => void reloadIdentity().catch(() => {})}>重试登录状态</Button>} /> : principal ? <>
        <Typography.Paragraph>当前账户：{principal.email}。确认后将更换登录邮箱，并退出当前登录。原邮箱会收到安全通知。</Typography.Paragraph>
        <Button type="primary" onClick={() => void confirm()} loading={busy}>确认更换邮箱</Button>
      </> : <Form layout="vertical" onFinish={async (v: { email: string; password: string }) => {
        setError(''); try { await login(v.email, v.password); } catch (e) { setError(identityError(e)); }
      }}>
        <Typography.Paragraph>请登录发起更换的账户，再确认新邮箱。</Typography.Paragraph>
        <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email', message: '请输入原邮箱' }]}><Input autoComplete="email" /></Form.Item>
        <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}><Input.Password autoComplete="current-password" /></Form.Item>
        <Button type="primary" htmlType="submit" loading={loading}>登录后继续确认</Button>
      </Form>}
    </>}
  </Card></div>;
}
