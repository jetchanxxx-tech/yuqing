import { useState } from 'react';
import { Alert, Button, Form, Input, Modal, Typography } from 'antd';
import { adminErrorMessage, createAdminUser, type CreatedAccount } from '../../api/admin';

export default function CreateAccountDialog({ onClose, onCreated, onOpen }: { onClose: () => void; onCreated: () => Promise<void>; onOpen: (id: string) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [created, setCreated] = useState<CreatedAccount | null>(null);
  const submit = async (values: { email: string; name: string; tenant_name?: string }) => {
    setBusy(true); setError('');
    try {
      const result = await createAdminUser({ email: values.email.trim(), name: values.name.trim(), tenant_name: values.tenant_name?.trim() });
      setCreated(result); await onCreated();
    } catch (e) { setError(adminErrorMessage(e)); }
    finally { setBusy(false); }
  };
  return <Modal open title="新建账号" footer={null} maskClosable={false} closable={!busy} onCancel={() => { if (!busy) onClose(); }}>
    {created ? <>
      <Alert showIcon type={created.activation.state === 'accepted' ? 'success' : 'warning'} message={created.activation.state === 'accepted' ? '账号已创建；激活邮件已受理，送达尚未确认' : created.activation.state === 'failed' ? '账号已创建；激活发送失败，可在账号详情重试' : '账号已创建；激活发送结果待确认，请查看账号详情后重试'} />
      <Typography.Paragraph style={{ marginTop: 16 }}>用户 ID：{created.user_id}</Typography.Paragraph>
      <Button type="primary" onClick={() => onOpen(created.user_id)}>查看账号</Button>
    </> : <>
      <Typography.Paragraph>创建待激活账号和初始团队，按当前规则赠送一次试用额度。用户通过邮件自行设置密码后登录。</Typography.Paragraph>
      {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} />}
      <Form layout="vertical" onFinish={submit} disabled={busy}>
        <Form.Item name="email" label="邮箱" rules={[{ required: true, type: 'email', message: '请输入有效邮箱' }]}><Input autoComplete="off" maxLength={254} /></Form.Item>
        <Form.Item name="name" label="昵称" rules={[{ required: true, whitespace: true, message: '请输入昵称' }]}><Input maxLength={100} /></Form.Item>
        <Form.Item name="tenant_name" label="初始团队名称"><Input maxLength={100} placeholder="留空使用昵称创建团队名称" /></Form.Item>
        <Button type="primary" htmlType="submit" loading={busy}>创建待激活账号</Button>
      </Form>
    </>}
  </Modal>;
}
