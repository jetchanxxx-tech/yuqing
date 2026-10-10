import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, App, Button, Card, Form, Input, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminErrorMessage, adminStatusLabel, listUsers, type AdminQuery, type AdminUser } from '../../api/admin';
import { useAuth } from '../../stores/auth';
import { useDateTime, calendarDateRange, DEFAULT_TIMEZONE } from '../../lib/format';
import UserDetail from './UserDetail';

export default function UsersTab() {
  const formatDateTime = useDateTime();
  const { principal } = useAuth();
  const { message } = App.useApp();
  const timezone = principal?.timezone || DEFAULT_TIMEZONE;
  const [form] = Form.useForm();
  const [query, setQuery] = useState<AdminQuery>({ page: 1, page_size: 20 });
  const [selected, setSelected] = useState<string | null>(null);
  const users = useQuery({
    queryKey: ['admin', 'users', query], queryFn: ({ signal }) => listUsers(query, signal),
  });
  const columns: ColumnsType<AdminUser> = [
    { title: '用户 ID', dataIndex: 'id', width: 240, render: (id: string) => <Typography.Text code>{id}</Typography.Text> },
    { title: '昵称', dataIndex: 'name' },
    { title: '邮箱', dataIndex: 'email' },
    { title: '手机号', dataIndex: 'phone_masked', render: (value: string) => value || '未绑定' },
    { title: '账号状态', dataIndex: 'status', render: (value: string) => <Tag color={value === 'active' ? 'success' : 'warning'}>{adminStatusLabel(value)}</Tag> },
    { title: '验证状态', key: 'verified', render: (_, user) => <Space direction="vertical" size={0}><span>邮箱：{user.email_verified ? '已验证' : '未验证'}</span><span>手机：{user.phone_verified ? '已验证' : '未验证'}</span></Space> },
    { title: '平台角色', dataIndex: 'platform_roles', render: (roles: string[]) => roles.length ? roles.map((role) => <Tag key={role}>{role}</Tag>) : '无' },
    { title: '关联租户数', dataIndex: 'tenant_count' },
    { title: '创建时间', dataIndex: 'created_at', render: formatDateTime },
    { title: '最后登录', dataIndex: 'last_login_at', render: formatDateTime },
    { title: '操作', fixed: 'right', width: 110, render: (_, user) => <Button size="small" onClick={() => setSelected(user.id)}>查看详情</Button> },
  ];
  return <>
    <Card>
      <Form form={form} layout="inline" onFinish={(values: Omit<AdminQuery, 'page' | 'page_size'> & { from_day?: string; through_day?: string }) => {
        const { from_day, through_day, ...filters } = values;
        try { setQuery({ ...filters, ...calendarDateRange(from_day, through_day, timezone), q: values.q?.trim(), page: 1, page_size: 20 }); }
        catch (error) { message.error(error instanceof Error ? error.message : '日期范围无效'); }
      }} style={{ gap: 12, marginBottom: 20 }}>
        <Form.Item name="q"><Input aria-label="搜索用户" placeholder="搜索邮箱、昵称或用户 ID" style={{ width: 260 }} allowClear /></Form.Item>
        <Form.Item name="status"><Select aria-label="账号状态筛选" placeholder="账号状态" allowClear style={{ width: 145 }} options={['active', 'disabled', 'pending_activation', 'closure_pending', 'closed'].map((value) => ({ value, label: adminStatusLabel(value) }))} /></Form.Item>
        <Form.Item name="platform_role"><Select aria-label="平台角色筛选" placeholder="平台角色" allowClear style={{ width: 160 }} options={[{ value: 'platform_admin', label: '平台管理员' }]} /></Form.Item>
        <Form.Item name="verified"><Select aria-label="验证状态筛选" placeholder="验证状态" allowClear style={{ width: 150 }} options={[{ value: 'email', label: '邮箱已验证' }, { value: 'phone', label: '手机已验证' }, { value: 'none', label: '均未验证' }]} /></Form.Item>
        <Form.Item name="from_day"><Input type="date" aria-label="创建开始日期" /></Form.Item>
        <Form.Item name="through_day"><Input type="date" aria-label="创建结束日期" /></Form.Item>
        <Typography.Text type="secondary">创建日期（{timezone}，含结束日）</Typography.Text>
        <Button type="primary" htmlType="submit" autoInsertSpace={false}>搜索</Button>
        <Button onClick={() => { form.resetFields(); setQuery({ page: 1, page_size: 20 }); }}>重置</Button>
        <Button onClick={() => void users.refetch()} loading={users.isFetching}>刷新</Button>
      </Form>
      {users.isError && <Alert type="error" showIcon message={adminErrorMessage(users.error)} action={<Button onClick={() => void users.refetch()}>重试</Button>} style={{ marginBottom: 16 }} />}
      <Table<AdminUser> rowKey="id" columns={columns} dataSource={users.data?.items ?? []} loading={users.isLoading} scroll={{ x: 1700 }} locale={{ emptyText: users.isError ? '用户列表加载失败' : '暂无符合条件的用户' }} pagination={{ current: query.page, pageSize: 20, total: users.data?.total ?? 0, showSizeChanger: false, showTotal: (total) => `共 ${total} 条`, onChange: (page) => setQuery((previous) => ({ ...previous, page })) }} />
    </Card>
    {selected && <UserDetail key={selected} userID={selected} onClose={() => setSelected(null)} />}
  </>;
}
