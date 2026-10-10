import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';

const db = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const api = 'http://127.0.0.1:8080/api/v1';
const password = 'K8-profile-password-2026';
function sql(statement: string, values: Record<string, string>) {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== db) throw new Error('Hosted disposable database required');
  return execFileSync('psql', [db, '-v', 'ON_ERROR_STOP=1', '-A', '-t', ...Object.entries(values).flatMap(([k, v]) => ['-v', `${k}=${v}`])], { input: statement, encoding: 'utf8' }).trim();
}
async function account(request: APIRequestContext) {
  const email = `k8-${randomBytes(8).toString('hex')}@example.invalid`;
  const r = await request.post(`${api}/auth/register`, { data: { email, password, name: '时区头像用户' } });
  expect(r.status()).toBe(201);
  return { ...await r.json(), email };
}
async function login(page: Page, email: string) {
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}
async function timezone(page: Page, zone: string) {
  await page.goto('/settings');
  await page.getByRole('tab', { name: '个人资料' }).click();
  await page.getByLabel('时区', { exact: true }).focus();
  await page.getByLabel('时区', { exact: true }).press('ArrowDown');
  await page.getByText(zone, { exact: true }).last().click();
  await page.getByRole('button', { name: '保存更改' }).click();
  await expect(page.getByText('资料已保存', { exact: true })).toBeVisible();
}

test.use({ timezoneId: 'Europe/London' });
test('saved timezone controls actual analysis report order and security timestamps after reload across DST and UTC day', async ({ page, request }) => {
  const a = await account(request);
  const uid = a.user.user_id, tid = a.user.tenant_id;
  sql("INSERT INTO platform_user_roles(user_id,role) VALUES (:'uid','platform_admin');", { uid });
  const before = '2026-03-08T06:59:59Z', after = '2026-03-08T07:00:00Z', day = '2026-03-08T00:30:00Z';
  sql(`INSERT INTO analyses(id,tenant_id,created_by,name,state,created_at) VALUES (:'aid',:'tid',:'uid','K8 DST analysis','completed',:'at');
       INSERT INTO reports(id,tenant_id,created_by,analysis_id,format,status,created_at) VALUES (:'rid',:'tid',:'uid',:'aid','html','completed',:'after');
       UPDATE users SET password_changed_at=:'day' WHERE id=:'uid';`, { uid, tid, aid: `k8-a-${uid}`, rid: `k8-r-${uid}`, at: before, after, day });
  sql("INSERT INTO orders(id,tenant_id,sku_code,kind,credits,amount_cents,channel,state,expires_at,created_at) VALUES (:'id',:'tid','lite','plan',10,1000,'alipay','closed',:'at',:'at');", { id: `k8-o-${uid}`, tid, at: day });
  await login(page, a.email);
  await timezone(page, 'America/New_York');
  await page.reload();
  await expect(page.getByText('America/New_York', { exact: true }).first()).toBeVisible();
  await page.goto('/analyses');
  await expect(page.getByText('2026-03-08 01:59:59 (America/New_York)', { exact: true })).toBeVisible();
  await page.goto('/reports');
  await expect(page.getByText('2026-03-08 03:00:00 (America/New_York)', { exact: true })).toBeVisible();
  await page.goto('/admin');
  await page.getByRole('tab', { name: '租户与套餐管理' }).click();
  await page.getByLabel('搜索租户', { exact: true }).fill(tid);
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  await page.getByRole('row').filter({ hasText: tid }).getByRole('button', { name: '查看详情', exact: true }).click();
  await expect(page.getByRole('dialog').getByText('2026-03-07 19:30:00 (America/New_York)', { exact: true })).toBeVisible();
  await page.goto('/settings');
  await page.getByRole('tab', { name: '账户安全' }).click();
  await expect(page.getByText('上次修改：2026-03-07 19:30:00 (America/New_York)', { exact: true })).toBeVisible();
  await timezone(page, 'Asia/Shanghai');
  await page.goto('/analyses');
  await expect(page.getByText('2026-03-08 14:59:59 (Asia/Shanghai)', { exact: true })).toBeVisible();
  expect(sql("SELECT to_char(password_changed_at AT TIME ZONE 'UTC','YYYY-MM-DD HH24:MI:SS') FROM users WHERE id=:'uid';", { uid })).toBe('2026-03-08 00:30:00');
});

