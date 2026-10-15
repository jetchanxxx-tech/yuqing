import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Collapse,
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
  getEventTimeline,
  rerunAnalysis,
  type AnalysisResult,
  type RetrievalCoverage,
  type EventTimelineResponse,
  type DimensionResult,
  type SentimentItem,
  type ResearchIntent,
  type PlatformCoverage,
  type CandidateAudit,
  type MaterialCoverage,
  type SentimentDoc,
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
import { useDateTime, formatNum, formatSourceTime } from '../lib/format';

const RUNNING_HINTS: Partial<Record<AnalysisState, string>> = {
  queued: '任务已进入队列，等待调度执行',
  acquiring_budget: '正在校验预算额度并接纳本轮任务',
  fetching: '正在逐平台检索、补抓正文并核验与共同主题的关系',
  analyzing: '正在分批阅读直接证据与背景材料，开展智能研判',
  generating_report: '正在编排报告并核验引用与材料处理记录',
};

const TREND_ICONS: Record<string, React.ReactNode> = {
  up: <ArrowUpOutlined />,
  down: <ArrowDownOutlined />,
  flat: <MinusOutlined />,
};

export default function AnalysisDetailPage() {
  const formatDateTime = useDateTime();
  const { id } = useParams<{ id: string }>();
  const [timelineCursor, setTimelineCursor] = useState('');
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
  const timelineQ = useQuery({
    queryKey: ['analysis', 'event-timeline', id, timelineCursor],
    queryFn: () => getEventTimeline(id as string, timelineCursor),
    enabled: !!id && (completed || failed),
  });

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
      setTimelineCursor('');
      void queryClient.invalidateQueries({ queryKey: ['analysis', 'event-timeline', id] });
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
                ...(detail.date_from || detail.date_to ? [{
                  key: 'period', label: '分析时间范围',
                  children: detail.date_from && detail.date_to
                    ? `${detail.date_from} 至 ${detail.date_to}`
                    : detail.date_from ? `${detail.date_from} 起` : `截至 ${detail.date_to}`,
                }] : []),
                { key: 'progress', label: '进度', children: <span>{formatNum(detail.progress)}%</span> },
                ...(detail.research_question ? [{ key: 'question', label: '关注问题', children: detail.research_question }] : []),
              ]}
            />
            {running && <RunTiming startedAt={detail.run_started_at} deadlineAt={detail.task_deadline_at}
              phaseStartedAt={detail.phase_started_at} stageDeadlineAt={detail.stage_deadline_at} />}
            {running && (
              <Progress
                percent={Math.max(0, Math.min(100, detail.progress ?? 0))}
                strokeColor="#FF2442"
                style={{ maxWidth: 420, marginTop: 4 }}
              />
            )}
            {failed && detail.error_code === 'insufficient_relevant_evidence' && (
              <Alert type="warning" showIcon message="有效证据不足" description="不同链接的直接证据不足两条，未生成完整研判或报告；背景材料不用于补足门槛，请查看下方各平台与候选处理原因。" style={{ marginTop: 12 }} />
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

      {(detail.research_intent_version === 'semantic-v2' || detail.retrieval_coverage?.admission_version === 'semantic-v2')
        && (detail.research_intent || detail.retrieval_coverage?.research_intent) && (
        <Card title="系统理解（本轮主题）" style={{ borderRadius: 16, marginBottom: 16 }}>
          <ResearchIntentView intent={(detail.research_intent || detail.retrieval_coverage?.research_intent) as ResearchIntent} />
        </Card>
      )}

      {(completed || failed) && (
        <Card title="本次采集准入" style={{ borderRadius: 16, marginBottom: 16 }}>
          <RetrievalCoverageView coverage={detail.retrieval_coverage} storedCount={detail.doc_count} />
        </Card>
      )}
      {(detail.insight_material_coverage || detail.report_material_coverage) && (
        <Card title="材料处理记录" style={{ borderRadius: 16, marginBottom: 16 }}>
          <MaterialCoverageView label="洞察" coverage={detail.insight_material_coverage} />
          <MaterialCoverageView label="报告" coverage={detail.report_material_coverage} />
        </Card>
      )}
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
                      {detail.error_code === 'fetch_failed'
                        ? '采集未完成，请检查关键词、所选来源及采集记录后重试。'
                        : '本次分析未完整交付，请查看错误提示与处理记录后重试。'}
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
      {(completed || failed) && (
        <Card title="事件原文时间线（按已采集来源发表时间）" style={{ borderRadius: 16, marginTop: 16 }}>
          {timelineQ.isLoading ? <LoadingBlock rows={3} /> : timelineQ.isError ? (
            <ErrorBlock description="时间线加载失败" onRetry={() => void timelineQ.refetch()} />
          ) : timelineQ.data ? (
            <EventEvidenceTimeline data={timelineQ.data} onNext={setTimelineCursor} />
          ) : null}
        </Card>
      )}
    </div>
  );
}

