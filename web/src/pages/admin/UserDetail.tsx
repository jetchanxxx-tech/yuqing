import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Button, Descriptions, Form, Input, Modal, Select, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  adminErrorMessage, adminErrorStatus, adminStatusLabel, changeMemberRole, changePlatformRole,
  changeUserStatus, getAdminUser, type AuditEntry, type MemberRole, type Membership,
} from '../../api/admin';
import { useAuth } from '../../stores/auth';
import { formatDateTime } from '../../lib/format';
import { ErrorBlock, LoadingBlock } from '../../components/PageState';

export interface AdminAction {
  title: string;
  target: string;
  targetID: string;
  previous: string;
  next: string;
  expectedVersion: number;
  memberRole?: boolean;
  notice: string;
  submit: (reason: string, role: MemberRole) => Promise<void>;
}

export function AdminActionDialog({ action, onClose, onSuccess, onError }: {
  action: AdminAction; onClose: () => void; onSuccess: () => Promise<void>; onError: (error: unknown) => void;
}) {
  const [form] = Form.useForm();
  const selectedRole = Form.useWatch('role', form) as MemberRole | undefined;
  const { message } = App.useApp();
  const mutation = useMutation({
    mutationFn: (values: { reason: string; role?: MemberRole }) => action.submit(values.reason.trim(), values.role ?? action.previous as MemberRole),
    onSuccess: async () => {
      await onSuccess();
      message.success('操作已完成');
      onClose();
    },
    onError,
  });
  return <Modal open title={action.title} onCancel={() => { if (!mutation.isPending) onClose(); }} closable={!mutation.isPending} maskClosable={false} footer={[
    <Button key="cancel" disabled={mutation.isPending} onClick={onClose} autoInsertSpace={false}>取消</Button>,
    <Button key="confirm" type="primary" danger loading={mutation.isPending} onClick={() => form.submit()}>确认操作</Button>,
  ]}>
    <Descriptions column={1} size="small" items={[
      { key: 'target', label: '操作对象', children: action.target },
      { key: 'id', label: action.memberRole ? '目标团队 ID' : '对象 ID', children: <Typography.Text code>{action.targetID}</Typography.Text> },
      { key: 'change', label: '变更', children: <Typography.Text strong>{action.previous} → {action.memberRole ? selectedRole ?? action.previous : action.next}</Typography.Text> },
      { key: 'version', label: '当前版本', children: action.expectedVersion },
    ]} />
    <Typography.Paragraph type="secondary" style={{ marginTop: 16 }}>{action.notice}</Typography.Paragraph>
    <Form form={form} layout="vertical" initialValues={{ role: action.previous }} onFinish={(values: { reason: string; role?: MemberRole }) => mutation.mutate(values)}>
      {action.memberRole && <Form.Item name="role" label="新团队角色" rules={[{ required: true }]}>
        <Select options={[{ value: 'tenant_admin', label: '团队管理员（tenant_admin）' }, { value: 'analyst', label: '分析成员（analyst）' }, { value: 'viewer', label: '只读成员（viewer）' }]} />
      </Form.Item>}
      <Form.Item name="reason" label="操作原因" rules={[{ required: true, whitespace: true, message: '请输入操作原因' }]}>
        <Input.TextArea rows={3} maxLength={2000} showCount />
      </Form.Item>
    </Form>
  </Modal>;
}

export function MutationErrorAlert({ error, onRefresh }: { error: unknown; onRefresh: () => void }) {
  if (!error) return null;
  return <Alert type="error" showIcon message={adminErrorMessage(error)} style={{ marginBottom: 16 }} action={adminErrorStatus(error) === 409 ? <Button onClick={onRefresh}>刷新最新数据</Button> : undefined} />;
}

export function AuditTable({ entries }: { entries: AuditEntry[] }) {
  const columns: ColumnsType<AuditEntry> = [
    { title: '时间', dataIndex: 'created_at', render: formatDateTime, width: 175 },
    { title: '操作', dataIndex: 'action' },
    { title: '原因', dataIndex: 'reason' },
    { title: '操作者', dataIndex: 'actor_id' },
    { title: '变更前', dataIndex: 'before', render: (value: Record<string, unknown>) => <Typography.Text code>{JSON.stringify(value)}</Typography.Text> },
    { title: '变更后', dataIndex: 'after', render: (value: Record<string, unknown>) => <Typography.Text code>{JSON.stringify(value)}</Typography.Text> },
    { title: '请求 ID', dataIndex: 'request_id' },
  ];
  return <Table<AuditEntry> rowKey="id" size="small" dataSource={entries} columns={columns} scroll={{ x: 1100 }} pagination={{ pageSize: 5, hideOnSinglePage: true, showSizeChanger: false }} locale={{ emptyText: '暂无操作审计' }} />;
}

