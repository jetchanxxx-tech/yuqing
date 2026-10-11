import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Descriptions, Form, Input, Modal, Select } from 'antd';
import { adminErrorMessage, adminErrorStatus, changeAdminPlan, getAdminPlans, getAdminTenant, planSourceLabel, type PlanChange } from '../../api/admin';

export default function PlanChangeDialog({ tenantID, tenantName, code, source, balance, version, onClose, onSuccess }: {
  tenantID: string; tenantName: string; code: string; source: string; balance: number; version: number;
  onClose: () => void; onSuccess: () => Promise<void>;
}) {
  const [form] = Form.useForm();
  const plans = useQuery({ queryKey: ['admin', 'plans'], queryFn: ({ signal }) => getAdminPlans(signal) });
  const [snapshot, setSnapshot] = useState({ code, source, balance, version });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [intent, setIntent] = useState<PlanChange | null>(null);
  const conflict = adminErrorStatus(error) === 409;
  const submit = async (values: { plan_code: string; reason: string }) => {
    const change = intent ?? { plan_code: values.plan_code, reason: values.reason.trim(), expected_version: snapshot.version };
    setIntent(change); setBusy(true); setError(null);
    try { await changeAdminPlan(tenantID, change); await onSuccess(); onClose(); }
    catch (failure) { setError(failure); }
    finally { setBusy(false); }
  };
  const reload = async () => {
    setBusy(true);
    try {
      const tenant = await getAdminTenant(tenantID);
      setSnapshot({ code: tenant.effective_plan_code, source: tenant.plan_source, balance: tenant.credit?.balance ?? 0, version: tenant.credit?.version ?? 0 });
      setIntent(null); setError(null);
    } catch (failure) { setError(failure); }
    finally { setBusy(false); }
  };
  return <Modal open title="修改套餐权益" footer={null} maskClosable={false} closable={!busy} onCancel={() => { if (!busy) onClose(); }}>
    <Descriptions column={1} size="small" items={[
      { key: 'tenant', label: '租户', children: tenantName },
      { key: 'code', label: '当前套餐', children: snapshot.code || '未知' },
      { key: 'source', label: '当前来源', children: planSourceLabel(snapshot.source) },
      { key: 'balance', label: '报告余额', children: snapshot.balance },
      { key: 'version', label: '权益版本', children: snapshot.version },
    ]} />
    <Alert type="info" showIcon message="人工调整现有套餐权益" description="报告余额保持当前值，操作原因写入审计。额度调整使用独立入口；购买与订阅周期以实际支付记录为准。" style={{ margin: '16px 0' }} />
    {plans.isError && <Alert type="error" message="套餐目录加载失败" action={<Button onClick={() => void plans.refetch()}>重新加载</Button>} style={{ marginBottom: 16 }} />}
    {error !== null && <Alert type="error" showIcon message={adminErrorMessage(error)} description={conflict ? '当前权益已变化。刷新后核对实际结果，再重新确认。' : '输入已保留，可重试；结果不确定时请先核对当前权益。'} action={conflict ? <Button disabled={busy} onClick={() => void reload()}>刷新权益并重新确认</Button> : undefined} style={{ marginBottom: 16 }} />}
    <Form form={form} layout="vertical" onFinish={submit} disabled={busy || intent !== null} initialValues={{ plan_code: code }}>
      <Form.Item name="plan_code" label="目标套餐" rules={[{ required: true, message: '请选择现有套餐' }]}><Select aria-label="目标套餐" loading={plans.isLoading} options={plans.data?.map(plan => ({ value: plan.code, label: `${plan.name}（${plan.code}）` }))} /></Form.Item>
      <Form.Item name="reason" label="操作原因" rules={[{ required: true, whitespace: true, message: '请输入操作原因' }]}><Input.TextArea maxLength={1000} rows={3} /></Form.Item>
    </Form>
    <Button type="primary" aria-label="确认权益变更" aria-busy={busy} loading={busy} disabled={busy || conflict || !plans.data} onClick={() => { if (intent) void submit({ plan_code: intent.plan_code, reason: intent.reason }); else form.submit(); }}>确认权益变更</Button>
  </Modal>;
}
