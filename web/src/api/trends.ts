import client from './client';

/** 热榜条目（F21：排名 + 标题 + 链接 + 可选热度值） */
export interface TrendItem {
  rank: number;
  title: string;
  url: string;
  hot?: string;
}

/** 单平台热榜快照（三态：ok / stale / error） */
export interface TrendPlatform {
  name: string;
  items: TrendItem[];
  status: 'ok' | 'stale' | 'error';
  error?: string;
  /** 最后一次成功抓取时间（stale 时保留旧值） */
  updated_at?: string;
}

/** GET /api/v1/trends 响应 */
export interface TrendsResponse {
  platforms: TrendPlatform[];
}

export async function getTrends(): Promise<TrendPlatform[]> {
  const { data } = await client.get<{ platforms: TrendPlatform[] }>('/trends');
  return data.platforms ?? [];
}
