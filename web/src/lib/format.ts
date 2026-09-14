/** 展示层格式化工具 */

/** 2006-01-02T15:04:05Z / 2006-01-02 15:04:05 / 时间戳 → 本地可读时间 */
export function formatDateTime(value?: string | number | null): string {
  if (value === undefined || value === null || value === '') return '-';
  const t = typeof value === 'number' || /^\d{10,13}$/.test(String(value)) ? Number(value) : String(value);
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return String(value);
  return d.toLocaleString('zh-CN', { hour12: false });
}

/** 金额（元）→ ¥xx.xx；未提供返回 - */
export function formatCny(yuan?: number | null, digits = 2): string {
  if (yuan === undefined || yuan === null || Number.isNaN(yuan)) return '-';
  return `¥${yuan.toLocaleString('zh-CN', { minimumFractionDigits: digits, maximumFractionDigits: digits })}`;
}

/** 金额（分）→ ¥xx.xx（后端价格字段统一为分） */
export function formatCents(cents?: number | null, digits = 2): string {
  if (cents === undefined || cents === null || Number.isNaN(cents)) return '-';
  return formatCny(cents / 100, digits);
}

/** token 配额（单位：百万）→ 展示文案，如 1 → 100万 / 月 */
export function formatTokenQuota(million?: number | null): string {
  if (million === undefined || million === null) return '-';
  if (million <= 0) return '无上限';
  if (million >= 1000) return `${million / 1000}B tokens`;
  return `${million}M tokens`;
}

/** token 数量（个）→ 中文紧凑文案 */
export function formatTokens(n?: number | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '-';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n >= 100_000_000 ? 0 : 1)}M`;
  if (n >= 10_000) return `${Math.round(n / 10_000)}万`;
  return n.toLocaleString('zh-CN');
}

/** 千分位数字 */
export function formatNum(n?: number | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '-';
  return n.toLocaleString('zh-CN');
}

/** 0~100 情感占比 → 百分比文案 */
export function formatPercent(n?: number | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '-';
  return `${Math.round(n * 10) / 10}%`;
}