function RunTiming({ startedAt, deadlineAt, phaseStartedAt, stageDeadlineAt }: {
  startedAt?: string; deadlineAt?: string; phaseStartedAt?: string; stageDeadlineAt?: string;
}) {
  const [now, setNow] = useState(Date.now());
  const formatDateTime = useDateTime();
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const elapsed = (value: string) => {
    const seconds = Math.max(0, Math.floor((now - Date.parse(value)) / 1000));
    return `${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`;
  };
  return <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
    {startedAt && Number.isFinite(Date.parse(startedAt)) ? `本轮已用时 ${elapsed(startedAt)}` : '等待本轮开始执行'}
    {phaseStartedAt && Number.isFinite(Date.parse(phaseStartedAt)) ? ` · 当前阶段 ${elapsed(phaseStartedAt)}` : ''}
    {deadlineAt ? ` · 最晚处理时间 ${formatDateTime(deadlineAt)}` : ' · 全流程最长 46 分钟，按剩余时限处理'}
    {stageDeadlineAt ? ` · 当前阶段截止 ${formatDateTime(stageDeadlineAt)}` : ''}
  </Typography.Paragraph>;
}

function ResearchIntentView({ intent }: { intent: ResearchIntent }) {
  return <>
    <Typography.Paragraph style={{ fontSize: 15, marginBottom: 8 }}>{intent.topic || '研究意图已保存，未提供主题说明'}</Typography.Paragraph>
    <Descriptions column={1} size="small" items={[
      ...(intent.original_keywords?.length ? [{ key: 'original', label: '原始关键词', children: intent.original_keywords.join(' / ') }] : []),
      ...(intent.entities?.length ? [{ key: 'entities', label: '研究对象', children: intent.entities.join('、') }] : []),
      ...(intent.focus?.length ? [{ key: 'focus', label: '关注点', children: intent.focus.join('、') }] : []),
      ...(intent.relations?.length ? [{ key: 'relations', label: '对象关系', children: intent.relations.join('；') }] : []),
      ...(intent.non_assumptions?.length ? [{ key: 'limits', label: '不预设的事实', children: intent.non_assumptions.join('；') }] : []),
    ]} />
    <Typography.Text type="secondary">这是本轮系统对共同主题的理解，不能作为产品已发布或事实已证实的依据。</Typography.Text>
  </>;
}

