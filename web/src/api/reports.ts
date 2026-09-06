import client from './client';
import type { ReportFormat } from '../lib/constants';

export interface Report {
  id: string;
  analysis_id: string;
  title: string;
  format: string;
  status: string;
  created_at?: string;
}

export interface ReportListResponse {
  reports: Report[];
  total: number;
}

export interface ReportDownloadResponse {
  download_url: string;
}

export async function listReports(): Promise<ReportListResponse> {
  const { data } = await client.get<ReportListResponse>('/reports');
  return data;
}

/**
 * 返回报告的下载地址。download_url 若为相对路径则拼上 API 前缀。
 */
export async function downloadReport(id: string, format: ReportFormat): Promise<string> {
  const { data } = await client.get<ReportDownloadResponse>(`/reports/${id}/download`, {
    params: { format },
  });
  const url = data.download_url;
  return /^(https?:)?\/\//.test(url) ? url : `/api/v1${url.startsWith('/') ? '' : '/'}${url}`;
}

/**
 * 跳转下载（新窗口打开）。
 * 先同步创建空白窗口（在用户手势内，避免被弹窗拦截），
 * 拿到 download_url 后再写入地址；失败则关闭空白窗口并抛出。
 */
export async function openReportDownload(id: string, format: ReportFormat): Promise<void> {
  const win = window.open('', '_blank');
  try {
    const url = await downloadReport(id, format);
    if (win) {
      win.location.href = url;
    } else {
      window.open(url, '_blank');
    }
  } catch (err) {
    win?.close();
    throw err;
  }
}
