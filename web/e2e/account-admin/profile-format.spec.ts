import { test, expect } from '@playwright/test';
import { calendarDateRange, formatDateTime } from '../../src/lib/format';

test('timezone formatting preserves source precision and UTC instants across spring and fall DST', () => {
  expect(formatDateTime('2026-03-08', 'America/New_York')).toBe('2026-03-08 (仅日期)');
  expect(formatDateTime('2026-03-08 01:15:00', 'Asia/Shanghai')).toBe('2026-03-08 01:15:00 (来源时区未知)');
  expect(formatDateTime('2026-03-08T06:59:59Z', 'America/New_York')).toBe('2026-03-08 01:59:59 (America/New_York)');
  expect(formatDateTime('2026-03-08T07:00:00Z', 'America/New_York')).toBe('2026-03-08 03:00:00 (America/New_York)');
  expect(formatDateTime('2026-11-01T05:59:59Z', 'America/New_York')).toBe('2026-11-01 01:59:59 (America/New_York)');
  expect(formatDateTime('2026-11-01T06:00:00Z', 'America/New_York')).toBe('2026-11-01 01:00:00 (America/New_York)');
  expect(formatDateTime('2026-03-08T00:30:00Z', 'America/New_York')).toBe('2026-03-07 19:30:00 (America/New_York)');
  expect(formatDateTime('2026-03-08T00:30:00Z', 'Asia/Shanghai')).toBe('2026-03-08 08:30:00 (Asia/Shanghai)');
  expect(formatDateTime(0, 'UTC')).toBe('1970-01-01 00:00:00 (UTC)');
});
test('saved-zone date ranges use next calendar midnight for 23 and 25 hour days', () => {
  expect(calendarDateRange('2026-03-08', '2026-03-08', 'America/New_York')).toEqual({ created_from: '2026-03-08T05:00:00.000Z', created_to: '2026-03-09T04:00:00.000Z' });
  expect(calendarDateRange('2026-11-01', '2026-11-01', 'America/New_York')).toEqual({ created_from: '2026-11-01T04:00:00.000Z', created_to: '2026-11-02T05:00:00.000Z' });
  expect(calendarDateRange('2026-03-08', '2026-03-08', 'Asia/Shanghai')).toEqual({ created_from: '2026-03-07T16:00:00.000Z', created_to: '2026-03-08T16:00:00.000Z' });
  expect(() => calendarDateRange('2026-02-30', undefined, 'UTC')).toThrow();
  expect(() => calendarDateRange('2026-03-09', '2026-03-08', 'UTC')).toThrow();
});
