import { useQuery } from '@tanstack/react-query';
import { Button, Card, Table, Tag, Typography } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { getInvoices, type Invoice } from '../api/billing';
import { formatCny, formatDateTime } from '../lib/format';
import { ErrorBlock, EmptyBlock, LoadingBlock } from '../components/PageState';

const STATUS_META: Record<string, { label: string; color: string }> = {
  paid: { label: '已支付', color: 'success' },
  pending: { label: '待支付', color: 'warning' },
  failed: { label: '支付失败', color: 'error' },
};

export default function InvoicesPage() {
  const { data, isLoading, isError, isFetching, refetch } = useQuery({
    queryKey: ['billing', 'invoices'],
    queryFn: getInvoices,
  });

  const rows = data ?? [];

  const columns: ColumnsType<Invoice> = [
    {
      title: '账单编号',
      dataIndex: 'id',
      key: 'id',
      width: 260,
      render: (v: string) => <Typography.Text code style={{ fontSize: 12 }}>{v}</Typography.Text>,
    },
    {
      title: '周期',
      dataIndex: 'period',
      key: 'period',
      width: 180,
      render: (v?: string) => v || '-',
    },
    {
      title: '金额',
      dataIndex: 'amount_cny',
      key: 'amount_cny',
      width: 140,
      align: 'right',
      render: (v?: number) => <span style={{ fontWeight: 600 }}>{formatCny(v)}</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 120,
      render: (s?: string) => {
        if (!s) return '-';
        const meta = STATUS_META[s] ?? { label: s, color: 'default' };
        return <Tag color={meta.color}>{meta.label}</Tag>;
      },
    },
    {
      title: '出账时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (v?: string) => formatDateTime(v),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          账单记录
        </Typography.Title>
        <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
          刷新
        </Button>
      </div>
      <Card style={{ borderRadius: 16 }}>
        {isLoading ? (
          <LoadingBlock rows={6} />
        ) : isError ? (
          <ErrorBlock description="账单加载失败" onRetry={() => void refetch()} />
        ) : rows.length === 0 ? (
          <EmptyBlock description="暂无账单，订阅套餐后每月出账" />
        ) : (
          <Table<Invoice>
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
