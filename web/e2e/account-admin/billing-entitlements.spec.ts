import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';

const databaseURL = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const apiURL = 'http://127.0.0.1:8080/api/v1';
const password = 'K4-sandbox-billing-only-2026';

test('account and usage pages show the paid plan, true balance and unconfigured cycle', async ({ page, request }) => {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== databaseURL) {
    throw new Error('Billing acceptance requires the disposable GitHub hosted database.');
  }
  const email = `k4-billing-${Date.now()}@example.invalid`;
  const registered = await request.post(`${apiURL}/auth/register`, { data: { email, password, name: 'K4 billing entitlement' } });
  expect(registered.status()).toBe(201);
  const account = await registered.json();
  expect(account.user.tenant_id).toMatch(/^[0-9A-HJKMNP-TV-Z]{26}$/);
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-v', `tenant_id=${account.user.tenant_id}`], {
    input: "UPDATE report_credits SET plan_code='lite',balance=0 WHERE tenant_id=:'tenant_id';\n", stdio: ['pipe', 'pipe', 'pipe'],
  });
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto('/settings');
  await expect(page.getByText('速览版', { exact: true })).toBeVisible();
  await expect(page.getByText('报告余额：0 次', { exact: true })).toBeVisible();
  await expect(page.getByText('每次创建或重跑消耗 1 次报告额度', { exact: true })).toBeVisible();
  await page.goto('/usage');
  await expect(page.getByText('实际 Token 用量', { exact: true })).toBeVisible();
  await expect(page.getByText('统计周期未配置，当前按累计用量统计', { exact: true })).toBeVisible();
  await expect(page.getByText('报告余额', { exact: true })).toBeVisible();
  await expect(page.getByText('速览版', { exact: true })).toBeVisible();
  await expect(page.getByText('超额部分将按套餐费率计费')).toHaveCount(0);
  await page.screenshot({ path: 'test-results/account-admin/billing-entitlements.png', fullPage: true });
});
