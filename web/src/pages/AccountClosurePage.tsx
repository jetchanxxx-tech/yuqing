import { useState } from 'react';
import { Alert, Button, Card, Form, Input, Space, Typography } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { Navigate, useNavigate } from 'react-router-dom';
import { cancelClosure, closureStatus } from '../api/accountClosure';
import { useAuth } from '../stores/auth';
import { useDateTime } from '../lib/format';
import { identityError } from '../lib/identity';

export default function AccountClosurePage() {
  const { principal, logout } = useAuth();
  const navigate = useNavigate();
  const format = useDateTime();
  const status = useQuery({ queryKey: ['account-closure', principal?.user_id], queryFn: closureStatus, retry: false });
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  if (principal?.user_status !== 'closure_pending') return <Navigate to="/dashboard" replace />;
  const leave = () => { logout(); navigate('/login', { replace: true }); };
  const withdraw = async () => {
    if (busy || !password) return;
    setBusy(true); setError('');
    try { await cancelClosure(password); leave(); }
    catch (e) { setError(identityError(e)); void status.refetch(); }
    finally { setBusy(false); }
  };
  const canWithdraw = status.data?.state === 'pending' && Date.now() < Date.parse(status.data.withdraw_until);
  return <Card style={{ maxWidth: 620, margin: '60px auto' }}>
    <Space direction="vertical" style={{ width: '100%' }}>
      <Typography.Title level={2}>注销等待期</Typography.Title>
      <Typography.Paragraph>当前账号仅可查看注销状态、撤回申请或退出。原登录凭据和 API 密钥已撤销。</Typography.Paragraph>
      {status.isPending && <Typography.Text>正在读取注销状态…</Typography.Text>}
      {status.isError && <Alert type="error" message="注销状态暂时不可用" action={<Button onClick={() => void status.refetch()}>重试</Button>} />}
      {status.data && <>
        <Typography.Text>申请时间：{format(status.data.requested_at)}</Typography.Text>
        <Typography.Text>撤回截止：{format(status.data.withdraw_until)}</Typography.Text>
        {!canWithdraw && <Alert type="info" message="撤回期已结束，注销处理将按政策继续。" />}
        {status.data.error_code && <Alert type="warning" message="注销处理尚有待办事项，账户数据仍保留等待处理。" />}
      </>}
      {error && <Alert type="error" showIcon message={error} />}
      {canWithdraw && <Form layout="vertical" onFinish={() => void withdraw()}>
        <Form.Item label="当前密码" htmlFor="withdraw-password" required><Input.Password id="withdraw-password" value={password} disabled={busy} onChange={e => setPassword(e.target.value)} autoComplete="current-password" /></Form.Item>
        <Button type="primary" htmlType="submit" aria-label="撤回注销申请" aria-busy={busy} loading={busy} disabled={busy || !password}>撤回注销申请</Button>
      </Form>}
      <Button disabled={busy} onClick={leave}>退出登录</Button>
    </Space>
  </Card>;
}
