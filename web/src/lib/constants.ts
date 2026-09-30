/**
 * 领域常量：状态机 / 数据源 / 情感 / 格式的展示元信息。
 * 状态机与后端 analysis_state_machine 对齐（9 态）。
 */

export type AnalysisType = 'event' | 'brand' | 'competitor' | 'industry';

export interface AnalysisTypeMeta {
  value: AnalysisType;
  label: string;
  desc: string;
}

export const ANALYSIS_TYPES: AnalysisTypeMeta[] = [
  { value: 'event', label: '舆情事件分析', desc: '研判本次采集的事件讨论；传播关系需要真实证据' },
  { value: 'brand', label: '品牌声誉监测', desc: '研判本次采集的品牌口碑与声誉风险' },
  { value: 'competitor', label: '竞品动态追踪', desc: '研判本次采集的竞品动作与市场反馈' },
  { value: 'industry', label: '行业趋势洞察', desc: '研判本次采集的行业话题与关注点' },
];

/** Task 1 的场景目录仅供单次分析选择视角；当前创建接口不接受 template_id。 */
export const ANALYSIS_TEMPLATES: { id: string; label: string; desc: string; defaultType: AnalysisType }[] = [
  { id: 'brand_daily', label: '品牌日常口碑', desc: '品牌提及、评价及变化', defaultType: 'brand' },
  { id: 'product_launch', label: '新品上市', desc: '新品讨论、反馈与风险', defaultType: 'event' },
  { id: 'quality_complaint', label: '产品质量投诉', desc: '质量问题、投诉与重复诉求', defaultType: 'brand' },
  { id: 'competitor_update', label: '竞品动态', desc: '竞品提及及主要变化', defaultType: 'competitor' },
  { id: 'crisis', label: '突发危机', desc: '特定事件的扩散与风险变化', defaultType: 'event' },
  { id: 'campaign_review', label: '营销活动复盘', desc: '活动讨论、反馈与传播结果', defaultType: 'event' },
];

export type AnalysisState =
  | 'draft'
  | 'queued'
  | 'acquiring_budget'
  | 'fetching'
  | 'analyzing'
  | 'generating_report'
  | 'completed'
  | 'failed'
  | 'canceled';

/** 正向流转顺序（用于详情页时间线） */
export const ANALYSIS_STATE_FLOW: AnalysisState[] = [
  'draft',
  'queued',
  'acquiring_budget',
  'fetching',
  'analyzing',
  'generating_report',
  'completed',
];

/** 终态：轮询到这些状态后停止 */
export const TERMINAL_STATES: AnalysisState[] = ['completed', 'failed', 'canceled'];

/** 允许取消的状态（执行中） */
export const CANCELLABLE_STATES: AnalysisState[] = [
  'queued',
  'acquiring_budget',
  'fetching',
  'analyzing',
  'generating_report',
];

/** 允许重跑的状态 */
export const RERUNNABLE_STATES: AnalysisState[] = ['failed', 'canceled', 'completed'];

export const ANALYSIS_STATE_LABELS: Record<AnalysisState, string> = {
  draft: '草稿',
  queued: '排队中',
  acquiring_budget: '预算校验',
  fetching: '数据采集',
  analyzing: '智能研判',
  generating_report: '报告生成',
  completed: '已完成',
  failed: '失败',
  canceled: '已取消',
};

export const ANALYSIS_STATE_TAG_COLORS: Record<AnalysisState, string> = {
  draft: 'default',
  queued: 'default',
  acquiring_budget: 'default',
  fetching: 'processing',
  analyzing: 'processing',
  generating_report: 'processing',
  completed: 'success',
  failed: 'error',
  canceled: 'warning',
};

export type SourceKey = 'douyin' | 'toutiao' | 'xigua' | 'weibo' | 'wechat' | 'xiaohongshu' | 'bilibili' | 'news';

export const SOURCE_LABELS: Record<SourceKey, string> = {
  douyin: '抖音',
  toutiao: '今日头条',
  xigua: '西瓜视频',
  weibo: '微博',
  wechat: '公众号',
  xiaohongshu: '小红书',
  bilibili: 'B站',
  news: '新闻', // Historical analyses may still contain this source.
};

export const SOURCES: SourceKey[] = ['douyin', 'toutiao', 'xigua', 'weibo', 'wechat', 'xiaohongshu', 'bilibili'];
export const SELECTABLE_SOURCES: SourceKey[] = ['douyin', 'toutiao', 'xigua', 'weibo', 'wechat'];

export function sourceLabel(key: string): string {
  return SOURCE_LABELS[key as SourceKey] ?? key;
}

export type SentimentKey = 'positive' | 'neutral' | 'negative';

export const SENTIMENT_META: Record<SentimentKey, { label: string; color: string; bg: string }> = {
  positive: { label: '正面', color: '#02b940', bg: '#EAF8EF' },
  neutral: { label: '中性', color: '#6b7280', bg: 'rgba(48,48,52,0.08)' },
  negative: { label: '负面', color: '#FF2442', bg: '#FFEDF0' },
};

export type ReportFormat = 'html' | 'md' | 'pdf' | 'docx';

export const REPORT_FORMATS: { value: ReportFormat; label: string }[] = [
  { value: 'html', label: 'HTML' },
  { value: 'md', label: 'Markdown' },
  { value: 'pdf', label: 'PDF' },
  { value: 'docx', label: 'Word' },
];

/** 话题趋势取值：up / down / stable（后端约定，其他值按平稳处理） */
export function trendArrow(trend?: string | null): 'up' | 'down' | 'flat' {
  if (trend === 'up' || trend === 'rising') return 'up';
  if (trend === 'down' || trend === 'falling') return 'down';
  return 'flat';
}
