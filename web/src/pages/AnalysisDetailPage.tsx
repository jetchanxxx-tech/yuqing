import { useMemo } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  List,
  Popconfirm,
  Progress,
  Row,
  Space,
  Statistic,
  Table,
  Tabs,
  Tag,
  Timeline,
  Tooltip,
  Typography,
} from 'antd';
import {
  ArrowDownOutlined,
  ArrowLeftOutlined,
  ArrowUpOutlined,
  DownloadOutlined,
  MinusOutlined,
  ReloadOutlined,
  SyncOutlined,
} from '@ant-design/icons';
import type { EChartsOption } from 'echarts';
import { Link, useParams } from 'react-router-dom';
import EChart from '../components/EChart';
import { ErrorBlock, EmptyBlock, LoadingBlock } from '../components/PageState';
import {
  cancelAnalysis,
  getAnalysis,
  getAnalysisResult,
  rerunAnalysis,
  type AnalysisResult,
} from '../api/analyses';
import { listReports, openReportDownload, type Report } from '../api/reports';
import {
  ANALYSIS_STATE_FLOW,
  ANALYSIS_STATE_LABELS,
  ANALYSIS_STATE_TAG_COLORS,
  CANCELLABLE_STATES,
  RERUNNABLE_STATES,
  SENTIMENT_META,
  SOURCE_LABELS,
  REPORT_FORMATS,
  TERMINAL_STATES,
  trendArrow,
  type AnalysisState,
  type SentimentKey,
} from '../lib/constants';
import { formatDateTime, formatNum } from '../lib/format';

const RUNNING_HINTS: Partial<Record<AnalysisState, string>> = {
  queued: '任务已进入队列，等待调度执行',
  acquiring_budget: '正在校验预算额度',
  fetching: '正在从各平台采集公开数据',
  analyzing: '多 Agent 正在协同研判内容',
  generating_report: '正在生成洞察报告',
};

const TREND_ICONS: Record<string, React.ReactNode> = {
  up: <ArrowUpOutlined />,
  down: <ArrowDownOutlined />,
  flat: <MinusOutlined />,
};

