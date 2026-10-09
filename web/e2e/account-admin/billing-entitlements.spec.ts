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

async function billingAccount(request: import('@playwright/test').APIRequestContext, label: string) {
  const email = `k4-${label}-${Date.now()}@example.invalid`;
  const result = await request.post(`${apiURL}/auth/register`, { data: { email, password, name: label } });
  expect(result.status()).toBe(201);
  return { ...await result.json(), email };
}
async function loginBilling(page: import('@playwright/test').Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}
function billingSQL(sql: string, variables: string[] = []) {
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', ...variables.flatMap((value) => ['-v', value])], { input: sql, stdio: 'pipe' });
}
async function refetchCachedBilling(page: import('@playwright/test').Page, path = '/settings') {
  await page.clock.setSystemTime(Date.now() + 120_000);
  // Application intentionally disables focus refetch. Real SPA navigation
  // remounts the stale query while preserving its QueryClient cache.
  await page.getByRole('menuitem', { name: '数据面板', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.getByRole('menuitem', { name: path === '/settings' ? '设置' : '用量', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`${path}$`));
}

test('Settings describes actual Pro retention and only connected verification capabilities', async ({ page, request }) => {
  const account = await billingAccount(request, 'truthful-help');
  billingSQL("UPDATE report_credits SET plan_code='pro' WHERE tenant_id=:'tenant_id';", [`tenant_id=${account.user.tenant_id}`]);
  await loginBilling(page, account.email);
  await page.goto('/settings');
  await expect.soft(page.getByText('当前套餐报告与原始文档保留 180 天', { exact: true })).toBeVisible();
  await expect.soft(page.getByText('客服渠道暂未配置', { exact: true })).toBeVisible();
  await expect.soft(page.getByText('support@yuqing.example.com', { exact: false })).toHaveCount(0);
  await page.getByRole('tab', { name: '个人资料', exact: true }).click();
  await expect.soft(page.getByText('验证邮箱后可解锁全部功能', { exact: false })).toHaveCount(0);
  await expect.soft(page.getByText('验证用于确认邮箱归属；功能与额度以当前套餐为准。', { exact: true })).toBeVisible();
  await page.getByRole('tab', { name: '手机绑定', exact: true }).click();
  await expect.soft(page.getByText('手机号登录、找回密码与支付验证暂未开放', { exact: true })).toBeVisible();
});

test('Settings rejects stale cached credit values after a real database read failure and retries', async ({ page, request }) => {
  const account = await billingAccount(request, 'credit-retry');
  await page.clock.install();
  await loginBilling(page, account.email);
  await page.goto('/settings');
  await expect(page.getByText('报告余额：1 次', { exact: true })).toBeVisible();
  billingSQL('ALTER TABLE report_credits RENAME COLUMN balance TO balance_k4_read_failure;');
  try {
    const failed = page.waitForResponse((response) => response.url().endsWith('/billing/credits') && response.status() === 500);
    await refetchCachedBilling(page);
    await failed;
    await expect(page.getByRole('alert').filter({ hasText: '报告额度暂不可用' })).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText('报告余额：1 次', { exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: '重试报告额度', exact: true })).toBeVisible();
    expect(await page.evaluate(() => Boolean(localStorage.getItem('access_token')))).toBe(true);
  } finally {
    billingSQL('ALTER TABLE report_credits RENAME COLUMN balance_k4_read_failure TO balance;');
  }
  await page.getByRole('button', { name: '重试报告额度', exact: true }).click();
  await expect(page.getByText('报告余额：1 次', { exact: true })).toBeVisible();
});

for (const path of ['/settings', '/usage']) {
  test(`${path} exposes catalog transport failure and retries the real API`, async ({ page, request }) => {
    const account = await billingAccount(request, `catalog-${path.slice(1)}`);
    billingSQL("UPDATE report_credits SET plan_code='lite' WHERE tenant_id=:'tenant_id';", [`tenant_id=${account.user.tenant_id}`]);
    await page.clock.install();
    await loginBilling(page, account.email);
    await page.goto(path);
    await expect(page.getByText('速览版', { exact: true })).toBeVisible();
    // Failure-only transport injection; successful data always comes from real Go/PG.
    let aborted = 0;
    await page.route('**/api/v1/billing/plans', (route) => { aborted += 1; return route.abort('connectionfailed'); });
    await refetchCachedBilling(page, path);
    await expect.poll(() => aborted).toBeGreaterThan(0);
    await expect(page.getByRole('alert').filter({ hasText: '套餐目录暂不可用' })).toBeVisible({ timeout: 15_000 });
    await expect(page.getByText('速览版', { exact: true })).toHaveCount(0);
    await page.unroute('**/api/v1/billing/plans');
    await page.getByRole('button', { name: '重试套餐目录', exact: true }).click();
    await expect(page.getByText('速览版', { exact: true })).toBeVisible();
  });
}

test('fixed account displays unlimited use with zero actual balance and pending provider cost', async ({ page, request }) => {
  const account = await billingAccount(request, 'fixed-entitlement-view');
  billingSQL("INSERT INTO billing_exempt_principals(policy_key,user_id,bound_by) VALUES('fixed_admin_v1',:'user_id','isolated-browser-fixture'); UPDATE report_credits SET balance=0 WHERE tenant_id=:'tenant_id'; INSERT INTO usage_events(tenant_id,user_id,model,prompt_tokens,completion_tokens,cache_tokens,quota_tokens,billing_exempt,usage_status,cost_status,event_version) VALUES(:'tenant_id',:'user_id','unknown-sandbox',0,0,0,0,true,'unknown','pending',1);", [`tenant_id=${account.user.tenant_id}`, `user_id=${account.user.user_id}`]);
  await loginBilling(page, account.email);
  await page.goto('/settings');
  await expect(page.getByText('报告余额：0 次', { exact: true })).toBeVisible();
  await expect(page.getByText('当前固定账号免次数与消费限制；团队其他成员按套餐计费', { exact: true })).toBeVisible();
  await page.goto('/usage');
  await expect(page.getByText('当前固定账号免次数与消费限制', { exact: true })).toBeVisible();
  await expect(page.getByText('供应商成本仍有待核对记录', { exact: true })).toBeVisible();
  await expect(page.getByText('当前账号不扣报告次数', { exact: true })).toBeVisible();
});
