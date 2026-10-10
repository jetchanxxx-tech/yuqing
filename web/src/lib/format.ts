/** 展示层格式化工具 */

import { createContext, useCallback, useContext } from 'react';

export const DEFAULT_TIMEZONE = 'Asia/Shanghai';
export const TimezoneContext = createContext(DEFAULT_TIMEZONE);
export function useDateTime() {
  const timezone = useContext(TimezoneContext);
  return useCallback((value?: string | number | null) => formatDateTime(value, timezone), [timezone]);
}

/** Only explicit instants are shifted. Source dates/unknown-zone text retain precision. */
export function formatDateTime(value?: string | number | null, timezone = DEFAULT_TIMEZONE): string {
  if (value === undefined || value === null || value === '') return '-';
  const raw = String(value);
  if (typeof value === 'string' && !/^\d{10}(?:\d{3})?$/.test(raw) && !/T.*(?:Z|[+-]\d{2}:\d{2})$/i.test(raw)) {
    return `${raw} (${ /^\d{4}-\d{2}-\d{2}$/.test(raw) ? '仅日期' : '来源时区未知' })`;
  }
  const numeric = typeof value === 'number' || /^\d{10}(?:\d{3})?$/.test(raw);
  const instant = new Date(numeric ? (Math.abs(Number(value)) < 1e11 ? Number(value) * 1000 : Number(value)) : raw);
  if (Number.isNaN(instant.getTime())) return raw;
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).formatToParts(instant);
  const p = Object.fromEntries(parts.map(({ type, value }) => [type, value]));
  return `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}:${p.second} (${timezone})`;
}

/** Find the first UTC instant on a saved-zone calendar day. Binary search also
 * handles zones with midnight transitions, and rejects entirely skipped days. */
export function calendarDayStart(date: string, timezone: string): string {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) throw new Error('日期无效');
  const center = Date.parse(`${date}T00:00:00Z`);
  if (!Number.isFinite(center) || new Date(center).toISOString().slice(0, 10) !== date) throw new Error('日期无效');
  const formatter = new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' });
  const day = (ms: number) => {
    const p = Object.fromEntries(formatter.formatToParts(new Date(ms)).map(({ type, value }) => [type, value]));
    return `${p.year}-${p.month}-${p.day}`;
  };
  let low = center - 36 * 3600000, high = center + 36 * 3600000;
  while (low < high) { const mid = Math.floor((low + high) / 2); if (day(mid) < date) low = mid + 1; else high = mid; }
  if (day(low) !== date) throw new Error('所选时区不存在该日期');
  return new Date(low).toISOString();
}
export function calendarDateRange(from: string | undefined, through: string | undefined, timezone: string) {
  const created_from = from ? calendarDayStart(from, timezone) : undefined;
  let created_to: string | undefined;
  if (through) {
    calendarDayStart(through, timezone);
    const next = new Date(`${through}T00:00:00Z`); next.setUTCDate(next.getUTCDate() + 1);
    created_to = calendarDayStart(next.toISOString().slice(0, 10), timezone);
  }
  if (created_from && created_to && created_from >= created_to) throw new Error('开始日期不能晚于结束日期');
  return { created_from, created_to };
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