export default function AnalysisDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { message } = App.useApp();
  const queryClient = useQueryClient();

  // —— 状态轮询：每 5s 一次，进入终态后停止 ——
  const detailQ = useQuery({
    queryKey: ['analysis', 'detail', id],
    queryFn: () => getAnalysis(id as string),
    enabled: !!id,
    refetchInterval: (query) => {
      const s = query.state.data?.state;
      return s && TERMINAL_STATES.includes(s) ? false : 5000;
    },
  });
  const detail = detailQ.data;
  const state = detail?.state;

  const completed = state === 'completed';
  const running = state !== undefined && !TERMINAL_STATES.includes(state);
  const failed = state === 'failed';

  // —— 结果（仅 completed 时拉取） ——
  const resultQ = useQuery({
    queryKey: ['analysis', 'result', id],
    queryFn: () => getAnalysisResult(id as string),
    enabled: completed,
  });

  // —— 报告列表（完成态标签页内展示该分析的报告） ——
  const reportsQ = useQuery({
    queryKey: ['reports', 'list'],
    queryFn: listReports,
    enabled: completed,
  });
  const relatedReports = useMemo(
    () => (reportsQ.data?.reports ?? []).filter((r) => r.analysis_id === id),
    [reportsQ.data, id]
  );

  const cancelQ = useMutation({
    mutationFn: () => cancelAnalysis(id as string),
    onSuccess: () => {
      message.success('已发送取消请求');
      void queryClient.invalidateQueries({ queryKey: ['analyses'] });
    },
  });
  const rerunQ = useMutation({
    mutationFn: () => rerunAnalysis(id as string),
    onSuccess: () => {
      message.success('任务已重新排队');
      void queryClient.invalidateQueries({ queryKey: ['analyses'] });
    },
  });

  const back = (
    <Link to="/analyses" style={{ display: 'inline-block', marginBottom: 12 }}>
      <ArrowLeftOutlined /> 返回任务列表
    </Link>
  );

  if (!detailQ.data && detailQ.isLoading) {
    return (
      <div>
        {back}
        <Card style={{ borderRadius: 16 }}>
          <LoadingBlock rows={5} />
        </Card>
      </div>
    );
  }
  if (detailQ.isError || !detail || !state) {
    return (
      <div>
        {back}
        <Card style={{ borderRadius: 16 }}>
          <ErrorBlock description="分析详情加载失败" onRetry={() => void detailQ.refetch()} />
        </Card>
      </div>
    );
  }

  return (
    <div>
      {back}
      {/* —— 概览卡 —— */}
      <Card style={{ borderRadius: 16, marginBottom: 16 }}>
        <Row gutter={[16, 16]} align="middle" justify="space-between">
          <Col flex="auto">
            <Space size={12} wrap>
              <Typography.Title level={4} style={{ margin: 0 }}>
                {detail.name}
              </Typography.Title>
              <Tag color={ANALYSIS_STATE_TAG_COLORS[state]} style={{ fontSize: 13, padding: '2px 12px' }}>
                {running && <SyncOutlined spin />} {ANALYSIS_STATE_LABELS[state]}
              </Tag>
            </Space>
            <Descriptions
              column={{ xs: 1, sm: 2, lg: 3 }}
              size="small"
              style={{ marginTop: 8 }}
              items={[
                { key: 'id', label: '任务 ID', children: <Typography.Text code>{detail.id}</Typography.Text> },
                ...(detail.created_at ? [{ key: 'at', label: '创建时间', children: formatDateTime(detail.created_at) }] : []),
                { key: 'progress', label: '进度', children: <span>{formatNum(detail.progress)}%</span> },
              ]}
            />
            {running && (
              <Progress
                percent={Math.max(0, Math.min(100, detail.progress ?? 0))}
                strokeColor="#FF2442"
                style={{ maxWidth: 420, marginTop: 4 }}
              />
            )}
            {failed && detail.error_message && (
              <Alert type="error" showIcon message={detail.error_message} style={{ marginTop: 12 }} />
            )}
          </Col>
          <Col>
            <Space wrap>
              {CANCELLABLE_STATES.includes(state) && (
                <Popconfirm
                  title="取消这次分析？"
                  description="执行中的采集与研判将被终止"
                  okText="取消任务"
                  cancelText="再想想"
                  onConfirm={() => cancelQ.mutate()}
                >
                  <Button danger loading={cancelQ.isPending}>
                    取消任务
                  </Button>
                </Popconfirm>
              )}
              {RERUNNABLE_STATES.includes(state) && (
                <Popconfirm
                  title="重新运行这次分析？"
                  description="将使用原参数重新排队执行"
                  okText="重新运行"
                  cancelText="再想想"
                  onConfirm={() => rerunQ.mutate()}
                >
                  <Button type="primary" icon={<ReloadOutlined />} loading={rerunQ.isPending}>
                    重新运行
                  </Button>
                </Popconfirm>
              )}
              {running && (
                <Button onClick={() => void detailQ.refetch()} loading={detailQ.isFetching}>
                  刷新状态
                </Button>
              )}
            </Space>
          </Col>
        </Row>
      </Card>

      <Row gutter={16} align="stretch">
        {/* —— 状态时间线 —— */}
        <Col xs={24} lg={9}>
          <Card title="任务进度" style={{ borderRadius: 16, height: '100%' }} styles={{ body: { paddingBottom: 8 } }}>
            <AnalysisTimeline state={state} errorMessage={detail.error_message} />
          </Card>
        </Col>
        {/* —— 内容区 —— */}
        <Col xs={24} lg={15}>
          {completed ? (
            <Card style={{ borderRadius: 16 }}>
              <ResultTabs
                resultLoading={resultQ.isLoading}
                resultError={resultQ.isError}
                onRetryResult={() => void resultQ.refetch()}
                result={resultQ.data}
                relatedReports={relatedReports}
                reportsLoading={reportsQ.isLoading}
              />
            </Card>
          ) : (
            <Card title={ANALYSIS_STATE_LABELS[state]} style={{ borderRadius: 16, height: '100%' }}>
              <div style={{ textAlign: 'center', padding: '48px 0' }}>
                {running ? (
                  <>
                    <Typography.Title level={2} style={{ margin: 0 }}>
                      <SyncOutlined spin style={{ color: '#FF2442' }} />
                    </Typography.Title>
                    <Typography.Paragraph type="secondary" style={{ marginTop: 16, fontSize: 15 }}>
                      {RUNNING_HINTS[state] ?? '任务执行中，请稍候…'}
                    </Typography.Paragraph>
                    <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                      页面将每 5 秒自动刷新状态
                    </Typography.Text>
                  </>
                ) : failed ? (
                  <>
                    <Typography.Title level={4} style={{ color: '#FF2442' }}>
                      分析失败
                    </Typography.Title>
                    <Typography.Paragraph type="secondary">
                      可重新运行任务；若反复失败请前往账单页确认额度
                    </Typography.Paragraph>
                  </>
                ) : (
                  <>
                    <Typography.Title level={4} style={{ color: '#ff7d03' }}>
                      任务已取消
                    </Typography.Title>
                    <Typography.Paragraph type="secondary">如需继续监测，可重新运行任务</Typography.Paragraph>
                  </>
                )}
              </div>
            </Card>
          )}
        </Col>
      </Row>
    </div>
  );
}

