import type { ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Button, Card, Col, Empty, List, Row, Statistic, Tag, Typography } from 'antd';
import { ArrowDownOutlined, ArrowUpOutlined, ReloadOutlined, MinusOutlined } from '@ant-design/icons';
import type { EChartsOption } from 'echarts';
import { Link } from 'react-router-dom';
import EChart from '../components/EChart';
import { ErrorBlock, LoadingBlock } from '../components/PageState';
import { getOverview, getTrend, getSources, getTopics } from '../api/dashboard';
import { sourceLabel, trendArrow } from '../lib/constants';
import { formatDateTime, formatNum, formatPercent } from '../lib/format';
import type { Topic } from '../api/analyses';

const BRAND = '#FF2442';
const AXIS_LABEL = 'rgba(0, 0, 0, 0.45)';
const SPLIT_LINE = 'rgba(0, 0, 0, 0.06)';

function baseTooltip(): EChartsOption['tooltip'] {
  return { trigger: 'axis', backgroundColor: 'rgba(255,255,255,0.96)', borderColor: 'rgba(0,0,0,0.08)' };
}

const trendMeta: Record<string, { icon: ReactNode; label: string; color: string }> = {
  up: { icon: <ArrowUpOutlined />, label: '上升', color: '#ff7d03' },
  down: { icon: <ArrowDownOutlined />, label: '下降', color: '#02b940' },
  flat: { icon: <MinusOutlined />, label: '平稳', color: 'default' },
};

function TrendTag({ trend }: { trend?: string }) {
  const t = trendMeta[trendArrow(trend)];
  return (
    <Tag color={t.color} style={{ marginInlineEnd: 0 }}>
      {t.icon} {t.label}
    </Tag>
  );
}

