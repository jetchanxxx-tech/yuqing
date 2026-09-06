import { Button, Card, Form, Input, Typography, App } from 'antd';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../stores/auth';

export default function LoginPage() {
  const { message } = App.useApp();
  const { login, loading } = useAuth();
  const navigate = useNavigate();

  const onFinish = async (values: { email: string; password: string }) => {
    try {
      await login(values.email, values.password);
      message.success('登录成功');
      navigate('/dashboard', { replace: true });
    } catch {
      message.error('邮箱或密码不正确');
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
          微舆舆情
        </Typography.Title>
        <Typography.Paragraph type="secondary" style={{ textAlign: 'center' }}>
          多 Agent 协作研判 · AI 原生舆情监测
        </Typography.Paragraph>
        <Form onFinish={onFinish} layout="vertical" size="large">
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
        </Form>
        <Typography.Paragraph style={{ textAlign: 'center', marginBottom: 0 }}>
          还没有账号？<Link to="/register">免费注册</Link>
        </Typography.Paragraph>
      </Card>
    </div>
  );
}