test('real avatar upload survives reload, rejects invalid bytes and removes only current owner image', async ({ page, request }) => {
  const a = await account(request);
  await login(page, a.email);
  await page.goto('/settings');
  await page.getByRole('tab', { name: '个人资料' }).click();
  // Browser canvas generates a real PNG; no supplier or remote image fetch.
  const png = await page.evaluate(() => { const c = document.createElement('canvas'); c.width = 4; c.height = 4; c.getContext('2d')!.fillRect(0, 0, 4, 4); return c.toDataURL().split(',')[1]; });
  await page.getByLabel('上传头像', { exact: true }).setInputFiles({ name: 'avatar.png', mimeType: 'image/png', buffer: Buffer.from(png, 'base64') });
  await expect(page.getByRole('img', { name: '个人头像', exact: true })).toBeVisible();
  const url = await page.getByRole('img', { name: '个人头像', exact: true }).getAttribute('src');
  await page.reload();
  await page.getByRole('tab', { name: '个人资料' }).click();
  await expect(page.getByRole('img', { name: '个人头像', exact: true })).toHaveAttribute('src', url!);
  const headers = { Authorization: `Bearer ${a.access_token}` };
  const bad = await request.post(`${api}/user/avatar`, { headers, multipart: { avatar: { name: 'evil.png', mimeType: 'image/png', buffer: Buffer.from('<svg onload="evil()"/>') } } });
  expect(bad.status()).toBe(400);
  expect((await (await request.get(`${api}/user/profile`, { headers })).json()).avatar_url).toBe(url);
  await page.getByRole('button', { name: '移除头像', exact: true }).click();
  await expect(page.getByRole('img', { name: '个人头像', exact: true })).toHaveCount(0);
  expect((await request.get(`http://127.0.0.1:8080${url}`)).status()).toBe(404);
});

test('administrator calendar dates use saved-zone DST boundaries and real server totals', async ({ page, request }) => {
  const a = await account(request);
  sql("INSERT INTO platform_user_roles(user_id,role) VALUES (:'uid','platform_admin');", { uid: a.user.user_id });
  const fixture = `K8 date ${randomBytes(5).toString('hex')}`;
  const prefix = randomBytes(8).toString('hex');
  for (const [index, at] of ['2026-03-08T04:59:59Z', '2026-03-08T05:00:00Z', '2026-03-09T03:59:59Z', '2026-03-09T04:00:00Z'].entries()) {
    sql("INSERT INTO users(id,email,password_hash,name,status,created_at) VALUES (:'id',:'email','fixture-not-used',:'name','active',:'at');", { id: `${prefix}-${index}`, email: `${prefix}-${index}@example.invalid`, name: fixture, at });
  }
  await login(page, a.email);
  await timezone(page, 'America/New_York');
  await page.reload();
  await page.goto('/admin');
  await page.getByLabel('搜索用户', { exact: true }).fill(fixture);
  await page.getByLabel('创建开始日期', { exact: true }).fill('2026-03-08');
  await page.getByLabel('创建结束日期', { exact: true }).fill('2026-03-08');
  const response = page.waitForResponse(r => r.url().includes('/admin/users?') && r.url().includes('created_from='));
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  const r = await response;
  expect(r.status()).toBe(200);
  const url = new URL(r.url());
  expect(url.searchParams.get('created_from')).toBe('2026-03-08T05:00:00.000Z');
  expect(url.searchParams.get('created_to')).toBe('2026-03-09T04:00:00.000Z');
  expect((await r.json()).total).toBe(2);
  await expect(page.getByText(`${prefix}-1`, { exact: true })).toBeVisible();
  await expect(page.getByText(`${prefix}-2`, { exact: true })).toBeVisible();
  await expect(page.getByText(`${prefix}-0`, { exact: true })).toHaveCount(0);
  await expect(page.getByText(`${prefix}-3`, { exact: true })).toHaveCount(0);
});
