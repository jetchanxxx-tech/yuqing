import { useState } from 'react';
import { Alert, Button, Descriptions, Form, Input, InputNumber, Modal, Typography } from 'antd';
import { adjustAdminCredit, adminErrorMessage, adminErrorStatus, getAdminTenant, type CreditAdjustment } from '../../api/admin';

export default function CreditAdjustmentDialog({ tenantID, tenantName, balance, version, onClose, onSuccess }: {
  tenantID: string; tenantName: string; balance: number; version: number; onClose: () => void; onSuccess: () => Promise<void>;
}) {
  const [form] = Form.useForm();
  const delta = Form.useWatch('delta', form) as number | undefined;
  const [snapshot, setSnapshot] = useState({ balance, version });
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [intent, setIntent] = useState<CreditAdjustment | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const submit = async (values: { delta: number; reason: string }) => {
    const body = intent ?? { delta: values.delta, reason: values.reason.trim(), expected_version: snapshot.version, idempotency_key: key };
    setIntent(body); setBusy(true); setError(null);
    try { await adjustAdminCredit(tenantID, body); await onSuccess(); onClose(); }
    catch (e) { setError(e); }
    finally { setBusy(false); }
  };
  const reload = async () => {
    setBusy(true);
    try {
      const latest = await getAdminTenant(tenantID);
      setSnapshot({ balance: latest.credit?.balance ?? 0, version: latest.credit?.version ?? 0 });
      setIntent(null); setKey(crypto.randomUUID()); setError(null);
    } catch (e) { setError(e); }
    finally { setBusy(false); }
  };
  const canRevise = [400, 402, 409].includes(adminErrorStatus(error));
  return <Modal open title="调整报告额度" footer={null} maskClosable={false} closable={!busy} onCancel={() => { if (!busy) onClose(); }}>
    <Descriptions column={1} size="small" items={[
      { key: 'tenant', label: '租户', children: tenantName },
      { key: 'balance', label: '当前额度', children: snapshot.balance },
      { key: 'next', label: '调整后额度', children: snapshot.balance + (intent?.delta ?? delta ?? 0) },
      { key: 'version', label: '余额版本', children: snapshot.version },
      { key: 'key', label: '操作编号', children: <Typography.Text code>{key}</Typography.Text> },
    ]} />
    <Typography.Paragraph style={{ marginTop: 16 }}>正数增加、负数扣减。请确认数量和原因后提交，操作记录将永久保留。</Typography.Paragraph>
    {error !== null && <Alert type="error" showIcon message={adminErrorMessage(error)} description={canRevise ? '刷新余额后重新核对本次操作。' : '结果尚未确认时，请使用同一操作编号重试。'} style={{ marginBottom: 16 }} action={canRevise ? <Button disabled={busy} onClick={() => void reload()}>刷新余额并重新确认</Button> : undefined} />}
    <Form form={form} layout="vertical" onFinish={submit} disabled={busy || intent !== null}>
      <Form.Item name="delta" label="调整数量" rules={[{ required: true, message: '请输入非零整数' }, { validator: (_, value: unknown) => typeof value === 'number' && Number.isSafeInteger(value) && value !== 0 ? Promise.resolve() : Promise.reject(new Error('请输入非零整数')) }]}><InputNumber min={-2147483647} max={2147483647} precision={0} style={{ width: '100%' }} /></Form.Item>
      <Form.Item name="reason" label="操作原因" rules={[{ required: true, whitespace: true, message: '请输入原因' }]}><Input.TextArea maxLength={2000} rows={3} /></Form.Item>
    </Form>
    <Button type="primary" aria-label="确认额度调整" aria-busy={busy} danger={Boolean((intent?.delta ?? delta ?? 0) < 0)} loading={busy} disabled={busy || canRevise} onClick={() => { if (intent) void submit({ delta: intent.delta, reason: intent.reason }); else form.submit(); }}>确认额度调整</Button>
  </Modal>;
}
