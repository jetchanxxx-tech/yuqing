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
  { value: 'event', label: '舆情事件分析', desc: '追踪突发事件传播路径，快速掌握事件脉络与热度拐点' },
  { value: 'brand', label: '品牌声誉监测', desc: '持续监测品牌口碑，识别声誉风险并评估应对效果' },
  { value: 'competitor', label: '竞品动态追踪', desc: '跟踪竞品动作与市场反馈，第一时间发现竞争信号' },
  { value: 'industry', label: '行业趋势洞察', desc: '聚合行业讨论声量，洞察话题演变与用户关注点迁移' },
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

export type SourceKey = 'weibo' | 'wechat' | 'news' | 'xiaohongshu' | 'bilibili' | 'douyin';

export const SOURCE_LABELS: Record<SourceKey, string> = {
  weibo: '微博',
  wechat: '公众号',
  news: '新闻',
  xiaohongshu: '小红书',
  bilibili: 'B站',
  douyin: '抖音',
};

export const SOURCES: SourceKey[] = ['weibo', 'wechat', 'news', 'xiaohongshu', 'bilibili', 'douyin'];

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
