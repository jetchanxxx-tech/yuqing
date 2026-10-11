import client from './client';
import type { AnalysisState, AnalysisType, SourceKey } from '../lib/constants';

/** POST /analyses 请求体 */
export interface CreateAnalysisPayload {
  name: string;
  analysis_type: AnalysisType;
  keywords: string[];
  sources: SourceKey[];
}

/** 列表元素（GET /analyses） */
export interface AnalysisSummary {
  id: string;
  name: string;
  analysis_type: string;
  state: AnalysisState;
  progress: number;
  created_at: string;
}

export interface AnalysisListResponse {
  analyses: AnalysisSummary[];
  total: number;
}

export interface RetrievalCoverage {
  admission_version: string;
  provider_candidates: number;
  unusable_count: number;
  irrelevant_count: number;
  source_mismatch_count: number;
  accepted_count: number;
  candidate_truncated?: boolean;
  per_keyword?: { keyword: string; status: string; returned_count: number }[];
}

/** GET /analyses/:id（轮询时可能出现的扩展字段） */
export interface AnalysisStateResponse {
  id: string;
  name: string;
  state: AnalysisState;
  progress: number;
  created_at?: string;
  error_message?: string;
  error_code?: string;
  doc_count?: number;
  retrieval_coverage?: RetrievalCoverage | null;
}

export interface SentimentDoc {
  id: string;
  title: string;
  url: string;
  source_type: string;
  source_name: string;
  published_at: string;
  source_published_at?: string;
  content: string;
}

/** 单篇文档的情感分类明细 */
export interface SentimentItem {
  document_id: string;
  sentiment: string;
  score: number;
}

/** 分析报告（内嵌 HTML 内容，来自 report 引擎） */
export interface AnalysisReport {
  id: string;
  format: string;
  content: string;
}

/** 逐字原声引用（来自源文档，附来源平台） */
export interface QuoteItem {
  text: string;
  source?: string;
}

/** 五维度研判结论（背景/热度/情感观点/群体差异/深层原因） */
export interface DimensionResult {
  id: string;
  name: string;
  findings: string;
  data_points?: string[];
  quotes?: QuoteItem[];
  deep_read: string;
  trend?: string;
}

export interface AnalysisResult {
  id: string;
  state: string;
  doc_count?: number;
  retrieval_coverage?: RetrievalCoverage | null;
  documents: SentimentDoc[];
  summary?: string;
  warning?: string;
  sentiments: { positive: number; negative: number; neutral: number; items?: SentimentItem[] };
  topics: Topic[];
  dimensions?: DimensionResult[];
  report?: AnalysisReport | null;
}

export interface EventTimelineNode {
  document_id: string;
  kind: 'first_observed' | 'source_record';
  basis: 'fact';
  event_time: string;
  title: string;
  source_type: string;
  source_name: string;
  author: string;
  evidence_document_ids: string[];
  evidence_urls: string[];
  limitations: string[];
}

export interface EventTimelineResponse {
  nodes: EventTimelineNode[];
  edges: [];
  unlocated: { document_id: string; title: string; url?: string; source_name: string; reason: string }[];
  coverage: {
    scope: string;
    source_count: number;
    duplicate_count: number;
    timed_count: number;
    unlocated_count: number;
    relation_reason: string;
  };
  warnings: string[];
  next_cursor: string;
}

export async function getEventTimeline(id: string, cursor = ''): Promise<EventTimelineResponse> {
  const { data } = await client.get<EventTimelineResponse>(`/analyses/${id}/timeline`, {
    params: { limit: 50, ...(cursor ? { cursor } : {}) },
  });
  return data;
}

export interface Topic {
  name: string;
  doc_count: number;
  trend: string;
}

export async function createAnalysis(payload: CreateAnalysisPayload): Promise<AnalysisSummary> {
  const { data } = await client.post<AnalysisSummary>('/analyses', payload);
  return data;
}

export async function listAnalyses(): Promise<AnalysisListResponse> {
  const { data } = await client.get<AnalysisListResponse>('/analyses');
  return data;
}

export async function getAnalysis(id: string): Promise<AnalysisStateResponse> {
  const { data } = await client.get<AnalysisStateResponse>(`/analyses/${id}`);
  return data;
}

export async function getAnalysisResult(id: string): Promise<AnalysisResult> {
  const { data } = await client.get<AnalysisResult>(`/analyses/${id}/result`);
  return data;
}

export async function cancelAnalysis(id: string): Promise<AnalysisStateResponse> {
  const { data } = await client.post<AnalysisStateResponse>(`/analyses/${id}/cancel`);
  return data;
}

export async function rerunAnalysis(id: string): Promise<AnalysisStateResponse> {
  const { data } = await client.post<AnalysisStateResponse>(`/analyses/${id}/rerun`);
  return data;
}
