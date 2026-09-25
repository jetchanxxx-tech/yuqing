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

export async function listReports(): Promise<ReportListResponse> {
  const { data } = await client.get<ReportListResponse>('/reports');
  return data;
}

/**
 * 下载报告文件流（P1-3 Solution A：直接消费后端返回的文件流，不再期待 download_url）。
 * 后端直接返回文件内容（application/octet-stream），前端用 Blob + createObjectURL 处理。
 */
export async function downloadReport(id: string, format: ReportFormat): Promise<Blob> {
  const { data } = await client.get<Blob>(`/reports/${id}/download`, {
    params: { format },
    responseType: 'blob', // 关键：告诉 axios 返回 Blob 而不是 JSON
  });
  return data;
}

/**
 * 下载报告并触发浏览器保存（新方案：直接消费文件流）。
 * 使用 Blob + createObjectURL + <a> 下载技巧。
 */
export async function openReportDownload(id: string, format: ReportFormat): Promise<void> {
  try {
    const blob = await downloadReport(id, format);

    // 创建临时 URL
    const url = window.URL.createObjectURL(blob);

    // 创建隐藏的 <a> 标签触发下载
    const a = document.createElement('a');
    a.style.display = 'none';
    a.href = url;
    a.download = `report-${id}.${format}`; // 文件名
    document.body.appendChild(a);
    a.click();

    // 清理
    document.body.removeChild(a);
    window.URL.revokeObjectURL(url);
  } catch (err) {
    throw err;
  }
}
