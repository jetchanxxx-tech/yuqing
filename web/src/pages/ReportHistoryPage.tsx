import { useQuery } from '@tanstack/react-query';
import { App, Button, Card, Dropdown, Space, Table, Tag, Tooltip, Typography } from 'antd';
import { DownloadOutlined, FileTextOutlined, ReloadOutlined } from '@ant-design/icons';
import { Link } from 'react-router-dom';
import type { ColumnsType } from 'antd/es/table';
import { listReports, openReportDownload, type Report } from '../api/reports';
import { REPORT_FORMATS, type ReportFormat } from '../lib/constants';
import { formatDateTime } from '../lib/format';
import { ErrorBlock, EmptyBlock, LoadingBlock } from '../components/PageState';

const STATUS_META: Record<string, { label: string; color: string }> = {
  completed: { label: '已生成', color: 'success' },
  processing: { label: '生成中', color: 'processing' },
  queued: { label: '排队中', color: 'default' },
  failed: { label: '失败', color: 'error' },
};

export default function ReportHistoryPage() {
  const { message } = App.useApp();
  const { data, isLoading, isError, isFetching, refetch } = useQuery({
    queryKey: ['reports', 'list'],
    queryFn: listReports,
  });

  const rows = data?.reports ?? [];

  const handleDownload = async (report: Report, format: ReportFormat) => {
    try {
      await openReportDownload(report.id, format);
    } catch {
      message.error('下载失败，请稍后重试');
    }
  };

  const columns: ColumnsType<Report> = [
    {
      title: '报告标题',
      dataIndex: 'title',
      key: 'title',
      render: (title: string, r) => (
        <Space>
          <FileTextOutlined style={{ color: '#FF2442' }} />
          <Link to={`/analyses/${r.analysis_id}`}>{title || '洞察报告'}</Link>
        </Space>
      ),
    },
    {
      title: '关联分析',
      dataIndex: 'analysis_id',
      key: 'analysis_id',
      width: 220,
      render: (aid: string) => (
        <Typography.Text code style={{ fontSize: 12 }}>
          {aid}
        </Typography.Text>
      ),
    },
    {
      title: '格式',
      dataIndex: 'format',
      key: 'format',
      width: 130,
      render: (fmt: string) => {
        const meta = REPORT_FORMATS.find((f) => f.value === fmt);
        return <Tag>{meta ? meta.label : fmt.toUpperCase()}</Tag>;
      },
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      width: 120,
      render: (s: string) => {
        const meta = STATUS_META[s] ?? { label: s, color: 'default' };
        return <Tag color={meta.color}>{meta.label}</Tag>;
      },
    },
    {
      title: '生成时间',
      dataIndex: 'created_at',
      key: 'created_at',
      width: 180,
      render: (v?: string) => formatDateTime(v),
    },
    {
      title: '操作',
      key: 'action',
      width: 160,
      render: (_, r) =>
        r.status === 'completed' ? (
          <Dropdown
            menu={{
              items: REPORT_FORMATS.map((f) => ({ key: f.value, label: `下载 ${f.label}` })),
              onClick: ({ key }) => void handleDownload(r, key as ReportFormat),
            }}
          >
            <Button size="small" icon={<DownloadOutlined />}>
              下载
            </Button>
          </Dropdown>
        ) : (
          <Tooltip
            title={
              r.status === 'processing' || r.status === 'queued'
                ? '报告生成中，请稍后刷新'
                : '报告暂不可下载'
            }
          >
            <Button size="small" icon={<DownloadOutlined />} disabled>
              下载
            </Button>
          </Tooltip>
        ),
    },
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          报告中心
        </Typography.Title>
        <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
          刷新
        </Button>
      </div>
      <Card style={{ borderRadius: 16 }}>
        {isLoading ? (
          <LoadingBlock rows={6} />
        ) : isError ? (
          <ErrorBlock description="报告列表加载失败" onRetry={() => void refetch()} />
        ) : rows.length === 0 ? (
          <EmptyBlock description="暂无报告，完成一次分析后自动生成" />
        ) : (
          <Table<Report>
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