/* ================== 状态时间线 ================== */
function AnalysisTimeline({ state, errorMessage }: { state: AnalysisState; errorMessage?: string }) {
  const flow = ANALYSIS_STATE_FLOW.filter((s) => s !== 'draft');

  const dots = (() => {
    if (state === 'completed') {
      return flow.map((s) => ({ key: s, color: '#02b940' as string, spin: false }));
    }
    if (state === 'failed' || state === 'canceled') {
      return [
        ...flow.map((s) => ({ key: s, color: 'rgba(0,0,0,0.15)', spin: false })),
        {
          key: state,
          color: state === 'failed' ? '#FF2442' : '#ff7d03',
          spin: false,
        },
      ];
    }
    const idx = ANALYSIS_STATE_FLOW.indexOf(state);
    return flow.map((s, i) => {
      const stepIdx = i + 1;
      if (stepIdx < idx) return { key: s, color: '#02b940', spin: false };
      if (stepIdx === idx) return { key: s, color: '#FF2442', spin: true };
      return { key: s, color: 'rgba(0,0,0,0.15)', spin: false };
    });
  })();

  const labelFor = (s: string) => ANALYSIS_STATE_LABELS[s as AnalysisState] ?? s;
  const items = dots.map((d) => ({
    key: d.key,
    dot: d.spin ? <SyncOutlined spin style={{ color: d.color }} /> : undefined,
    color: d.color,
    children: (
      <Typography.Text style={{ color: d.color === 'rgba(0,0,0,0.15)' ? 'rgba(0,0,0,0.45)' : 'rgba(0,0,0,0.8)' }}>
        {labelFor(d.key)}
        {d.key === 'failed' && errorMessage ? `：${errorMessage}` : ''}
      </Typography.Text>
    ),
  }));

  return <Timeline items={items} style={{ paddingTop: 8 }} />;
}

/* ================== 完成态结果 Tabs ================== */
interface ResultTabsProps {
  result: AnalysisResult | undefined;
  resultLoading: boolean;
  resultError: boolean;
  onRetryResult: () => void;
  relatedReports: Report[];
  reportsLoading: boolean;
}

function ResultTabs({ result, resultLoading, resultError, onRetryResult, relatedReports, reportsLoading }: ResultTabsProps) {
  if (resultError) {
    return <ErrorBlock description="结果数据加载失败" onRetry={onRetryResult} />;
  }
  if (resultLoading || !result) {
    return <LoadingBlock rows={6} />;
  }
  const docs = result.documents ?? [];
  const sentiments = result.sentiments;
  const topics = result.topics ?? [];

  return (
    <Tabs
      items={[
        {
          key: 'docs',
          label: `文档列表（${docs.length}）`,
          children:
            docs.length === 0 ? (
              <EmptyBlock description="未采集到公开文档" />
            ) : (
              <List
                dataSource={docs}
                renderItem={(doc) => (
                  <List.Item key={doc.id} style={{ alignItems: 'flex-start', paddingInline: 0 }}>
                    <List.Item.Meta
                      title={
                        <Space wrap>
                          {doc.url ? (
                            <a href={doc.url} target="_blank" rel="noopener noreferrer">
                              {doc.title || '（无标题）'}
                            </a>
                          ) : (
                            <Typography.Text>{doc.title || '（无标题）'}</Typography.Text>
                          )}
                          <Tag style={{ marginInlineEnd: 0 }}>
                            {SOURCE_LABELS[doc.source_type as keyof typeof SOURCE_LABELS] ?? doc.source_name ?? doc.source_type}
                          </Tag>
                        </Space>
                      }
                      description={
                        <>
                          <div style={{ color: 'rgba(0,0,0,0.45)', marginBottom: 8 }}>
                            {doc.source_name ?? ''}
                            {doc.published_at ? ` · ${formatDateTime(doc.published_at)}` : ''}
                          </div>
                          <Typography.Paragraph
                            style={{ marginBottom: 0, fontSize: 13 }}
                            ellipsis={{ rows: 3, expandable: 'collapsible', symbol: (expanded) => (expanded ? '收起' : '展开') }}
                          >
                            {doc.content || '（该文档暂无正文）'}
                          </Typography.Paragraph>
                        </>
                      }
                    />
                  </List.Item>
                )}
              />
            ),
        },
        {
          key: 'sentiment',
          label: '情感分析',
          children: (
            <SentimentView sentiments={sentiments} />
          ),
        },
        {
          key: 'topics',
          label: `话题聚类（${topics.length}）`,
          children:
            topics.length === 0 ? (
              <EmptyBlock description="暂无话题聚类结果" />
            ) : (
              <Table
                rowKey="name"
                size="middle"
                pagination={false}
                dataSource={topics}
                columns={[
                  { title: '话题', dataIndex: 'name', key: 'name' },
                  {
                    title: '相关文档',
                    dataIndex: 'doc_count',
                    key: 'doc_count',
                    width: 160,
                    render: (n: number) => {
                      const max = Math.max(...topics.map((t) => t.doc_count), 1);
                      return (
                        <Space size={8}>
                          <div style={{ width: 120, background: 'rgba(0,0,0,0.06)', borderRadius: 4, height: 8 }}>
                            <div
                              style={{
                                width: `${Math.max(4, Math.round((n / max) * 100))}%`,
                                background: '#FF2442',
                                height: 8,
                                borderRadius: 4,
                              }}
                            />
                          </div>
                          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                            {formatNum(n)}
                          </Typography.Text>
                        </Space>
                      );
                    },
                  },
                  {
                    title: '趋势',
                    dataIndex: 'trend',
                    key: 'trend',
                    width: 100,
                    render: (t: string) => (
                      <Tag color="default" style={{ marginInlineEnd: 0 }}>
                        {TREND_ICONS[trendArrow(t)]} {t === 'up' ? '上升' : t === 'down' ? '下降' : '平稳'}
                      </Tag>
                    ),
                  },
                ]}
              />
            ),
        },
        {
          key: 'report',
          label: `分析报告（${relatedReports.length}）`,
          children: (
            <div>
              <Alert
                type="success"
                showIcon
                message="分析已完成，洞察报告已生成"
                description="支持 HTML / Markdown / PDF / Word 四种格式下载，也可前往报告中心统一管理。"
                action={
                  <Link to="/reports">
                    <Button type="primary" size="small">
                      前往报告中心
                    </Button>
                  </Link>
                }
                style={{ marginBottom: 16 }}
              />
              {reportsLoading ? (
                <LoadingBlock rows={3} />
              ) : relatedReports.length === 0 ? (
                <EmptyBlock description="暂无该分析对应的报告记录" />
              ) : (
                <List
                  dataSource={relatedReports}
                  renderItem={(r) => (
                    <List.Item key={r.id} actions={[<ReportDownload report={r} key="dl" />]}>
                      <List.Item.Meta
                        title={r.title || '洞察报告'}
                        description={`格式：${r.format.toUpperCase()} · ${formatDateTime(r.created_at)}`}
                      />
                    </List.Item>
                  )}
                />
              )}
            </div>
          ),
        },
      ]}
    />
  );
}