function MaterialCoverageView({ label, coverage }: { label: string; coverage?: MaterialCoverage | null }) {
  if (!coverage?.phases?.length) return null;
  const phaseNames: Record<string, string> = { classification: '情感与话题', summary: '摘要', report: '报告写作',
    'dimension:background': '背景概述', 'dimension:heat': '热度传播', 'dimension:sentiment': '情感观点',
    'dimension:group': '群体平台差异', 'dimension:deep_cause': '深层原因' };
  return <Collapse ghost items={coverage.phases.map((phase) => ({
    key: `${label}-${phase.phase}`,
    label: `${label} · ${phaseNames[phase.phase] || phase.phase}：已处理 ${phase.read_spans?.length || 0} 个原文片段，未处理 ${phase.unprocessed?.length || 0} 个`,
    children: <>
      {phase.input_basis === 'verified_dimension_findings' && <Typography.Paragraph type="secondary">本阶段读取已核验的维度结论，未将生成内容标作原文。</Typography.Paragraph>}
      {phase.failure_reason && <Alert type="warning" message={phase.failure_reason} />}
      <List size="small" dataSource={phase.read_spans || []} renderItem={(span) => <List.Item>
        文档 {span.document_id} [{span.start}:{span.end}] · {span.material_class === 'context' ? '背景材料' : '直接证据'}
      </List.Item>} />
      {(phase.unprocessed || []).map((span, index) => <Alert key={`${span.document_id}-${index}`} type="warning" showIcon
        message={`文档 ${span.document_id} [${span.start}:${span.end}]：${span.reason}`} style={{ marginTop: 8 }} />)}
    </>,
  }))} />;
}

function sourceLabel(source?: string): string {
  return SOURCE_LABELS[source as keyof typeof SOURCE_LABELS] || source || '来源未知';
}

function contentKindLabel(kind?: string): string {
  return ({ body: '抓取正文', article: '抓取正文', summary: '搜索摘要', snippet: '搜索摘要',
    page_description: '页面描述', video_description: '视频页面描述', body_excerpt: '正文片段' } as Record<string, string>)[kind || ''] || kind || '历史材料（类型未记录）';
}

function publicLink(url?: string, label = '查看来源') {
  return url && /^https?:\/\//i.test(url) ? <a href={url} target="_blank" rel="noopener noreferrer">{label}</a> : null;
}

