import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, App, Button, Card, Popconfirm, Result, Table, Tag, Typography } from 'antd';
import { ReloadOutlined, StopOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { listTenants, suspendTenant, type Tenant } from '../api/admin';
import { getPlans, type Plan } from '../api/billing';
import { useAuth } from '../stores/auth';
import { formatDateTime } from '../lib/format';
import { ErrorBlock, EmptyBlock, LoadingBlock } from '../components/PageState';

const ADMIN_ROLES = ['platform_admin', 'admin'];

export default function AdminPage() {
  const { message } = App.useApp();
  const { principal } = useAuth();
  const queryClient = useQueryClient();

  const isAdmin = !!principal && principal.roles.some((r) => ADMIN_ROLES.includes(r));

  const tenantsQ = useQuery({ queryKey: ['admin', 'tenants'], queryFn: listTenants, enabled: isAdmin });
  const plansQ = useQuery({ queryKey: ['billing', 'plans'], queryFn: getPlans, enabled: isAdmin });

  const suspendQ = useMutation({
    mutationFn: (id: string) => suspendTenant(id),
    onSuccess: () => {
      message.success('操作已提交');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'tenants'] });
    },
  });

  if (!principal) return null;
  if (!isAdmin) {
    return (
      <Card style={{ borderRadius: 16 }}>
        <Result status="403" title="无访问权限" subTitle="管理后台仅对平台管理员开放" />
      </Card>
    );
  }

  const planName = (code: string, plans: Plan[]) => plans.find((p) => p.code === code)?.name ?? code;

  const columns: ColumnsType<Tenant> = [
    {
      title: '租户',
      dataIndex: 'name',
      key: 'name',
      render: (name: string) => <Typography.Text strong>{name}</Typography.Text>,
    },
    { title: '租户 ID', dataIndex: 'id', key: 'id', width: 220, render: (v: string) => <Typography.Text code style={{ fontSize: 12 }}>{v}</Typography.Text> },
    {
      title: '套餐',
      dataIndex: 'plan_code',
      key: 'plan_code',
      width: 130,
      render: (code: string) => (plansQ.data ? <Tag>{planName(code, plansQ.data)}</Tag> : code),
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 110,
      render: (s: string) => (
        <Tag color={s === 'active' ? 'success' : s === 'suspended' ? 'warning' : 'default'}>
          {s === 'active' ? '正常' : s === 'suspended' ? '已挂起' : s}
        </Tag>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (v?: string) => formatDateTime(v),
    },
    {
      title: '操作',
      key: 'action',
      width: 120,
      render: (_, r) =>
        r.status === 'active' ? (
          <Popconfirm
            title="挂起该租户？"
            description="挂起后租户将无法登录与使用服务"
            okText="挂起"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={() => suspendQ.mutate(r.id)}
          >
            <Button size="small" danger icon={<StopOutlined />} loading={suspendQ.isPending && suspendQ.variables === r.id}>
              挂起
            </Button>
          </Popconfirm>
        ) : (
          <Typography.Text type="secondary">—</Typography.Text>
        ),
    },
  ];

  const rows = tenantsQ.data ?? [];
  const isLoading = tenantsQ.isLoading;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          管理后台
        </Typography.Title>
        <Button icon={<ReloadOutlined />} onClick={() => void tenantsQ.refetch()} loading={tenantsQ.isFetching}>
          刷新
        </Button>
      </div>
      {suspendQ.isError && (
        <Alert type="error" showIcon message="挂起操作失败" style={{ marginBottom: 16 }} />
      )}
      <Card style={{ borderRadius: 16 }}>
        {isLoading ? (
          <LoadingBlock rows={6} />
        ) : tenantsQ.isError ? (
          <ErrorBlock description="租户列表加载失败" onRetry={() => void tenantsQ.refetch()} />
        ) : rows.length === 0 ? (
          <EmptyBlock description="暂无租户" />
        ) : (
          <Table<Tenant>
            rowKey="id"
            columns={columns}
            dataSource={rows}
            pagination={{
              pageSize: 10,
              showTotal: (t) => `共 ${t} 条`,
              showSizeChanger: false,
            }}
          />
        )}
      </Card>
    </div>
  );
}
