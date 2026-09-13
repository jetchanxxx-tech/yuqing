import client from './client';
import type { Topic } from './analyses';

/** GET /dashboard/overview */
export interface DashboardOverview {
  total_analyses: number;
  total_docs: number;
  sentiment_pos: number;
  sentiment_neg: number;
  sentiment_neu: number;
  success_rate: number;
  active_tasks: number;
}

/** GET /dashboard/trend */
export interface DashboardTrend {
  dates: string[];
  counts: number[];
  scores: number[];
}

/** GET /dashboard/sources */
export interface DashboardSources {
  sources: { name: string; count: number; pct: number }[];
}

/** GET /dashboard/topics → { topics: Topic[] } */
export interface DashboardTopics {
  topics: Topic[];
}

export async function getOverview(): Promise<DashboardOverview> {
  const { data } = await client.get<DashboardOverview>('/dashboard/overview');
  return data;
}

export async function getTrend(): Promise<DashboardTrend> {
  const { data } = await client.get<DashboardTrend>('/dashboard/trend');
  return data;
}

export async function getSources(): Promise<DashboardSources> {
  const { data } = await client.get<DashboardSources>('/dashboard/sources');
  return data;
}

export async function getTopics(): Promise<DashboardTopics> {
  const { data } = await client.get<DashboardTopics>('/dashboard/topics');
  return data;
}
