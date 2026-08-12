import { Card, Form, Input, Button, Typography, message } from 'antd';
import { Link, useNavigate } from 'react-router-dom';
import { useAuth } from '../stores/auth';

export default function RegisterPage() {
  const { register } = useAuth();
  const navigate = useNavigate();

  const onFinish = async (values: { email: string; password: string; name: string }) => {
    try {
      await register(values.email, values.password, values.name);
      message.success('注册成功，正在跳转...');
      navigate('/dashboard');
    } catch {
      message.error('注册失败，请重试');
    }
  };

  return (
    <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: '#f5f5f5' }}>
      <Card style={{ width: 400, borderRadius: 16 }}>
        <Typography.Title level={3} style={{ textAlign: 'center', color: '#FF2442' }}>注册微舆舆情</Typography.Title>
        <Form onFinish={onFinish} layout="vertical">
          <Form.Item name="name" label="姓名" rules={[{ required: true }]}>
            <Input size="large" />
          </Form.Item>
          <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email' }]}>
            <Input size="large" />
          </Form.Item>
          <Form.Item name="password" label="密码" rules={[{ required: true, min: 8 }]}>
            <Input.Password size="large" placeholder="至少8位" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block size="large" style={{ borderRadius: 9999 }}>注册</Button>
          </Form.Item>
        </Form>
        <div style={{ textAlign: 'center' }}><Link to="/login">已有账号？去登录</Link></div>
      </Card>
    </div>
  );
}
