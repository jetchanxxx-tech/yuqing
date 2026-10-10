import { useEffect, useState } from 'react';
import { Alert, Button, Card, Form, Input, Result, Tabs, Typography } from 'antd';
import { Link, useLocation } from 'react-router-dom';
import { activateAccount, requestPasswordReset, resetPassword } from '../api/user';
import { useAuth } from '../stores/auth';
import { acceptedMessage, identityError, passwordRules } from '../lib/identity';

export default function PasswordResetPage() {
  const location = useLocation();
  const isActivation = location.pathname === '/activate';
  const isLink = isActivation || location.pathname === '/reset-password';
  const [token] = useState(() => new URLSearchParams(window.location.hash.slice(1)).get('token') || '');
  const [method, setMethod] = useState('email');
  const [error, setError] = useState('');
  const [accepted, setAccepted] = useState(false);
  const [success, setSuccess] = useState(false);
  const [busy, setBusy] = useState(false);
  const [phoneForm] = Form.useForm();
  const { logout } = useAuth();
  useEffect(() => { window.history.replaceState(window.history.state, '', window.location.pathname); }, []);
  const request = async (target: { email?: string; phone?: string }) => {
    setError(''); setAccepted(false); setBusy(true);
    try { await requestPasswordReset(target); setAccepted(true); }
    catch (e) { setError(identityError(e)); }
    finally { setBusy(false); }
  };
  const confirm = async (values: { email?: string; phone?: string; code?: string; new_password: string }) => {
    setError(''); setBusy(true);
    try {
      if (isActivation) {
        await activateAccount({ email: values.email || '', token, new_password: values.new_password });
      } else {
        await resetPassword({ ...(isLink ? { token } : { phone: values.phone, code: values.code }), new_password: values.new_password });
      }
      logout(); setSuccess(true);
    } catch (e) { setError(identityError(e)); }
    finally { setBusy(false); }
  };
  const fields = <>
    <Form.Item name="new_password" label="新密码" rules={passwordRules}><Input.Password autoComplete="new-password" /></Form.Item>
    <Form.Item name="confirm" label="确认新密码" dependencies={['new_password']} rules={[{ required: true, message: '请再次输入新密码' }, ({ getFieldValue }) => ({ validator(_, value) { return value === getFieldValue('new_password') ? Promise.resolve() : Promise.reject(new Error('两次密码不一致')); } })]}><Input.Password autoComplete="new-password" /></Form.Item>
    <Button type="primary" htmlType="submit" loading={busy}>确认重置密码</Button>
  </>;
  return <div style={{ maxWidth: 440, margin: '64px auto', padding: 16 }}>
    <Card>
      <Typography.Title level={3}>{isActivation ? '激活账户' : '找回密码'}</Typography.Title>
      {success ? <Result status="success" title="密码已重置，请重新登录" extra={<Link to="/login">返回登录</Link>} /> : <>
        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} />}
        {accepted && <Alert type="info" showIcon message={acceptedMessage} style={{ marginBottom: 16 }} />}
        {isLink ? token ? <Form layout="vertical" onFinish={confirm}>{isActivation && <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email', message: '请输入管理员发送到的邮箱' }]}><Input autoComplete="email" /> </Form.Item>}{fields}</Form> : <Alert type="error" message="链接无效，请重新申请重置链接" action={<Link to="/forgot-password">重新申请</Link>} /> : <>
          <Tabs activeKey={method} onChange={(v) => { setMethod(v); setError(''); setAccepted(false); }} items={[{ key: 'email', label: '邮箱找回' }, { key: 'phone', label: '手机找回' }]} />
          {method === 'email' ? <Form layout="vertical" onFinish={request}>
            <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email', message: '请输入邮箱' }]}><Input autoComplete="email" /></Form.Item>
            <Button type="primary" htmlType="submit" loading={busy}>请求重置链接</Button>
          </Form> : <Form form={phoneForm} layout="vertical" onFinish={confirm}>
            <Typography.Paragraph type="secondary">仅支持已验证绑定的手机号，不会创建新账户。</Typography.Paragraph>
            <Form.Item name="phone" label="手机号" rules={[{ required: true, pattern: /^1[3-9]\d{9}$/, message: '请输入 11 位手机号' }]}><Input autoComplete="tel" /></Form.Item>
            <Button loading={busy} onClick={() => { void phoneForm.validateFields(['phone']).then(v => request({ phone: v.phone })).catch(() => {}); }}>请求短信验证码</Button>
            <Form.Item name="code" label="验证码" rules={[{ required: true, pattern: /^\d{6}$/, message: '请输入 6 位验证码' }]}><Input autoComplete="one-time-code" /></Form.Item>
            {fields}
          </Form>}
        </>}
        <Typography.Paragraph style={{ marginTop: 20 }}><Link to="/login">返回登录</Link></Typography.Paragraph>
      </>}
    </Card>
  </div>;
}
