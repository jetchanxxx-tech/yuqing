import { useState } from 'react';
import { Alert, Button, Form, Input, Modal } from 'antd';
import { adminErrorMessage, adminErrorStatus, updateAdminNickname, type AdminUserDetail } from '../../api/admin';
export default function NicknameDialog({ user, onClose, onSuccess }: { user: AdminUserDetail; onClose: () => void; onSuccess: () => Promise<void> }) {
  const [error, setError] = useState(''); const [busy, setBusy] = useState(false);
  return <Modal open title="修改昵称" footer={null} onCancel={() => { if (!busy) onClose(); }} maskClosable={false}>
    {error && <Alert type="error" message={error} />}
    <Form layout="vertical" initialValues={{ name: user.name }} onFinish={async (values: { name: string; reason: string }) => {
      setBusy(true); setError('');
      try { await updateAdminNickname(user.id, { name: values.name.trim(), reason: values.reason.trim(), expected_version: user.row_version }); await onSuccess(); onClose(); }
      catch (e) { setError(adminErrorMessage(e)); if (adminErrorStatus(e) === 409) await onSuccess(); }
      finally { setBusy(false); }
    }}>
      <Form.Item name="name" label="新昵称" rules={[{ required: true, whitespace: true, message: '请输入昵称' }]}><Input maxLength={100} disabled={busy} /></Form.Item>
      <Form.Item name="reason" label="操作原因" rules={[{ required: true, whitespace: true, message: '请输入原因' }]}><Input.TextArea maxLength={2000} disabled={busy} /></Form.Item>
      <Button type="primary" htmlType="submit" loading={busy}>保存昵称</Button>
    </Form>
  </Modal>;
}