export default function UserDetail({ userID, onClose }: { userID: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const { reloadIdentity } = useAuth();
  const [action, setAction] = useState<AdminAction | null>(null);
  const [error, setError] = useState<unknown>(null);
  const detail = useQuery({ queryKey: ['admin', 'user', userID], queryFn: ({ signal }) => getAdminUser(userID, signal) });
  const user = detail.data;
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['admin'], refetchType: 'all' });
  };
  const success = async () => {
    setError(null);
    await Promise.all([refresh(), reloadIdentity()]);
  };
  const failed = (failure: unknown) => {
    setError(failure);
    setAction(null);
    if (adminErrorStatus(failure) === 409) void refresh();
    if (adminErrorStatus(failure) === 403) void reloadIdentity().catch(() => {});
  };
  const begin = (next: AdminAction) => { setError(null); setAction(next); };
  const statusAction = () => {
    if (!user || !['active', 'disabled'].includes(user.status)) return;
    const disable = user.status === 'active';
    begin({ title: disable ? '禁用账号' : '启用账号', target: `${user.name} / ${user.email}`, targetID: user.id,
      previous: adminStatusLabel(user.status), next: disable ? '已禁用' : '正常', expectedVersion: user.row_version,
      notice: '账号状态变更会撤销该账号现有登录凭据；启用后需重新登录。最后一名正常平台管理员受到保护。',
      submit: (reason) => changeUserStatus(user.id, disable ? 'disable' : 'enable', { reason, expected_version: user.row_version }),
    });
  };
  const platformAction = () => {
    if (!user) return;
    const revoke = user.platform_roles.includes('platform_admin');
    begin({ title: revoke ? '撤销平台管理员' : '授予平台管理员', target: `${user.name} / ${user.email}`, targetID: user.id,
      previous: revoke ? 'platform_admin' : '无平台角色', next: revoke ? '无平台角色' : 'platform_admin', expectedVersion: user.row_version,
      notice: '平台角色独立于团队角色。变更撤销目标账号的现有登录凭据；最后一名正常平台管理员受到保护。',
      submit: (reason) => changePlatformRole(user.id, !revoke, { reason, expected_version: user.row_version }),
    });
  };
  const memberAction = (member: Membership) => {
    if (!user) return;
    begin({ title: '修改团队角色', target: `${member.tenant_name} / ${user.email}（${user.id}）`, targetID: member.tenant_id,
      previous: member.role, next: member.role, expectedVersion: member.row_version, memberRole: true,
      notice: '仅修改此账号在指定团队的现有成员角色，其他团队保持原角色。变更会撤销目标账号现有凭据；最后一名团队管理员受到保护。',
      submit: (reason, role) => changeMemberRole(member.tenant_id, user.id, role, { reason, expected_version: member.row_version }),
    });
  };
  const memberships: ColumnsType<Membership> = [
    { title: '团队', dataIndex: 'tenant_name' },
    { title: '租户 ID', dataIndex: 'tenant_id', render: (id: string) => <Typography.Text code>{id}</Typography.Text> },
    { title: '租户状态', dataIndex: 'tenant_status', render: (status: string) => `租户${adminStatusLabel(status)}` },
    { title: '成员角色', dataIndex: 'role', render: (role: string) => <Tag>{role}</Tag> },
    { title: '版本', dataIndex: 'row_version' },
    { title: '操作', render: (_, member) => <Button size="small" onClick={() => memberAction(member)}>修改团队角色</Button> },
  ];
  return <>
    <Modal open title="用户详情" width={1100} onCancel={onClose} footer={<Button onClick={onClose} autoInsertSpace={false}>关闭</Button>}>
      <MutationErrorAlert error={error} onRefresh={() => void refresh()} />
      {detail.isLoading ? <LoadingBlock rows={8} /> : detail.isError ? <ErrorBlock description={adminErrorMessage(detail.error)} onRetry={() => void detail.refetch()} /> : user && <>
        <Descriptions bordered size="small" column={2} items={[
          { key: 'id', label: '用户 ID', children: <Typography.Text code>{user.id}</Typography.Text> },
          { key: 'name', label: '昵称', children: user.name },
          { key: 'email', label: '邮箱', children: user.email },
          { key: 'phone', label: '手机号', children: user.phone_masked || '未绑定' },
          { key: 'status', label: '账号状态', children: <Tag color={user.status === 'active' ? 'success' : 'warning'}>{adminStatusLabel(user.status)}</Tag> },
          { key: 'count', label: '关联租户数', children: user.tenant_count },
          { key: 'email_verified', label: '邮箱验证', children: user.email_verified ? '已验证' : '未验证' },
          { key: 'phone_verified', label: '手机验证', children: user.phone_verified ? '已验证' : '未验证' },
          { key: 'created', label: '创建时间', children: formatDateTime(user.created_at) },
          { key: 'login', label: '最后成功登录', children: formatDateTime(user.last_login_at) },
          { key: 'version', label: '账号版本', children: user.row_version },
        ]} />
        <Space style={{ marginTop: 16 }}>
          {['active', 'disabled'].includes(user.status) && <Button danger={user.status === 'active'} onClick={statusAction}>{user.status === 'active' ? '禁用账号' : '启用账号'}</Button>}
          <Button onClick={() => void refresh()} loading={detail.isFetching}>刷新详情</Button>
        </Space>
        <Typography.Title level={5}>平台角色</Typography.Title>
        <Space wrap>{user.platform_roles.length ? user.platform_roles.map((role) => <Tag key={role}>{role}</Tag>) : <Typography.Text type="secondary">无平台角色</Typography.Text>}
          <Button danger={user.platform_roles.includes('platform_admin')} onClick={platformAction}>{user.platform_roles.includes('platform_admin') ? '撤销平台管理员' : '授予平台管理员'}</Button>
        </Space>
        <Typography.Title level={5}>团队成员角色</Typography.Title>
        <Table<Membership> rowKey="tenant_id" columns={memberships} dataSource={user.memberships} size="small" pagination={false} scroll={{ x: 900 }} locale={{ emptyText: '暂无团队成员关系' }} />
        <Typography.Title level={5}>操作审计</Typography.Title>
        <AuditTable entries={user.audit_logs} />
      </>}
    </Modal>
    {action && <AdminActionDialog action={action} onClose={() => setAction(null)} onSuccess={success} onError={failed} />}
  </>;
}
