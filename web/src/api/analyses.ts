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

/** GET /analyses/:id（轮询时可能出现的扩展字段） */
export interface AnalysisStateResponse {
  id: string;
  name: string;
  state: AnalysisState;
  progress: number;
  created_at?: string;
  error_message?: string;
}

export interface SentimentDoc {
  id: string;
  title: string;
  url: string;
  source_type: string;
  source_name: string;
  published_at: string;
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

export interface AnalysisResult {
  id: string;
  state: string;
  doc_count?: number;
  documents: SentimentDoc[];
  summary?: string;
  warning?: string;
  sentiments: { positive: number; negative: number; neutral: number; items?: SentimentItem[] };
  topics: Topic[];
  report?: AnalysisReport | null;
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
