import { useMemo } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { App, Button, Card, Popconfirm, Progress, Space, Table, Tag, Typography } from 'antd';
import { Link } from 'react-router-dom';
import type { ColumnsType } from 'antd/es/table';
import { ReloadOutlined } from '@ant-design/icons';
import { cancelAnalysis, listAnalyses, type AnalysisSummary } from '../api/analyses';
import {
  ANALYSIS_STATE_LABELS,
  ANALYSIS_STATE_TAG_COLORS,
  CANCELLABLE_STATES,
  ANALYSIS_TYPES,
  type AnalysisState,
} from '../lib/constants';
import { formatDateTime } from '../lib/format';
import { ErrorBlock, EmptyBlock, LoadingBlock } from '../components/PageState';

const PAGE_SIZE = 10;

export default function AnalysisListPage() {
  const { message } = App.useApp();
  const queryClient = useQueryClient();

  const { data, isLoading, isError, isFetching, refetch } = useQuery({
    queryKey: ['analyses', 'list'],
    queryFn: listAnalyses,
    // 有运行中的任务时自动刷新，全部进入终态后停止
    refetchInterval: (query) => {
      const list = query.state.data?.analyses;
      return list && list.some((a) => CANCELLABLE_STATES.includes(a.state)) ? 5000 : false;
    },
  });

  const cancelQ = useMutation({
    mutationFn: (id: string) => cancelAnalysis(id),
    onSuccess: (_d, id) => {
      message.success('已发送取消请求');
      void queryClient.invalidateQueries({ queryKey: ['analyses'] });
      void queryClient.invalidateQueries({ queryKey: ['analysis', 'detail', id] });
    },
  });

  const rows = data?.analyses ?? [];

  const columns = useMemo<ColumnsType<AnalysisSummary>>(
    () => [
      {
        title: '名称',
        dataIndex: 'name',
        key: 'name',
        render: (name: string, r) => <Link to={`/analyses/${r.id}`}>{name}</Link>,
      },
      {
        title: '类型',
        dataIndex: 'analysis_type',
        key: 'analysis_type',
        width: 140,
        render: (v: string) => {
          const meta = ANALYSIS_TYPES.find((t) => t.value === v);
          return meta ? meta.label : v || '-';
        },
      },
      {
        title: '状态',
        dataIndex: 'state',
        key: 'state',
        width: 130,
        render: (s: AnalysisState) => (
          <Tag color={ANALYSIS_STATE_TAG_COLORS[s] ?? 'default'}>{ANALYSIS_STATE_LABELS[s] ?? s}</Tag>
        ),
      },
      {
        title: '进度',
        dataIndex: 'progress',
        key: 'progress',
        width: 180,
        render: (p: number | undefined, r) =>
          CANCELLABLE_STATES.includes(r.state) ? (
            <Progress percent={Math.max(0, Math.min(100, p ?? 0))} size="small" strokeColor="#FF2442" />
          ) : p && p > 0 ? (
            <Progress percent={Math.max(0, Math.min(100, p))} size="small" />
          ) : (
            <Typography.Text type="secondary">—</Typography.Text>
          ),
      },
      {
        title: '创建时间',
        dataIndex: 'created_at',
        key: 'created_at',
        width: 180,
        render: (v: string) => formatDateTime(v),
      },
      {
        title: '操作',
        key: 'action',
        width: 180,
        render: (_, r) => (
          <Space size={4}>
            <Link to={`/analyses/${r.id}`}>
              <Button size="small">查看</Button>
            </Link>
            {CANCELLABLE_STATES.includes(r.state) && (
              <Popconfirm
                title="取消这次分析？"
                description="执行中的采集与研判将被终止"
                okText="取消任务"
                cancelText="再想想"
                onConfirm={() => cancelQ.mutate(r.id)}
              >
                <Button size="small" danger loading={cancelQ.isPending && cancelQ.variables === r.id}>
                  取消
                </Button>
              </Popconfirm>
            )}
          </Space>
        ),
      },
    ],
    [cancelQ]
  );

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Typography.Title level={4} style={{ margin: 0 }}>
          分析任务
        </Typography.Title>
        <Space>
          <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
            刷新
          </Button>
          <Link to="/analyses/new">
            <Button type="primary">新建分析</Button>
          </Link>
        </Space>
      </div>
      <Card style={{ borderRadius: 16 }}>
        {isLoading ? (
          <LoadingBlock rows={6} />
        ) : isError ? (
          <ErrorBlock description="任务列表加载失败" onRetry={() => void refetch()} />
        ) : rows.length === 0 ? (
          <EmptyBlock description="还没有分析任务，点击右上角「新建分析」开始监测" />
        ) : (
          <Table<AnalysisSummary>
            rowKey="id"
            columns={columns}
            dataSource={rows}
            pagination={{
              pageSize: PAGE_SIZE,
              showTotal: (t) => `共 ${t} 条`,
              showSizeChanger: false,
            }}
            loading={isFetching && !isLoading}
          />
        )}
      </Card>
    </div>
  );
}
