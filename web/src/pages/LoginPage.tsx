import { useState } from 'react';
import { requestPhoneLogin } from '../api/user';
import { acceptedMessage, identityError } from '../lib/identity';
import { Alert, Tabs, Button, Card, Form, Input, Typography, App } from 'antd';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../stores/auth';

export default function LoginPage() {
  const { message } = App.useApp();
  const { login, loginPhone, loading } = useAuth();
  const navigate = useNavigate();
  const [method, setMethod] = useState('email');
  const [error, setError] = useState('');
  const [accepted, setAccepted] = useState(false);
  const [sending, setSending] = useState(false);
  const [phoneForm] = Form.useForm();

  const onFinish = async (values: { email: string; password: string }) => {
    setError('');
    try {
      await login(values.email, values.password);
      message.success('登录成功');
      navigate('/dashboard', { replace: true });
    } catch (e) {
      setError(identityError(e));
    }
  };

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#f5f5f5',
      }}
    >
      <Card style={{ width: 400, borderRadius: 16, boxShadow: '0 8px 32px rgba(0,0,0,0.12)' }}>
        <Typography.Title level={3} style={{ textAlign: 'center', color: '#FF2442', marginTop: 0 }}>
          盘古舆情
        </Typography.Title>
        <Typography.Paragraph type="secondary" style={{ textAlign: 'center' }}>
          多 Agent 协作研判 · AI 原生舆情监测
        </Typography.Paragraph>
        <Tabs activeKey={method} onChange={v => { setMethod(v); setError(''); setAccepted(false); }} items={[{ key: 'email', label: '邮箱登录' }, { key: 'phone', label: '手机号登录' }]} />
        {error && <Alert type="error" message={error} showIcon style={{ marginBottom: 16 }} />}
        {accepted && <Alert type="info" message={acceptedMessage} showIcon style={{ marginBottom: 16 }} />}
        {method === 'email' ? <Form key="email-login" onFinish={onFinish} layout="vertical" size="large">
          <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email', message: '请输入正确的邮箱' }]}>
            <Input placeholder="you@example.com" autoComplete="email" />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true, message: '请输入密码' }]}>
            <Input.Password placeholder="请输入密码" autoComplete="current-password" />
          </Form.Item>
          <Form.Item style={{ marginBottom: 8 }}>
            <Button type="primary" htmlType="submit" block loading={loading}>
              登录
            </Button>
          </Form.Item>
        </Form> : <Form key="phone-login" form={phoneForm} layout="vertical" onFinish={async (v: { phone: string; code: string }) => {
          setError(''); try { await loginPhone(v.phone, v.code); navigate('/dashboard', { replace: true }); } catch (e) { setError(identityError(e)); }
        }}>
          <Form.Item name="phone" label="手机号" rules={[{ required: true, pattern: /^1[3-9]\d{9}$/, message: '请输入已绑定手机号' }]}><Input autoComplete="tel" /></Form.Item>
          <Button loading={sending} onClick={() => { void phoneForm.validateFields(['phone']).then(async v => {
            setError(''); setAccepted(false); setSending(true);
            try { await requestPhoneLogin(v.phone); setAccepted(true); } catch (e) { setError(identityError(e)); } finally { setSending(false); }
          }).catch(() => {}); }}>请求短信验证码</Button>
          <Form.Item name="code" label="验证码" rules={[{ required: true, pattern: /^\d{6}$/, message: '请输入 6 位验证码' }]}><Input autoComplete="one-time-code" /></Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading}>手机登录</Button>
        </Form>}
        <Typography.Paragraph style={{ marginTop: 16 }}><Link to="/forgot-password">忘记密码</Link></Typography.Paragraph>
        <Typography.Paragraph style={{ textAlign: 'center', marginBottom: 0 }}>
          还没有账号？<Link to="/register">免费注册</Link>
        </Typography.Paragraph>
      </Card>
    </div>
  );
}