export default function DashboardPage() {
  const overviewQ = useQuery({ queryKey: ['dashboard', 'overview'], queryFn: getOverview, refetchInterval: 60_000 });
  const trendQ = useQuery({ queryKey: ['dashboard', 'trend'], queryFn: getTrend });
  const sourcesQ = useQuery({ queryKey: ['dashboard', 'sources'], queryFn: getSources });
  const topicsQ = useQuery({ queryKey: ['dashboard', 'topics'], queryFn: getTopics });

  const refreshing = overviewQ.isFetching || trendQ.isFetching || sourcesQ.isFetching || topicsQ.isFetching;

  const refreshAll = () => {
    void overviewQ.refetch();
    void trendQ.refetch();
    void sourcesQ.refetch();
    void topicsQ.refetch();
  };

  // —— 趋势折线（左轴声量 + 右轴情感指数）——
  const trendOption: EChartsOption = trendQ.data
    ? {
        tooltip: baseTooltip(),
        legend: {
          data: ['声量', '情感指数'],
          icon: 'circle',
          itemWidth: 8,
          itemHeight: 8,
          top: 0,
          right: 0,
          textStyle: { color: 'rgba(0,0,0,0.62)' },
        },
        grid: { left: 8, right: 8, top: 36, bottom: 0, containLabel: true },
        xAxis: {
          type: 'category',
          boundaryGap: false,
          data: trendQ.data.dates,
          axisLine: { lineStyle: { color: 'rgba(0,0,0,0.08)' } },
          axisTick: { show: false },
          axisLabel: { color: AXIS_LABEL },
        },
        yAxis: [
          {
            type: 'value',
            splitLine: { lineStyle: { color: SPLIT_LINE } },
            axisLabel: { color: AXIS_LABEL },
          },
          {
            type: 'value',
            min: 0,
            splitLine: { show: false },
            axisLabel: { color: AXIS_LABEL },
          },
        ],
        series: [
          {
            name: '声量',
            type: 'line',
            smooth: true,
            symbol: 'none',
            data: trendQ.data.counts,
            lineStyle: { width: 2.5, color: BRAND },
            areaStyle: {
              color: {
                type: 'linear',
                x: 0,
                y: 0,
                x2: 0,
                y2: 1,
                colorStops: [
                  { offset: 0, color: 'rgba(255,36,66,0.12)' },
                  { offset: 1, color: 'rgba(255,36,66,0.01)' },
                ],
              },
            },
          },
          {
            name: '情感指数',
            type: 'line',
            yAxisIndex: 1,
            smooth: true,
            symbol: 'none',
            data: trendQ.data.scores,
            lineStyle: { width: 2, color: '#02b940' },
          },
        ],
      }
    : {};

  // —— 来源横向柱状图（按数量降序，顶部为最多） ——
  const sortedSources = [...(sourcesQ.data?.sources ?? [])].sort((a, b) => a.count - b.count);
  const sourcesOption: EChartsOption =
    sortedSources.length > 0
      ? {
          tooltip: {
            ...baseTooltip(),
            trigger: 'item',
            formatter: (p: unknown) => {
              const idx = (p as { dataIndex: number }).dataIndex;
              const row = sortedSources[idx];
              return `${sourceLabel(row.name)}<br/>声量：<b>${formatNum(row.count)}</b>（${row.pct ?? 0}%）`;
            },
          },
          grid: { left: 8, right: 24, top: 8, bottom: 0, containLabel: true },
          xAxis: {
            type: 'value',
            splitLine: { lineStyle: { color: SPLIT_LINE } },
            axisLabel: { color: AXIS_LABEL },
          },
          yAxis: {
            type: 'category',
            data: sortedSources.map((s) => sourceLabel(s.name)),
            axisLine: { lineStyle: { color: 'rgba(0,0,0,0.08)' } },
            axisTick: { show: false },
            axisLabel: { color: 'rgba(0,0,0,0.8)' },
          },
          series: [
            {
              type: 'bar',
              data: sortedSources.map((s) => s.count),
              barWidth: 14,
              itemStyle: { color: BRAND, borderRadius: [0, 7, 7, 0] },
              label: { show: true, position: 'right', color: AXIS_LABEL },
            },
          ],
        }
      : {};

  // —— 情感占比环形图 ——
  const ov = overviewQ.data;
  const sentimentData = ov
    ? [
        { name: '正面', value: ov.sentiment_pos },
        { name: '中性', value: ov.sentiment_neu },
        { name: '负面', value: ov.sentiment_neg },
      ]
    : [];
  const sentimentOption: EChartsOption = {
    tooltip: { ...baseTooltip(), trigger: 'item', formatter: '{b}：{c}%（{d}%）' },
    legend: {
      bottom: 0,
      icon: 'circle',
      itemWidth: 8,
      itemHeight: 8,
      textStyle: { color: 'rgba(0,0,0,0.62)' },
    },
    color: ['#02b940', '#9ca3af', '#FF2442'],
    series: [
      {
        type: 'pie',
        radius: ['58%', '78%'],
        center: ['50%', '44%'],
        avoidLabelOverlap: true,
        itemStyle: { borderColor: '#fff', borderWidth: 2 },
        label: { show: true, formatter: '{b}\n{d}%', color: 'rgba(0,0,0,0.62)', fontSize: 12 },
        labelLine: { length: 8, length2: 8 },
        data: sentimentData,
      },
    ],
  };

  const topics = topicsQ.data?.topics ?? [];
  const hasTopics = topics.length > 0;
  const trendEmpty = !trendQ.data || trendQ.data.dates.length === 0;

  return (
    <div>
      <Row justify="space-between" align="middle" style={{ marginBottom: 16 }}>
        <Col>
          <Typography.Title level={4} style={{ margin: 0 }}>
            数据面板
          </Typography.Title>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {overviewQ.dataUpdatedAt ? `更新于 ${formatDateTime(new Date(overviewQ.dataUpdatedAt).toISOString())}` : '监测概况一览'}
          </Typography.Text>
        </Col>
        <Col>
          <Button onClick={refreshAll} loading={refreshing} icon={<ReloadOutlined />}>
            刷新
          </Button>
        </Col>
      </Row>

      {/* —— 概览指标 —— */}
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        {overviewQ.isError && !ov ? (
          <Col span={24}>
            <Card>
              <ErrorBlock description="概览数据加载失败" onRetry={() => void overviewQ.refetch()} />
            </Card>
          </Col>
        ) : (
          <>
            <Col xs={24} sm={12} lg={6}>
              <Card>
                {ov ? (
                  <Statistic
                    title="累计分析"
                    value={ov.total_analyses}
                    valueStyle={{ fontWeight: 600 }}
                  />
                ) : (
                  <LoadingBlock rows={2} />
                )}
                {ov ? (
                  <div style={{ marginTop: 8, fontSize: 13, color: 'rgba(0,0,0,0.62)' }}>
                    {ov.active_tasks > 0 ? (
                      <Link to="/analyses">
                        <Tag color="processing">{ov.active_tasks} 个任务运行中</Tag>
                      </Link>
                    ) : (
                      <Typography.Text type="secondary">当前无运行中任务</Typography.Text>
                    )}
                  </div>
                ) : null}
              </Card>
            </Col>
            <Col xs={24} sm={12} lg={6}>
              <Card>
                {ov ? (
                  <Statistic title="采集文档" value={ov.total_docs} valueStyle={{ fontWeight: 600 }} />
                ) : (
                  <LoadingBlock rows={2} />
                )}
                {ov ? (
                  <Typography.Paragraph type="secondary" style={{ fontSize: 13, margin: '8px 0 0' }}>
                    全部监测任务累计
                  </Typography.Paragraph>
                ) : null}
              </Card>
            </Col>
            <Col xs={24} sm={12} lg={6}>
              <Card>
                {ov ? (
                  <Statistic
                    title="正面情绪占比"
                    value={ov.sentiment_pos}
                    precision={1}
                    suffix="%"
                    valueStyle={{ color: '#02b940', fontWeight: 600 }}
                  />
                ) : (
                  <LoadingBlock rows={2} />
                )}
                {ov ? (
                  <Typography.Paragraph type="secondary" style={{ fontSize: 13, margin: '8px 0 0' }}>
                    负面 {formatPercent(ov.sentiment_neg)} · 中性 {formatPercent(ov.sentiment_neu)}
                  </Typography.Paragraph>
                ) : null}
              </Card>
            </Col>
            <Col xs={24} sm={12} lg={6}>
              <Card>
                {ov ? (
                  <Statistic
                    title="任务成功率"
                    value={ov.success_rate}
                    precision={1}
                    suffix="%"
                    valueStyle={{ fontWeight: 600 }}
                  />
                ) : (
                  <LoadingBlock rows={2} />
                )}
                {ov ? (
                  <Link to="/analyses">
                    <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                      查看全部任务 →
                    </Typography.Text>
                  </Link>
                ) : null}
              </Card>
            </Col>
          </>
        )}
      </Row>

      {/* —— 趋势 + 情感占比 —— */}
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={24} lg={16}>
          <Card title="声量与情感趋势" styles={{ body: { paddingTop: 12 } }}>
            {trendQ.isError ? (
              <ErrorBlock description="趋势数据加载失败" onRetry={() => void trendQ.refetch()} />
            ) : trendQ.isLoading ? (
              <LoadingBlock rows={5} />
            ) : trendEmpty ? (
              <Empty description="暂无趋势数据，创建分析任务后即可查看" style={{ padding: 24 }} />
            ) : (
              <EChart option={trendOption} height={300} />
            )}
          </Card>
        </Col>
        <Col xs={24} lg={8}>
          <Card title="情感占比" styles={{ body: { paddingTop: 12 } }}>
            {overviewQ.isError && !ov ? (
              <ErrorBlock description="情感数据加载失败" onRetry={() => void overviewQ.refetch()} />
            ) : !ov ? (
              <LoadingBlock rows={5} />
            ) : (
              <EChart option={sentimentOption} height={300} />
            )}
          </Card>
        </Col>
      </Row>

      {/* —— 来源分布 + 热门话题 —— */}
      <Row gutter={[16, 16]}>
        <Col xs={24} lg={10}>
          <Card title="来源分布" styles={{ body: { paddingTop: 12 } }}>
            {sourcesQ.isError ? (
              <ErrorBlock description="来源数据加载失败" onRetry={() => void sourcesQ.refetch()} />
            ) : sourcesQ.isLoading ? (
              <LoadingBlock rows={5} />
            ) : sortedSources.length === 0 ? (
              <Empty description="暂无来源数据" style={{ padding: 24 }} />
            ) : (
              <EChart option={sourcesOption} height={300} />
            )}
          </Card>
        </Col>
        <Col xs={24} lg={14}>
          <Card
            title="热门话题"
            extra={
              <Link to="/analyses/new" style={{ fontSize: 13 }}>
                新建分析
              </Link>
            }
          >
            {topicsQ.isError ? (
              <ErrorBlock description="话题数据加载失败" onRetry={() => void topicsQ.refetch()} />
            ) : topicsQ.isLoading ? (
              <LoadingBlock rows={5} />
            ) : !hasTopics ? (
              <Empty description="暂无话题数据" style={{ padding: 24 }} />
            ) : (
              <List
                dataSource={topics}
                style={{ minHeight: 300 }}
                renderItem={(t: Topic, i: number) => (
                  <List.Item style={{ gap: 12 }}>
                    <span
                      style={{
                        width: 24,
                        textAlign: 'center',
                        fontWeight: 600,
                        color: i < 3 ? '#FF2442' : 'rgba(0,0,0,0.45)',
                      }}
                    >
                      {i + 1}
                    </span>
                    <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {t.name}
                    </span>
                    <span style={{ color: 'rgba(0,0,0,0.45)', fontSize: 13, fontVariantNumeric: 'tabular-nums' }}>
                      {formatNum(t.doc_count)} 篇
                    </span>
                    <TrendTag trend={t.trend} />
                  </List.Item>
                )}
              />
            )}
          </Card>
        </Col>
      </Row>
    </div>
  );
}
