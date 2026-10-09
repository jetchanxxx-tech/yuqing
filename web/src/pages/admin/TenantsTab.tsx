import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Form, Input, Select, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminErrorMessage, adminStatusLabel, listTenants, type AdminQuery, type Tenant } from '../../api/admin';
import { getPlans } from '../../api/billing';
import { formatDateTime } from '../../lib/format';
import TenantDetail from './TenantDetail';

export default function TenantsTab() {
  const [form] = Form.useForm();
  const [query, setQuery] = useState<AdminQuery>({ page: 1, page_size: 20 });
  const [selected, setSelected] = useState<string | null>(null);
  const tenants = useQuery({ queryKey: ['admin', 'tenants', query], queryFn: ({ signal }) => listTenants(query, signal) });
  const plans = useQuery({ queryKey: ['billing', 'plans'], queryFn: getPlans });
  const planName = (code: string) => plans.data?.find((plan) => plan.code === code)?.name ?? code;
  const columns: ColumnsType<Tenant> = [
    { title: '租户 ID', dataIndex: 'id', width: 240, render: (id: string) => <Typography.Text code>{id}</Typography.Text> },
    { title: '租户名称', dataIndex: 'name' },
    { title: '历史套餐', dataIndex: 'plan_code', render: (code: string) => planName(code) },
    { title: '有效套餐', dataIndex: 'effective_plan_code', render: (code: string) => code ? <Tag>{planName(code)}</Tag> : '未知（按严格限制）' },
    { title: '套餐来源', dataIndex: 'plan_source' },
    { title: '状态', dataIndex: 'status', render: (value: string) => <Tag color={value === 'active' ? 'success' : 'warning'}>{adminStatusLabel(value)}</Tag> },
    { title: '成员数', dataIndex: 'user_count' },
    { title: '创建时间', dataIndex: 'created_at', render: formatDateTime },
    { title: '操作', fixed: 'right', width: 110, render: (_, tenant) => <Button size="small" onClick={() => setSelected(tenant.id)}>查看详情</Button> },
  ];
  return <>
    <Card>
      <Form form={form} layout="inline" onFinish={(values: Omit<AdminQuery, 'page' | 'page_size'>) => setQuery({ ...values, q: values.q?.trim(), page: 1, page_size: 20 })} style={{ gap: 12, marginBottom: 20 }}>
        <Form.Item name="q"><Input aria-label="搜索租户" placeholder="搜索租户名称或 ID" style={{ width: 260 }} allowClear /></Form.Item>
        <Form.Item name="status"><Select aria-label="租户状态筛选" placeholder="租户状态" style={{ width: 140 }} allowClear options={['provisioning', 'active', 'suspended', 'closed'].map((value) => ({ value, label: adminStatusLabel(value) }))} /></Form.Item>
        <Form.Item name="plan_code"><Select aria-label="租户套餐筛选" placeholder="历史套餐" style={{ width: 150 }} allowClear options={plans.data?.map((plan) => ({ value: plan.code, label: plan.name }))} /></Form.Item>
        <Button type="primary" htmlType="submit" autoInsertSpace={false}>搜索</Button>
        <Button onClick={() => { form.resetFields(); setQuery({ page: 1, page_size: 20 }); }}>重置</Button>
        <Button onClick={() => void tenants.refetch()} loading={tenants.isFetching}>刷新</Button>
      </Form>
      {tenants.isError && <Alert type="error" showIcon message={adminErrorMessage(tenants.error)} action={<Button onClick={() => void tenants.refetch()}>重试</Button>} style={{ marginBottom: 16 }} />}
      {plans.isError && <Alert type="warning" showIcon message="套餐名称暂时无法加载，使用服务器返回的套餐编码。" action={<Button onClick={() => void plans.refetch()}>重试</Button>} style={{ marginBottom: 16 }} />}
      <Table<Tenant> rowKey="id" columns={columns} dataSource={tenants.data?.items ?? []} loading={tenants.isLoading} scroll={{ x: 1300 }} locale={{ emptyText: tenants.isError ? '租户列表加载失败' : '暂无符合条件的租户' }} pagination={{ current: query.page, pageSize: 20, total: tenants.data?.total ?? 0, showSizeChanger: false, showTotal: (total) => `共 ${total} 条`, onChange: (page) => setQuery((previous) => ({ ...previous, page })) }} />
    </Card>
    {selected && <TenantDetail key={selected} tenantID={selected} onClose={() => setSelected(null)} />}
  </>;
}
