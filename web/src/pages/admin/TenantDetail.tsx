import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Descriptions, Modal, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { adminErrorMessage, adminErrorStatus, adminStatusLabel, changeMemberRole, changeTenantStatus, getAdminTenant, type TenantMember } from '../../api/admin';
import type { Order } from '../../api/billing';
import { useAuth } from '../../stores/auth';
import { formatCents, useDateTime } from '../../lib/format';
import { ErrorBlock, LoadingBlock } from '../../components/PageState';
import CreditAdjustmentDialog from './CreditAdjustmentDialog';
import { AdminActionDialog, AuditTable, MutationErrorAlert, type AdminAction } from './UserDetail';

export default function TenantDetail({ tenantID, onClose }: { tenantID: string; onClose: () => void }) {
  const formatDateTime = useDateTime();
  const queryClient = useQueryClient();
  const { reloadIdentity } = useAuth();
  const [adjusting, setAdjusting] = useState(false);
  const [action, setAction] = useState<AdminAction | null>(null);
  const [error, setError] = useState<unknown>(null);
  const detail = useQuery({ queryKey: ['admin', 'tenant', tenantID], queryFn: ({ signal }) => getAdminTenant(tenantID, signal) });
  const tenant = detail.data;
  const refresh = async () => { await queryClient.invalidateQueries({ queryKey: ['admin'], refetchType: 'all' }); };
  const success = async () => { setError(null); await Promise.all([refresh(), reloadIdentity()]); };
  const failed = (failure: unknown) => {
    setError(failure);
    setAction(null);
    if (adminErrorStatus(failure) === 409) void refresh();
    if (adminErrorStatus(failure) === 403) void reloadIdentity().catch(() => {});
  };
  const statusAction = () => {
    if (!tenant || !['active', 'suspended'].includes(tenant.status)) return;
    const suspend = tenant.status === 'active';
    setError(null);
    setAction({ title: suspend ? '挂起租户' : '恢复租户', target: tenant.name, targetID: tenant.id,
      previous: adminStatusLabel(tenant.status), next: suspend ? '已挂起' : '正常', expectedVersion: tenant.row_version,
      notice: '租户挂起后暂停业务使用。账号状态、团队成员及资产保持原状态；平台管理员仍可恢复租户。',
      submit: (reason) => changeTenantStatus(tenant.id, suspend ? 'suspend' : 'resume', { reason, expected_version: tenant.row_version }),
    });
  };
  const memberAction = (member: TenantMember) => {
    if (!tenant) return;
    setError(null);
    setAction({ title: '修改团队角色', target: `${tenant.name} / ${member.email}（${member.user_id}）`, targetID: tenant.id,
      previous: member.role, next: member.role, expectedVersion: member.row_version, memberRole: true,
      notice: '仅修改此账号在指定团队的现有成员角色，其他团队保持原角色。变更会撤销目标账号现有凭据；最后一名团队管理员受到保护。',
      submit: (reason, role) => changeMemberRole(tenant.id, member.user_id, role, { reason, expected_version: member.row_version }),
    });
  };
  const members: ColumnsType<TenantMember> = [
    { title: '用户 ID', dataIndex: 'user_id', render: (id: string) => <Typography.Text code>{id}</Typography.Text> },
    { title: '昵称', dataIndex: 'name' },
    { title: '邮箱', dataIndex: 'email' },
    { title: '成员角色', dataIndex: 'role', render: (role: string) => <Tag>{role}</Tag> },
    { title: '版本', dataIndex: 'row_version' },
    { title: '操作', render: (_, member) => <Button size="small" onClick={() => memberAction(member)}>修改团队角色</Button> },
  ];
  const orders: ColumnsType<Order> = [
    { title: '订单 ID', dataIndex: 'id' }, { title: '商品编码', dataIndex: 'sku_code' },
    { title: '金额', dataIndex: 'amount_cents', render: (value: number) => formatCents(value) },
    { title: '额度', dataIndex: 'credits' }, { title: '状态', dataIndex: 'state' },
    { title: '支付渠道', dataIndex: 'channel' }, { title: '创建时间', dataIndex: 'created_at', render: formatDateTime },
    { title: '支付时间', dataIndex: 'paid_at', render: formatDateTime },
  ];
  return <>
    <Modal open title="租户详情" width={1100} onCancel={onClose} footer={<Button onClick={onClose} autoInsertSpace={false}>关闭</Button>}>
      <MutationErrorAlert error={error} onRefresh={() => void refresh()} />
      {detail.isLoading ? <LoadingBlock rows={8} /> : detail.isError ? <ErrorBlock description={adminErrorMessage(detail.error)} onRetry={() => void detail.refetch()} /> : tenant && <>
        <Descriptions bordered size="small" column={2} items={[
          { key: 'id', label: '租户 ID', children: <Typography.Text code>{tenant.id}</Typography.Text> },
          { key: 'name', label: '名称', children: tenant.name },
          { key: 'slug', label: '标识', children: tenant.slug },
          { key: 'status', label: '状态', children: <Tag color={tenant.status === 'active' ? 'success' : 'warning'}>{adminStatusLabel(tenant.status)}</Tag> },
          { key: 'plan', label: '历史套餐', children: tenant.plan_code },
          { key: 'effective', label: '有效套餐', children: tenant.effective_plan_code || '未知（按严格限制）' },
          { key: 'source', label: '套餐来源', children: tenant.plan_source },
          { key: 'credit', label: '报告剩余额度', children: tenant.credit ? tenant.credit.balance : '无额度池' },
          { key: 'credit_plan', label: '额度池套餐', children: tenant.credit?.plan_code ?? '未知' },
          { key: 'count', label: '成员数', children: tenant.user_count },
          { key: 'created', label: '创建时间', children: formatDateTime(tenant.created_at) },
          { key: 'version', label: '租户版本', children: tenant.row_version },
        ]} />
        <Space style={{ marginTop: 16 }}>
          <Button onClick={() => setAdjusting(true)}>调整报告额度</Button>
          {['active', 'suspended'].includes(tenant.status) && <Button danger={tenant.status === 'active'} onClick={statusAction}>{tenant.status === 'active' ? '挂起租户' : '恢复租户'}</Button>}
          <Button onClick={() => void refresh()} loading={detail.isFetching}>刷新详情</Button>
        </Space>
        <Typography.Title level={5}>现有团队成员</Typography.Title>
        <Table<TenantMember> rowKey="user_id" columns={members} dataSource={tenant.members} size="small" pagination={false} scroll={{ x: 1000 }} locale={{ emptyText: '暂无团队成员' }} />
        <Typography.Title level={5}>报告额度流水</Typography.Title>
        <Table rowKey="id" size="small" dataSource={tenant.credit_transactions ?? []} scroll={{ x: 1100 }} pagination={{ pageSize: 5, hideOnSinglePage: true }} columns={[
          { title: '时间', dataIndex: 'created_at', render: formatDateTime },
          { title: '变动', dataIndex: 'delta' }, { title: '变动后余额', dataIndex: 'balance_after' },
          { title: '类型', dataIndex: 'reason' }, { title: '原因', dataIndex: 'reason_detail' },
          { title: '操作人', dataIndex: 'actor_id' }, { title: '操作编号', dataIndex: 'idempotency_key' },
        ]} locale={{ emptyText: '暂无额度流水' }} />
        <Typography.Title level={5}>历史订单</Typography.Title>
        <Table<Order> rowKey="id" columns={orders} dataSource={tenant.orders} size="small" pagination={{ pageSize: 5, hideOnSinglePage: true, showSizeChanger: false }} scroll={{ x: 1100 }} locale={{ emptyText: '暂无订单记录' }} />
        <Typography.Title level={5}>操作审计</Typography.Title>
        <AuditTable entries={tenant.audit_logs} />
      </>}
    </Modal>
    {adjusting && tenant && <CreditAdjustmentDialog tenantID={tenant.id} tenantName={tenant.name} balance={tenant.credit?.balance ?? 0} version={tenant.credit?.version ?? 0} onClose={() => setAdjusting(false)} onSuccess={refresh} />}
    {action && <AdminActionDialog action={action} onClose={() => setAction(null)} onSuccess={success} onError={failed} />}
  </>;
}