function EventEvidenceTimeline({ data, onNext }: { data: EventTimelineResponse; onNext: (cursor: string) => void }) {
  const formatDateTime = useDateTime();
  return (
    <Space direction="vertical" size="middle" style={{ width: '100%' }}>
      <Alert type="info" showIcon message="仅按本次分析采集范围内原文所记载的发表时间排序，未独立核验来源时间；不代表全网首发。" />
      <Typography.Text type="secondary">
        原文 {data.coverage.source_count} 条，已定位 {data.coverage.timed_count} 条，未定位 {data.coverage.unlocated_count} 条，去重 {data.coverage.duplicate_count} 条。
      </Typography.Text>
      {data.nodes.length === 0 && data.coverage.timed_count === 0 && <Empty description="暂无有发表时间和可回溯链接的原文" />}
      <Timeline items={data.nodes.map((node) => ({
        key: node.document_id,
        children: <Space direction="vertical" size={2}>
          <Typography.Text strong>{node.kind === 'first_observed' ? '采集范围内最早记录 · ' : ''}{formatDateTime(node.event_time)} · {node.title || '无标题原文'}</Typography.Text>
          <Typography.Text type="secondary">来源事实 · {node.source_name || node.source_type || '来源未标明'}{node.author ? ` · ${node.author}` : ''} · 文档 ID: {node.document_id}</Typography.Text>
          <a href={node.evidence_urls[0]} target="_blank" rel="noopener noreferrer">查看原始来源</a>
        </Space>,
      }))} />
      {data.coverage.unlocated_count > 0 && (
        <Alert type="warning" showIcon message={`另有 ${data.coverage.unlocated_count} 条未定位原文（缺少有效发表时间或可回溯链接），未纳入时间排序。`} />
      )}
      {data.unlocated.length > 0 && <List size="small" header="未定位原文" dataSource={data.unlocated} renderItem={(doc) => (
        <List.Item key={doc.document_id}>
          {doc.title || doc.document_id || '未知原文'} · {doc.source_name || '来源未知'} · {doc.reason === 'missing_published_at' ? '缺少有效发表时间' : '来源链接不可核验'}
          {doc.url && <> · <a href={doc.url} target="_blank" rel="noopener noreferrer">查看来源</a></>}
        </List.Item>
      )} />}
      <Alert type="warning" showIcon message="传播关系不可用：无可核验的转发、回复或引用证据，未绘制传播链路。官方回应及回应后变化尚未核验。" />
      {data.next_cursor && <Button onClick={() => onNext(data.next_cursor)}>下一页证据</Button>}
    </Space>
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

function RetrievalCoverageView({ coverage, storedCount }: { coverage?: RetrievalCoverage | null; storedCount?: number }) {
  if (!coverage?.admission_version) {
    return <Typography.Text type="secondary">该历史任务没有准入审计数据，不能推断当时的有效样本比例。</Typography.Text>;
  }
  const semantic = coverage.admission_version === 'semantic-v2';
  const reasons: Record<string, string> = {
    semantic_irrelevant: '语义判断不相关', semantic_uncertain: '材料不足以判断相关性', uncertain: '材料不足以判断相关性',
    out_of_range_date: '发布日期超出范围', unverifiable_date: '发布日期无法核验', date_conflict: '发布日期冲突',
    source_mismatch: '链接不属于所选平台', unusable: '未取得可用文本', duplicate: '内容重复',
    candidate_limit: '候选处理数量上限，未核验', deadline: '阶段时限内未核验', excluded: '命中排除词',
    semantic_protocol_failed: '语义核验未完成，材料未采纳',
  };
  const protocolReasons: Record<string, string> = {
    unknown_document_id: '判断结果包含未知材料标识', duplicate_document_id: '同一材料出现重复判断',
    invalid_classification: '相关性分类无效', hard_boundary_violation: '材料未通过来源、日期或排除词核验',
    missing_reason: '缺少相关性判断理由', missing_evidence: '缺少可核验原文引用',
    evidence_not_in_document: '引用未能对应原文', missing_decision: '材料尚未完成相关性判断',
    invalid_evidence_shape: '原文引用格式无效', decision_not_object: '材料判断结果格式无效',
  };
  const protocol = coverage.semantic_protocol_errors;
  const batchIds = Array.from(new Set(protocol?.batch_document_ids || []));
  const candidates = new Map<string, CandidateAudit>((coverage.candidate_audit || []).map((candidate) => [candidate.id, candidate]));
  const protocolIssues = (protocol?.errors || []).flatMap((error) => {
    const reason = protocolReasons[error.category];
    if (typeof reason !== 'string') return [];
    const ids = error.document_id && batchIds.includes(error.document_id) ? [error.document_id] : batchIds;
    const materials = ids.map((documentId) => candidates.get(documentId)?.title || documentId).join('、');
    return [`${reason}：${materials || '该批次材料'}`];
  });
  const platformStatus = (row: PlatformCoverage) => {
    if (row.status === 'failed') return '检索失败';
    if (row.status === 'unprocessed') return '尚未检索／时限未处理';
    if (row.status === 'partial') return '部分完成';
    if (row.status === 'queried' && row.provider_candidates === 0) return '已检索，无候选';
    if (row.status === 'queried') return '已检索';
    return '执行状态未记录';
  };
  return (
    <>
      <Space wrap>
        <Tag>搜索候选 {coverage.provider_candidates} 条</Tag>
        <Tag>{semantic ? '无可用文本' : '无效摘要'} {coverage.unusable_count} 条</Tag>
        <Tag>不相关 {coverage.irrelevant_count} 条</Tag>
        {semantic ? <>
          <Tag color="green">直接证据 {coverage.direct_count ?? storedCount ?? 0} 条</Tag>
          <Tag>背景材料 {coverage.context_count ?? 0} 条</Tag>
        </> : <>
          <Tag>相关候选 {coverage.accepted_count} 条</Tag>
          <Tag>历史准入材料 {storedCount ?? coverage.accepted_count} 条</Tag>
        </>}
        {!!coverage.unverifiable_date_count && <Tag>日期无法核验 {coverage.unverifiable_date_count} 条</Tag>}
        {!!coverage.out_of_range_date_count && <Tag>日期越界 {coverage.out_of_range_date_count} 条</Tag>}
      </Space>
      <Typography.Paragraph type="secondary" style={{ margin: '8px 0 0' }}>
        仅统计本次执行，不代表全网或平台覆盖率。
        {semantic ? '正文、页面描述和搜索摘要按实际获取情况标注；背景材料不计入目标评价统计，也不能补足两篇直接证据门槛。' : '该历史任务采用旧准入规则，材料为搜索摘要，未补做语义判断。'}
        {coverage.candidate_truncated ? ' 本次存在因数量或时间限制未处理的候选，不能将其视为不相关。' : ''}
      </Typography.Paragraph>
      {semantic && protocol && <Alert type="warning" showIcon message="语义核验未完成" style={{ marginTop: 12 }}
        description={<>
          <p style={{ margin: '0 0 8px' }}>该批次材料未通过核验，未采纳为本轮研判或报告证据。</p>
          {protocolIssues.length ? <ul style={{ margin: 0, paddingLeft: 20 }}>
            {protocolIssues.map((issue, index) => <li key={index}>{issue}</li>)}
          </ul> : '判断结果未通过校验，请查看候选材料与处理记录后重试。'}
        </>} />}
      {!!coverage.per_platform?.length && <Table<PlatformCoverage> size="small" pagination={false} rowKey="source"
        dataSource={coverage.per_platform} scroll={{ x: 800 }} style={{ marginTop: 16 }} columns={[
          { title: '平台', dataIndex: 'source', render: (source: string) => sourceLabel(source) },
          { title: '执行情况', key: 'status', render: (_value: unknown, row) => platformStatus(row) },
          { title: '检索次数', dataIndex: 'queries', render: (value?: number) => value ?? '未记录' },
          { title: '候选', dataIndex: 'provider_candidates', render: (value?: number) => value ?? '未记录' },
          { title: '正文／摘要', key: 'body-summary', render: (_value: unknown, row) => `${row.body_count ?? 0}／${row.summary_count ?? 0}` },
          { title: '直接／背景', key: 'accepted', render: (_value: unknown, row) => `${row.direct_count ?? 0}／${row.context_count ?? 0}` },
          { title: '排除', dataIndex: 'rejected_count', render: (value?: number) => value ?? 0 },
          { title: '未处理', dataIndex: 'unprocessed_count', render: (value?: number) => value ?? 0 },
          { title: '失败', dataIndex: 'failed_count', render: (value?: number) => value ?? 0 },
        ]} />}
      {!!coverage.candidate_audit?.length && <Collapse style={{ marginTop: 12 }} items={[{
        key: 'audit', label: '候选材料与处理原因', children: <List<CandidateAudit> dataSource={coverage.candidate_audit}
          renderItem={(candidate) => <List.Item key={candidate.id} style={{ display: 'block' }}>
            <Typography.Text strong>{candidate.title || candidate.id}</Typography.Text>
            <div><Tag>{sourceLabel(candidate.source_type)}</Tag><Tag>{contentKindLabel(candidate.content_kind)}</Tag>
              <Tag>{({ direct: '直接相关', context: '背景／比较', irrelevant: '不相关', uncertain: '信息不足' } as Record<string, string>)[candidate.semantic_classification || '']
                || ({ unprocessed: '未完成，未采纳', rejected: '已排除' } as Record<string, string>)[candidate.status || ''] || candidate.status || '未核验'}</Tag>
              {candidate.truncated && <Tag color="orange">正文达到保存上限</Tag>}
            </div>
            {candidate.requested_source && candidate.requested_source !== candidate.source_type && <Typography.Paragraph type="secondary">请求平台：{sourceLabel(candidate.requested_source)}；链接实际平台：{sourceLabel(candidate.source_type)}</Typography.Paragraph>}
            {candidate.semantic_reason && <Typography.Paragraph style={{ marginBottom: 4 }}>{candidate.semantic_reason}</Typography.Paragraph>}
            {candidate.reason && <div>处理原因：{reasons[candidate.reason] || candidate.reason}</div>}
            {candidate.fetch_status && <div>正文补抓：{candidate.fetch_status}{candidate.fetch_reason ? ` · ${candidate.fetch_reason}` : ''}</div>}
            <div>发表时间：{candidate.published_at || '未核验'}；检索来源日期：{candidate.date_sources?.provider || '未提供'}；正文日期：{candidate.date_sources?.body || '未取得'}</div>
            <Space wrap>{publicLink(candidate.url, '检索原链接')}{candidate.final_url && publicLink(candidate.final_url, '实际到达链接')}</Space>
            {(candidate.evidence_spans || []).map((text, index) => <blockquote key={index} style={{ margin: '6px 0', color: 'rgba(0,0,0,0.62)' }}>
              {text}
            </blockquote>)}
          </List.Item>} />,
      }]} />}
    </>
  );
}

/* ================== 完成态结果 Tabs ================== */
function MaterialList({ documents }: { documents: SentimentDoc[] }) {
  const formatDateTime = useDateTime();
  return <List dataSource={documents} renderItem={(doc) => <List.Item key={doc.id} style={{ alignItems: 'flex-start', paddingInline: 0 }}>
    <List.Item.Meta title={<Space wrap>
      {publicLink(doc.final_url || doc.url, doc.title || '（无标题）') || <Typography.Text>{doc.title || '（无标题）'}</Typography.Text>}
      <Tag>{sourceLabel(doc.source_type)}</Tag><Tag>{contentKindLabel(doc.content_kind)}</Tag>
      {doc.truncated && <Tag color="orange">正文达到保存上限</Tag>}
    </Space>} description={<>
      <Typography.Paragraph type="secondary" style={{ marginBottom: 8 }}>
        文档 ID：{doc.id} · {doc.source_name || sourceLabel(doc.source_type)}
        {doc.published_at ? ` · ${formatDateTime(doc.published_at)}` : doc.source_published_at ? ` · ${formatSourceTime(doc.source_published_at)}` : ' · 发表时间未记录'}
      </Typography.Paragraph>
      {doc.semantic_reason && <Typography.Paragraph>{doc.semantic_reason}</Typography.Paragraph>}
      {doc.fetch_reason && <Typography.Paragraph type="secondary">正文补抓：{doc.fetch_reason}</Typography.Paragraph>}
      <Space wrap>{publicLink(doc.url, '检索原链接')}{doc.final_url && doc.final_url !== doc.url && publicLink(doc.final_url, '实际到达链接')}</Space>
      {(doc.date_sources?.provider || doc.date_sources?.body) && <div>日期依据：检索来源 {doc.date_sources.provider || '未提供'}；正文 {doc.date_sources.body || '未取得'}</div>}
      <Typography.Paragraph style={{ marginBottom: 0, fontSize: 13 }} ellipsis={{ rows: 3, expandable: 'collapsible', symbol: (expanded) => expanded ? '收起' : '展开' }}>
        {doc.content || '（该文档暂无正文）'}
      </Typography.Paragraph>
      {(doc.evidence_spans || []).map((text, index) => <blockquote key={index} style={{ margin: '6px 0' }}>{text}</blockquote>)}
    </>} />
  </List.Item>} />;
}

interface ResultTabsProps {
  result: AnalysisResult | undefined;
  resultLoading: boolean;
  resultError: boolean;
  onRetryResult: () => void;
  relatedReports: Report[];
  reportsLoading: boolean;
}

function ResultTabs({ result, resultLoading, resultError, onRetryResult, relatedReports, reportsLoading }: ResultTabsProps) {
  const formatDateTime = useDateTime();
  if (resultError) {
    return <ErrorBlock description="结果数据加载失败" onRetry={onRetryResult} />;
  }
  if (resultLoading || !result) {
    return <LoadingBlock rows={6} />;
  }
  const docs = (result.documents ?? []).filter((document) => document.semantic_classification !== 'context');
  const background = [...(result.documents ?? []).filter((document) => document.semantic_classification === 'context'), ...(result.context_documents ?? [])];
  const sentiments = result.sentiments;
  const topics = result.topics ?? [];
  const dimensions = result.dimensions ?? [];
  const semantic = result.retrieval_coverage?.admission_version === 'semantic-v2';

  return (
    <Tabs
      items={[
        {
          key: 'summary',
          label: '研判摘要',
          children: (
            <div>
              <Typography.Paragraph type="secondary">{semantic
                ? `本轮直接证据 ${docs.length} 篇，背景材料 ${background.length} 篇。情感、话题及目标评价统计只包含直接证据。`
                : `历史准入材料 ${docs.length} 篇；未补做语义分类，不能倒推为当前规则的直接证据。`}</Typography.Paragraph>
              {result.warning ? (
                <Alert
                  type="warning"
                  showIcon
                  message="部分分析未完成"
                  description={result.warning}
                  style={{ marginBottom: 16 }}
                />
              ) : null}
              {result.summary ? (
                <Typography.Paragraph style={{ fontSize: 15, lineHeight: 2 }}>
                  {result.summary}
                </Typography.Paragraph>
              ) : (
                <EmptyBlock description="暂无研判摘要（分析引擎未产出）" />
              )}
            </div>
          ),
        },
        {
          key: 'docs',
          label: `${semantic ? '直接证据' : '历史准入材料'}（${docs.length}）`,
          children:
            docs.length === 0 ? (
              <EmptyBlock description="未采集到公开文档" />
            ) : (
              <MaterialList documents={docs} />
            ),
        },
        {
          key: 'background', label: `背景材料（${background.length}）`, children: <>
            <Alert type="info" showIcon message="背景／比较材料不计入目标评价统计" description="这些内容用于理解背景或比较对象，不作为新品评价、目标情感或直接证据门槛的分母。" style={{ marginBottom: 12 }} />
            {background.length ? <MaterialList documents={background} /> : <EmptyBlock description="本轮没有背景材料" />}
          </>,
        },
        {
          key: 'sentiment',
          label: '情感分析',
          children: (
            <SentimentView sentiments={sentiments} />
          ),
        },
        {
          key: 'dimensions',
          label: `五维研判（${dimensions.length}）`,
          children:
            dimensions.length === 0 ? (
              <EmptyBlock description="暂无五维研判结论（分析引擎未产出或任务较早）" />
            ) : (
              <DimensionsView dimensions={dimensions} />
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
                    render: (t: string) => {
                      // 分析引擎返回 rising/stable/falling；必须经 trendArrow 归一化后
                      // 再判定文案 —— 用原始值比较 up/down 会让箭头正确但文案永远是「平稳」。
                      const arrow = trendArrow(t);
                      const label = arrow === 'up' ? '上升' : arrow === 'down' ? '下降' : '平稳';
                      return (
                        <Tag color="default" style={{ marginInlineEnd: 0 }}>
                          {TREND_ICONS[arrow]} {label}
                        </Tag>
                      );
                    },
                  },
                ]}
              />
            ),
        },
        {
          key: 'report',
          label: `分析报告（${result.report ? 1 : relatedReports.length}）`,
          children: (
            <div>
              <Alert
                type="success"
                showIcon
                message="分析已完成，洞察报告已生成"
                description="报告由 AI 基于采集文档与情感/话题分析自动生成。"
                style={{ marginBottom: 16 }}
              />
              {result.report?.content ? (
                <iframe
                  title="分析报告预览"
                  srcDoc={result.report.content}
                  sandbox=""
                  style={{ width: '100%', height: 560, border: '1px solid #f0f0f0', borderRadius: 12 }}
                />
              ) : reportsLoading ? (
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

/* ================== 五维研判视图 ================== */
function DimensionsView({ dimensions }: { dimensions: DimensionResult[] }) {
  const items = dimensions.map((d) => ({
    key: d.id,
    label: (
      <Space wrap size={8}>
        <Typography.Text strong>{d.name}</Typography.Text>
        {d.trend ? (
          <Tag
            style={{ marginInlineEnd: 0, whiteSpace: 'normal', overflowWrap: 'anywhere' }}
            color="default"
          >
            {d.trend}
          </Tag>
        ) : null}
      </Space>
    ),
    children: (
      <div>
        {d.findings ? (
          <Typography.Paragraph style={{ marginBottom: 8 }}>
            <Typography.Text strong>核心发现：</Typography.Text>
            {d.findings}
          </Typography.Paragraph>
        ) : null}
        {(d.data_points ?? []).length > 0 ? (
          <ul style={{ margin: '0 0 8px', paddingLeft: 20, color: 'rgba(0,0,0,0.72)', fontSize: 13 }}>
            {(d.data_points ?? []).map((dp, j) => <li key={j}>{dp}</li>)}
          </ul>
        ) : null}
        {(d.quotes ?? []).map((q, j) => (
          <blockquote
            key={j}
            style={{
              margin: '8px 0', padding: '8px 14px', background: '#f9fafb',
              borderLeft: '3px solid #d1d5db', color: '#374151', fontSize: 13,
            }}
          >
            {q.text}
            {q.source ? (
              <footer style={{ color: '#9ca3af', fontSize: 12, marginTop: 4 }}>—— {q.source}</footer>
            ) : null}
            {q.document_id && <Typography.Text type="secondary" style={{ display: 'block', fontSize: 12 }}>文档 {q.document_id}</Typography.Text>}
            {(d.quote_locations || []).filter((location) => location.document_id === q.document_id && location.quote_text === q.text).map((location, index) => (
              <Typography.Text key={index} type="secondary" style={{ display: 'block', fontSize: 12 }}>
                {location.material_class === 'context' ? '背景材料' : '直接证据'} · {location.field === 'title' ? '标题' : '正文'} [{location.start}:{location.end}]
              </Typography.Text>
            ))}
          </blockquote>
        ))}
        {d.deep_read ? (
          <Typography.Paragraph type="secondary" style={{ marginBottom: 0, fontSize: 13 }}>
            <Typography.Text strong style={{ color: 'rgba(0,0,0,0.72)' }}>深入解读：</Typography.Text>
            {d.deep_read}
          </Typography.Paragraph>
        ) : null}
      </div>
    ),
  }));

  return (
    <Collapse
      items={items}
      defaultActiveKey={dimensions.slice(0, 1).map((d) => d.id)}
      expandIconPosition="end"
    />
  );
}

/* ================== 情感视图 ================== */
interface SentimentViewProps {
  sentiments: { positive: number; negative: number; neutral: number; items?: SentimentItem[] };
}

function SentimentView({ sentiments }: SentimentViewProps) {
  const items = sentiments.items ?? [];
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
      {items.length > 0 && (
        <Col span={24}>
          <Card size="small" style={{ borderRadius: 12 }} title="逐文档情感明细">
            <Table
              rowKey="document_id"
              size="small"
              pagination={{ pageSize: 10, showSizeChanger: false }}
              dataSource={items}
              columns={[
                {
                  title: '文档 ID',
                  dataIndex: 'document_id',
                  key: 'document_id',
                  width: 240,
                  render: (v: string) => <Typography.Text code style={{ fontSize: 12 }}>{v}</Typography.Text>,
                },
                {
                  title: '情感',
                  dataIndex: 'sentiment',
                  key: 'sentiment',
                  width: 110,
                  render: (s: string) => (
                    <Tag
                      color={s === 'positive' ? 'success' : s === 'negative' ? 'error' : 'default'}
                      style={{ marginInlineEnd: 0 }}
                    >
                      {s === 'positive' ? '正面' : s === 'negative' ? '负面' : '中性'}
                    </Tag>
                  ),
                },
                {
                  title: '强度',
                  dataIndex: 'score',
                  key: 'score',
                  width: 180,
                  render: (v: number) => (
                    <Progress
                      percent={Math.round((v ?? 0) * 100)}
                      size="small"
                      strokeColor="#1677ff"
                    />
                  ),
                },
              ]}
            />
          </Card>
        </Col>
      )}
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