/* ================== 情感视图 ================== */
function SentimentView({ sentiments }: { sentiments: { positive: number; negative: number; neutral: number } }) {
  const entries = (Object.keys(SENTIMENT_META) as SentimentKey[]).map((k) => ({
    key: k,
    meta: SENTIMENT_META[k],
    value: sentiments[k] ?? 0,
  }));
  const option: EChartsOption = {
    tooltip: { trigger: 'item', formatter: '{b}：{c}%（{d}%）' },
    legend: { bottom: 0, icon: 'circle', itemWidth: 8, itemHeight: 8, textStyle: { color: 'rgba(0,0,0,0.62)' } },
    color: entries.map((e) => e.meta.color),
    series: [
      {
        type: 'pie',
        radius: ['55%', '78%'],
        center: ['50%', '45%'],
        itemStyle: { borderColor: '#fff', borderWidth: 2 },
        label: { show: true, formatter: '{b}\n{d}%', color: 'rgba(0,0,0,0.62)', fontSize: 12 },
        labelLine: { length: 8, length2: 8 },
        data: entries.map((e) => ({ name: e.meta.label, value: e.value })),
      },
    ],
  };
  return (
    <Row gutter={[16, 16]}>
      {entries.map((e) => (
        <Col xs={8} key={e.key}>
          <Card size="small" style={{ borderRadius: 12, textAlign: 'center', background: e.meta.bg, borderColor: 'transparent' }}>
            <Statistic
              title={<span style={{ color: e.meta.color }}>{e.meta.label}</span>}
              value={e.value}
              precision={1}
              suffix="%"
              valueStyle={{ fontWeight: 600 }}
            />
          </Card>
        </Col>
      ))}
      <Col span={24}>
        {entries.every((e) => !e.value) ? (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无情感数据" style={{ padding: 24 }} />
        ) : (
          <EChart option={option} height={280} />
        )}
      </Col>
    </Row>
  );
}

/* ================== 报告下载 ================== */
function ReportDownload({ report }: { report: Report }) {
  const fmt = REPORT_FORMATS.find((f) => f.value === report.format)?.value ?? 'html';
  return (
    <Tooltip title="下载（当前格式）">
      <Button size="small" icon={<DownloadOutlined />} onClick={() => void openReportDownload(report.id, fmt)}>
        下载 {fmt.toUpperCase()}
      </Button>
    </Tooltip>
  );
}
