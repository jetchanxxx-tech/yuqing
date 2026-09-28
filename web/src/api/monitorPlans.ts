import client from './client';
import type { AnalysisType } from '../lib/constants';

export type ConfigKey = 'keywords' | 'exclude_words' | 'sources' | 'monitoring_cycle' | 'risk_tags' | 'alert_rules' | 'report_template';
export type Candidate = { state: 'proposed' | 'unavailable'; value?: unknown; source: string; reason?: string };
export type DraftConfig = Record<ConfigKey, Candidate>;

export interface Template {
  template_id: string;
  template_version: number;
  name: string;
  default_analysis_type: AnalysisType;
}

export interface PreviewRequest {
  template_id: string;
  template_version: number;
  analysis_type?: AnalysisType;
  inputs: Record<string, string>;
}

export interface Preview {
  analysis_type_resolution: { analysis_type: AnalysisType; source: string; explanation: string };
  config: DraftConfig;
  warnings: string[];
}

export interface DraftBody extends PreviewRequest {
  name: string;
  analysis_type: AnalysisType;
  config: DraftConfig;
  state: 'draft';
}

export interface MonitorPlan extends DraftBody {
  plan_id: string;
  tenant_id: string;
  owner_id: string;
  revision: number;
  created_at?: string;
  updated_at?: string;
}

export const CONFIG_KEYS: ConfigKey[] = ['keywords', 'exclude_words', 'sources', 'monitoring_cycle', 'risk_tags', 'alert_rules', 'report_template'];

export async function listTemplates(): Promise<Template[]> {
  const { data } = await client.get<{ templates: Template[] }>('/monitor-plans/templates');
  return data.templates;
}

export async function previewMonitorPlan(body: PreviewRequest): Promise<Preview> {
  const { data } = await client.post<Preview>('/monitor-plans/preview', body);
  return data;
}

export async function createMonitorPlan(body: DraftBody): Promise<MonitorPlan> {
  const { data } = await client.post<MonitorPlan>('/monitor-plans', body);
  return data;
}

export async function listMonitorPlans(offset = 0): Promise<{ plans: MonitorPlan[]; total: number }> {
  const { data } = await client.get<{ plans: MonitorPlan[]; total: number }>('/monitor-plans', { params: { limit: 20, offset } });
  return data;
}

export async function getMonitorPlan(id: string): Promise<MonitorPlan> {
  const { data } = await client.get<MonitorPlan>(`/monitor-plans/${encodeURIComponent(id)}`);
  return data;
}

export async function patchMonitorPlan(id: string, revision: number, body: Pick<DraftBody, 'name' | 'analysis_type' | 'inputs' | 'config' | 'state'>): Promise<MonitorPlan> {
  const { data } = await client.patch<MonitorPlan>(`/monitor-plans/${encodeURIComponent(id)}`, { revision, ...body });
  return data;
}
