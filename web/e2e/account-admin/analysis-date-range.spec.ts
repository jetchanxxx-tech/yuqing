import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { randomBytes } from 'node:crypto';

const api = 'http://127.0.0.1:8080/api/v1';
const password = 'DateRange-hosted-sandbox-only-2026!';

async function newAnalysis(page: Page, request: APIRequestContext) {
  const email = `date-window-${randomBytes(7).toString('hex')}@example.invalid`;
  const registered = await request.post(`${api}/auth/register`, { data: { email, password, name: '日期范围验收' } });
  expect(registered.status()).toBe(201);
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto('/analyses/new');
  await page.getByPlaceholder('例如：新品发布会舆情监测').fill('窗口品牌日期范围验收');
  await page.getByRole('button', { name: '手动选择视角', exact: true }).click();
  await page.getByRole('button', { name: '下一步', exact: true }).click();
  await page.getByRole('combobox').fill('窗口品牌');
  await page.getByRole('combobox').press('Enter');
}

async function confirmAnalysis(page: Page) {
  await page.getByRole('button', { name: '下一步', exact: true }).click();
  await page.getByRole('checkbox', { name: '微博', exact: true }).check();
  await page.getByRole('button', { name: '下一步', exact: true }).click();
}

test('date calendar defaults to seven days and excludes future dates', async ({ page, request }) => {
  await newAnalysis(page, request);
  const today = new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai' }).format(new Date());
  const beginning = new Date(`${today}T00:00:00Z`);
  beginning.setUTCDate(beginning.getUTCDate() - 6);
  await expect(page.getByPlaceholder('开始日期', { exact: true })).toHaveValue(beginning.toISOString().slice(0, 10));
  await expect(page.getByPlaceholder('结束日期', { exact: true })).toHaveValue(today);
  await page.getByPlaceholder('开始日期', { exact: true }).click();
  await expect(page.locator('.ant-picker-dropdown:visible')).toBeVisible();
  await expect(page.locator('.ant-picker-dropdown:visible')).toHaveCSS('opacity', '1');
  const future = new Date(`${today}T00:00:00Z`);
  future.setUTCDate(future.getUTCDate() + 1);
  await expect(page.locator(`.ant-picker-dropdown:visible .ant-picker-cell-in-view[title="${future.toISOString().slice(0, 10)}"]`).first()).toHaveClass(/ant-picker-cell-disabled/);
  await page.screenshot({ path: 'test-results/account-admin/analysis-date-calendar-desktop.png' });
});

test('custom calendar range reaches create API and survives reload in task details', async ({ page, request }) => {
  await newAnalysis(page, request);
  await page.getByPlaceholder('开始日期', { exact: true }).fill('2026-09-10');
  await page.getByPlaceholder('开始日期', { exact: true }).press('Tab');
  await page.getByPlaceholder('结束日期', { exact: true }).fill('2026-09-20');
  await page.getByPlaceholder('结束日期', { exact: true }).press('Enter');
  await expect(page.getByPlaceholder('开始日期', { exact: true })).toHaveValue('2026-09-10');
  await expect(page.getByPlaceholder('结束日期', { exact: true })).toHaveValue('2026-09-20');
  await page.keyboard.press('Escape');
  await confirmAnalysis(page);
  await expect(page.getByText('2026-09-10 至 2026-09-20', { exact: true })).toBeVisible();
  const response = page.waitForResponse(value => value.url() === `${new URL(page.url()).origin}/api/v1/analyses` && value.request().method() === 'POST');
  await page.getByRole('button', { name: '提交分析', exact: true }).click();
  const created = await response;
  expect(created.status()).toBe(201);
  expect(created.request().postDataJSON()).toMatchObject({ date_from: '2026-09-10', date_to: '2026-09-20' });
  await expect(page).toHaveURL(/\/analyses\/[^/]+$/);
  await expect(page.getByText('2026-09-10 至 2026-09-20', { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByText('2026-09-10 至 2026-09-20', { exact: true })).toBeVisible();
});

test('mobile calendar stays within the viewport and a cleared range cannot submit', async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await newAnalysis(page, request);
  const calendar = page.getByPlaceholder('开始日期', { exact: true });
  await calendar.click();
  const popup = page.locator('.ant-picker-dropdown:visible');
  await expect(popup).toBeVisible();
  await expect(popup).toHaveCSS('opacity', '1');
  const bounds = await popup.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.width).toBeLessThanOrEqual(390);
  expect(bounds!.x).toBeGreaterThanOrEqual(-1);
  await page.screenshot({ path: 'test-results/account-admin/analysis-date-calendar-mobile.png' });
  await calendar.press('Tab');
  const ending = page.getByPlaceholder('结束日期', { exact: true });
  await ending.press('Tab');
  await expect(popup).toBeHidden();
  await page.locator('.analysis-date-range-picker').hover();
  await page.locator('.analysis-date-range-picker .ant-picker-clear').click();
  await expect(calendar).toHaveValue('');
  await expect(ending).toHaveValue('');
  await expect(page.getByRole('button', { name: '下一步', exact: true })).toBeDisabled();
});
