import { Card, Form, Input, Button, Typography, Divider } from 'antd';

export default function SettingsPage() {
  return (
    <div>
      <Typography.Title level={4}>账户设置</Typography.Title>
      <Card style={{ borderRadius: 16, maxWidth: 600 }}>
        <Form layout="vertical">
          <Form.Item label="姓名" name="name"><Input size="large" /></Form.Item>
          <Form.Item label="邮箱" name="email"><Input size="large" disabled /></Form.Item>
          <Form.Item><Button type="primary" htmlType="submit" style={{ borderRadius: 9999 }}>保存修改</Button></Form.Item>
        </Form>
        <Divider />
        <Typography.Title level={5}>修改密码</Typography.Title>
        <Form layout="vertical">
          <Form.Item label="当前密码" name="currentPassword"><Input.Password size="large" /></Form.Item>
          <Form.Item label="新密码" name="newPassword"><Input.Password size="large" /></Form.Item>
          <Form.Item><Button type="primary" htmlType="submit" style={{ borderRadius: 9999 }}>更新密码</Button></Form.Item>
        </Form>
      </Card>
    </div>
  );
}
