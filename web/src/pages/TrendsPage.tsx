import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  Alert, Button, Card, Space, Table, Tabs, Tooltip, Typography,
} from 'antd';
import {
  FireOutlined, ReloadOutlined, RocketOutlined,
} from '@ant-design/icons';
import { Link } from 'react-router-dom';
import { getTrends, type TrendItem, type TrendPlatform } from '../api/trends';
import { formatDateTime } from '../lib/format';
import { LoadingBlock } from '../components/PageState';

const STATUS_TAG: Record<string, { color: string; text: string }> = {
  ok: { color: '#02b940', text: '实时' },
  stale: { color: '#ff7d03', text: '数据延迟' },
  error: { color: '#9ca3af', text: '暂不可用' },
};

/**
 * F21 热榜聚合页：多平台热榜快照（纯展示，零存储零计费）。
 * 服务端 5 分钟共享缓存；本页条目过了就过了，不保留历史。
 */
export default function TrendsPage() {
  // refetchInterval 90s：服务端 5min 缓存，前端轻轮询只为多 Tab 场景保鲜
  const trendsQ = useQuery({
    queryKey: ['trends'],
    queryFn: getTrends,
    refetchInterval: 90_000,
  });

  const platforms = trendsQ.data ?? [];
  const [activeKey, setActiveKey] = useState<string>('');
  const active = platforms.find((p) => p.name === activeKey) ?? platforms[0];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div>
          <Typography.Title level={4} style={{ margin: 0 }}>
            <FireOutlined style={{ color: '#FF2442', marginRight: 8 }} />热榜
          </Typography.Title>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            实时快照，不保留历史 · 每 5 分钟自动更新
          </Typography.Text>
        </div>
        <Button
          icon={<ReloadOutlined />}
          onClick={() => void trendsQ.refetch()}
          loading={trendsQ.isFetching}
        >
          刷新
        </Button>
      </div>

      {trendsQ.isLoading && (
        <Card style={{ borderRadius: 16 }}>
          <LoadingBlock rows={10} />
        </Card>
      )}

      {trendsQ.isError && (
        <Alert
          type="warning"
          showIcon
          message="热榜服务暂不可用"
          description="数据源连接失败，请稍后重试。这不是没有事件发生，而是我们暂时抓不到。"
          action={<Button onClick={() => void trendsQ.refetch()}>重试</Button>}
        />
      )}

      {platforms.length > 0 && (
        <Card style={{ borderRadius: 16 }} styles={{ body: { paddingTop: 8 } }}>
          <Tabs
            activeKey={active?.name}
            onChange={setActiveKey}
            items={platforms.map((p) => ({
              key: p.name,
              label: (
                <Space size={6}>
                  {p.name}
                  <span style={{
                    width: 7, height: 7, borderRadius: '50%', display: 'inline-block',
                    background: STATUS_TAG[p.status]?.color ?? '#9ca3af',
                  }} />
                </Space>
              ),
              children: <PlatformFeed platform={p} />,
            }))}
          />
        </Card>
      )}
    </div>
  );
}

function PlatformFeed({ platform }: { platform: TrendPlatform }) {
  if (platform.status === 'error') {
    return (
      <Alert
        type="info"
        showIcon
        style={{ margin: 16, borderRadius: 12 }}
        message={`${platform.name}数据源暂不可用`}
        description="该平台数据源暂时无法访问。这不是没有事件发生，而是我们暂时抓不到。"
      />
    );
  }

  const isStale = platform.status === 'stale';

  return (
    <div>
      {isStale && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12, borderRadius: 12 }}
          message="该平台数据有延迟，以下为最近一次成功获取的快照"
        />
      )}
      <div style={{ marginBottom: 8 }}>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          更新于 {platform.updated_at ? formatDateTime(platform.updated_at) : '-'} · {platform.items.length} 条
        </Typography.Text>
      </div>
      <Table<TrendItem>
        size="middle"
        rowKey="rank"
        dataSource={platform.items}
        pagination={false}
        locale={{ emptyText: '暂无数据' }}
        columns={[
          {
            title: '#',
            dataIndex: 'rank',
            width: 56,
            render: (rank: number) => (
              <span style={{
                fontWeight: 700,
                fontVariantNumeric: 'tabular-nums',
                color: rank <= 3 ? '#FF2442' : 'rgba(0,0,0,0.45)',
              }}>
                {rank}
              </span>
            ),
          },
          {
            title: '标题',
            dataIndex: 'title',
            render: (title: string, item: TrendItem) => (
              <a href={item.url} target="_blank" rel="noreferrer" style={{ fontWeight: item.rank <= 3 ? 600 : 400 }}>
                {title}
              </a>
            ),
          },
          {
            title: '热度',
            dataIndex: 'hot',
            width: 120,
            render: (hot?: string) =>
              hot ? (
                <span style={{ color: '#FF2442', fontVariantNumeric: 'tabular-nums', fontSize: 13 }}>
                  {hot}
                </span>
              ) : (
                <span style={{ color: 'rgba(0,0,0,0.3)' }}>-</span>
              ),
          },
          {
            title: '',
            key: 'action',
            width: 90,
            render: (_: unknown, item: TrendItem) => (
              <Tooltip title="用该关键词发起深度分析（消耗 1 次报告额度）">
                <Link to={`/analyses/new?keywords=${encodeURIComponent(item.title)}`}>
                  <Button size="small" icon={<RocketOutlined />}>分析</Button>
                </Link>
              </Tooltip>
            ),
          },
        ]}
      />
    </div>
  );
}
