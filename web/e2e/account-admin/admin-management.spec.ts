import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// This suite intentionally uses the actual PostgreSQL API and built frontend.
// Missing authorization, stale CAS, or a missing UI action must fail acceptance.
// There are no mocked routes, real mail recipients, or production endpoints.
const workspace = fileURLToPath(new URL('../../../', import.meta.url));
const apiURL = 'http://127.0.0.1:8080/api/v1';
const databaseURL = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const password = 'K3-cloud-acceptance-only-2026';
const runID = `${Date.now()}-${process.pid}`;
const ulid = /^[0-9A-HJKMNP-TV-Z]{26}$/;

interface Session {
  access_token: string;
  refresh_token: string;
  user: { user_id: string; tenant_id: string; email: string; roles: string[]; plan_code: string; tenant_status: string };
}
interface Account extends Session { name: string; }
interface UserDetailDTO {
  id: string; status: string; platform_roles: string[]; row_version: number;
  memberships: { tenant_id: string; tenant_name: string; role: string; row_version: number }[];
  audit_logs: { action: string; reason: string }[];
}
interface TenantDetailDTO {
  id: string; status: string; row_version: number; user_count: number;
  members: { user_id: string; name: string; email: string; role: string; row_version: number }[];
  orders: unknown[]; audit_logs: { action: string; reason: string }[];
}

let admin: Account;
let ordinary: Account;

function requireRunner() {
  if (process.env.GITHUB_ACTIONS !== 'true' || process.env.RUNNER_ENVIRONMENT !== 'github-hosted'
      || process.env.RUNNER_OS !== 'Linux' || process.env.YUQING_TEST_PG_URL !== databaseURL
      || !process.env.YUQING_E2E_CONFIG) {
    throw new Error('K3 fixtures require the GitHub hosted Linux runner and isolated account/admin test database.');
  }
}

async function register(request: APIRequestContext, label: string): Promise<Account> {
  const name = `K3 ${label} ${runID}`;
  const email = `k3-${label}-${runID}@example.invalid`;
  const response = await request.post(`${apiURL}/auth/register`, { data: { name, email, password } });
  expect(response.status(), 'real fixture registration').toBe(201);
  const session = await response.json() as Session;
  expect(session.user.user_id).toMatch(ulid);
  expect(session.user.tenant_id).toMatch(ulid);
  return { ...session, name };
}

async function sharedFixture(request: APIRequestContext, label: string): Promise<Account> {
  const email = `k3-shared-${label}@example.invalid`;
  const name = `K3 shared ${label}`;
  const existing = await request.post(`${apiURL}/auth/login`, { data: { email, password } });
  if (existing.status() === 200) return { ...await existing.json() as Session, name };
  expect(existing.status()).toBe(401);
  const created = await request.post(`${apiURL}/auth/register`, { data: { name, email, password } });
  expect(created.status()).toBe(201);
  return { ...await created.json() as Session, name };
}

async function login(request: APIRequestContext, account: Account): Promise<Session> {
  const response = await request.post(`${apiURL}/auth/login`, { data: { email: account.user.email, password } });
  expect(response.status()).toBe(200);
  return response.json() as Promise<Session>;
}

async function api(request: APIRequestContext, path: string, session: Session = admin) {
  return request.get(`${apiURL}${path}`, { headers: { Authorization: `Bearer ${session.access_token}` } });
}

async function userDetail(request: APIRequestContext, id: string): Promise<UserDetailDTO> {
  const response = await api(request, `/admin/users/${id}`);
  expect(response.status()).toBe(200);
  return response.json() as Promise<UserDetailDTO>;
}

async function tenantDetail(request: APIRequestContext, id: string): Promise<TenantDetailDTO> {
  const response = await api(request, `/admin/tenants/${id}`);
  expect(response.status()).toBe(200);
  return response.json() as Promise<TenantDetailDTO>;
}

async function mutate(request: APIRequestContext, path: string, body: Record<string, unknown>, method = 'POST') {
  return request.fetch(`${apiURL}${path}`, {
    method, headers: { Authorization: `Bearer ${admin.access_token}` }, data: body,
  });
}

async function signIn(page: Page, account: Account = admin) {
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(account.user.email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto('/admin');
}

async function openUser(page: Page, account: Account) {
  await page.getByRole('tab', { name: '用户账号管理', exact: true }).click();
  await page.getByPlaceholder('搜索邮箱、昵称或用户 ID').fill(account.user.email);
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  const row = page.getByRole('row').filter({ hasText: account.user.email });
  await expect(row).toHaveCount(1);
  await row.getByRole('button', { name: '查看详情', exact: true }).click();
  await expect(page.getByRole('dialog', { name: '用户详情', exact: true })).toBeVisible();
}

async function openTenant(page: Page, account: Account) {
  await page.getByRole('tab', { name: '租户与套餐管理', exact: true }).click();
  await page.getByPlaceholder('搜索租户名称或 ID').fill(account.user.tenant_id);
  await page.getByRole('button', { name: '搜索', exact: true }).click();
  const row = page.getByRole('row').filter({ hasText: account.user.tenant_id });
  await expect(row).toHaveCount(1);
  await row.getByRole('button', { name: '查看详情', exact: true }).click();
  await expect(page.getByRole('dialog', { name: '租户详情', exact: true })).toBeVisible();
}

async function confirmAction(page: Page, action: string, path: string, reason: string, status = 200): Promise<void> {
  await page.getByRole('button', { name: action, exact: true }).click();
  const dialog = page.getByRole('dialog', { name: action, exact: true });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel('操作原因', { exact: true }).fill(reason);
  const responsePromise = page.waitForResponse((response) => response.url().endsWith(path)
    && response.request().method() !== 'GET');
  const mePromise = status === 200 ? page.waitForResponse((response) => response.url().endsWith('/auth/me')) : undefined;
  await dialog.getByRole('button', { name: '确认操作', exact: true }).click();
  const response = await responsePromise;
  expect(response.status()).toBe(status);
  expect(response.request().postDataJSON()).toMatchObject({ reason, expected_version: expect.any(Number) });
  if (mePromise) expect((await mePromise).status()).toBe(200);
}

test.setTimeout(120_000);

test.beforeAll(async ({ request }) => {
  requireRunner();
  console.info('Account/admin fixture: registering initial operator');
  admin = await sharedFixture(request, 'initial-admin');
  console.info('Account/admin fixture: initializing verified operator ID');
  if (!admin.user.roles.includes('platform_admin')) execFileSync(resolve(workspace, 'platform/bin/yuqing-cli'), [
    'bootstrap-platform-admin', '--user-id', admin.user.user_id,
  ], {
    cwd: resolve(workspace, 'platform'),
    env: { ...process.env, YUQING_CONFIG: process.env.YUQING_E2E_CONFIG },
    stdio: 'pipe',
    timeout: 30_000,
  });
  console.info('Account/admin fixture: checking current operator login');
  admin = { ...admin, ...await login(request, admin) };
  expect(admin.user.roles).toContain('platform_admin');
  ordinary = await sharedFixture(request, 'ordinary');
  console.info('Account/admin fixture: ready');
});

test('server confirmed platform administrators get both entries and actual server pagination', async ({ page, request }) => {
  for (let index = 0; index < 20; index += 1) await register(request, `page-${index}`);
  await signIn(page);
  await expect(page.getByRole('menuitem', { name: '管理后台', exact: true })).toBeVisible();
  await expect(page.getByRole('tab', { name: '用户账号管理', exact: true })).toBeVisible();
  await expect(page.getByRole('tab', { name: '租户与套餐管理', exact: true })).toBeVisible();
  const pageTwo = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return url.pathname === '/api/v1/admin/users' && url.searchParams.get('page') === '2';
  });
  await page.getByTitle('2', { exact: true }).click();
  const response = await pageTwo;
  expect(response.status()).toBe(200);
  const secondPage = await response.json() as { page: number; page_size: number; total: number; items: unknown[] };
  expect(secondPage).toMatchObject({ page: 2, page_size: 20 });
  expect(secondPage.total).toBeGreaterThanOrEqual(22);
  expect(secondPage.items.length).toBeGreaterThan(0);
  await openUser(page, ordinary);
  const detail = page.getByRole('dialog', { name: '用户详情', exact: true });
  await expect(detail.getByText(ordinary.user.user_id, { exact: true })).toBeVisible();
  await expect(detail.getByRole('heading', { name: '平台角色', exact: true })).toBeVisible();
  await expect(detail.getByRole('heading', { name: '团队成员角色', exact: true })).toBeVisible();
  await expect(detail.getByText(ordinary.user.tenant_id, { exact: true })).toBeVisible();
  await expect(detail.getByText('tenant_admin', { exact: true })).toBeVisible();
  await expect(detail.getByText(/设备|邀请/)).toHaveCount(0);
});

test('account disable and enable require a reason, save audit, and revoke old credentials', async ({ page, request }) => {
  const account = await register(request, 'account-state');
  await signIn(page);
  await openUser(page, account);
  const before = await userDetail(request, account.user.user_id);
  let disableRequests = 0;
  page.on('request', (sent) => {
    if (sent.url().endsWith(`/admin/users/${account.user.user_id}/disable`)) disableRequests += 1;
  });
  await page.getByRole('button', { name: '禁用账号', exact: true }).click();
  const emptyReason = page.getByRole('dialog', { name: '禁用账号', exact: true });
  await emptyReason.getByRole('button', { name: '确认操作', exact: true }).click();
  await expect(emptyReason.getByText('请输入操作原因', { exact: true })).toBeVisible();
  expect(disableRequests).toBe(0);
  await emptyReason.getByRole('button', { name: '取消', exact: true }).click();
  await confirmAction(page, '禁用账号', `/admin/users/${account.user.user_id}/disable`, 'K3 禁用原因');
  const disabled = await userDetail(request, account.user.user_id);
  expect(disabled).toMatchObject({ status: 'disabled', row_version: before.row_version + 1 });
  expect(disabled.audit_logs).toContainEqual(expect.objectContaining({ action: 'user.disable', reason: 'K3 禁用原因' }));
  expect((await api(request, '/auth/me', account)).status()).toBe(401);
  await expect(page.getByRole('dialog', { name: '用户详情', exact: true }).getByText('已禁用', { exact: true })).toBeVisible();
  await confirmAction(page, '启用账号', `/admin/users/${account.user.user_id}/enable`, 'K3 启用原因');
  expect((await userDetail(request, account.user.user_id)).status).toBe('active');
  expect((await api(request, '/auth/me', account)).status()).toBe(401);
  await expect(page.getByRole('dialog', { name: '用户详情', exact: true }).getByText('正常', { exact: true })).toBeVisible();
});

test('platform role grant and revoke preserve the independent team role', async ({ page, request }) => {
  const account = await register(request, 'platform-role');
  await signIn(page);
  await openUser(page, account);
  await confirmAction(page, '授予平台管理员', `/admin/users/${account.user.user_id}/platform-role`, 'K3 授权原因');
  const granted = await userDetail(request, account.user.user_id);
  expect(granted.platform_roles).toEqual(['platform_admin']);
  expect(granted.memberships).toContainEqual(expect.objectContaining({ tenant_id: account.user.tenant_id, role: 'tenant_admin' }));
  expect((await login(request, account)).user.roles).toContain('platform_admin');
  await expect(page.getByRole('dialog', { name: '用户详情', exact: true }).getByText('platform_admin', { exact: true })).toBeVisible();
  await confirmAction(page, '撤销平台管理员', `/admin/users/${account.user.user_id}/platform-role`, 'K3 撤权原因');
  expect((await userDetail(request, account.user.user_id)).platform_roles).toEqual([]);
  await expect(page.getByRole('dialog', { name: '用户详情', exact: true }).getByText('tenant_admin', { exact: true })).toBeVisible();
});

test('existing member role confirmation names the tenant and refreshes both user and tenant details', async ({ page, request }) => {
  const account = await register(request, 'member-role');
  requireRunner();
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-v', `tenant_id=${admin.user.tenant_id}`, '-v', `user_id=${account.user.user_id}`], {
    input: "INSERT INTO tenant_members (tenant_id,user_id,role,accepted_at) VALUES (:'tenant_id',:'user_id','tenant_admin',now());\n",
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  await signIn(page);
  await openUser(page, account);
  const detail = page.getByRole('dialog', { name: '用户详情', exact: true });
  const row = detail.getByRole('row').filter({ hasText: admin.user.tenant_id });
  await row.getByRole('button', { name: '修改团队角色', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '修改团队角色', exact: true });
  await expect(dialog.getByText(admin.user.tenant_id, { exact: true })).toBeVisible();
  await dialog.getByLabel('新团队角色', { exact: true }).click();
  await page.getByRole('option', { name: '只读成员（viewer）', exact: true }).click();
  await expect(dialog.getByText('tenant_admin → viewer', { exact: true })).toBeVisible();
  await dialog.getByLabel('操作原因', { exact: true }).fill('K3 成员角色原因');
  const changed = page.waitForResponse((response) => response.url().endsWith(`/admin/tenants/${admin.user.tenant_id}/members/${account.user.user_id}/role`)
    && response.request().method() === 'PUT');
  const me = page.waitForResponse((response) => response.url().endsWith('/auth/me'));
  await dialog.getByRole('button', { name: '确认操作', exact: true }).click();
  expect((await changed).status()).toBe(200);
  expect((await me).status()).toBe(200);
  await expect(row.getByText('viewer', { exact: true })).toBeVisible();
  const updated = await userDetail(request, account.user.user_id);
  expect(updated.memberships).toContainEqual(expect.objectContaining({ tenant_id: account.user.tenant_id, role: 'tenant_admin' }));
  expect(updated.memberships).toContainEqual(expect.objectContaining({ tenant_id: admin.user.tenant_id, role: 'viewer' }));
  await detail.getByRole('button', { name: '关闭', exact: true }).click();
  await openTenant(page, admin);
  const tenant = page.getByRole('dialog', { name: '租户详情', exact: true });
  await expect(tenant.getByRole('row').filter({ hasText: account.user.email }).getByText('viewer', { exact: true })).toBeVisible();
  expect((await tenantDetail(request, admin.user.tenant_id)).members).toContainEqual(expect.objectContaining({ user_id: account.user.user_id, role: 'viewer' }));
});

test('stale row versions show a conflict reason and reload the actual state', async ({ page, request }) => {
  const account = await register(request, 'stale-row');
  await signIn(page);
  await openUser(page, account);
  const before = await userDetail(request, account.user.user_id);
  expect((await mutate(request, `/admin/users/${account.user.user_id}/disable`, { reason: '另一管理员已处理', expected_version: before.row_version })).status()).toBe(200);
  await confirmAction(page, '授予平台管理员', `/admin/users/${account.user.user_id}/platform-role`, 'K3 过期提交', 409);
  await expect(page.getByRole('alert').filter({ hasText: /冲突|更新|版本/ })).toBeVisible();
  await page.getByRole('button', { name: '刷新最新数据', exact: true }).click();
  await expect(page.getByRole('dialog', { name: '用户详情', exact: true }).getByText('已禁用', { exact: true })).toBeVisible();
  const after = await userDetail(request, account.user.user_id);
  expect(after.platform_roles).toEqual([]);
  expect(after.audit_logs.some((entry) => entry.reason === 'K3 过期提交')).toBe(false);
});

test('ordinary roles get an explicit 403 despite a forged local administrator principal', async ({ page, request }) => {
  await signIn(page, ordinary);
  await page.evaluate(() => {
    const principal = JSON.parse(localStorage.getItem('principal') ?? '{}');
    localStorage.setItem('principal', JSON.stringify({ ...principal, roles: ['platform_admin'] }));
  });
  await page.goto('/admin');
  await expect(page.getByText('无访问权限', { exact: true })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '管理后台', exact: true })).toHaveCount(0);
  await expect(page.getByRole('tab', { name: '用户账号管理', exact: true })).toHaveCount(0);
  const response = await api(request, '/admin/users', ordinary);
  expect(response.status()).toBe(403);
  expect(await response.json()).toMatchObject({ code: 'FORBIDDEN', message: expect.any(String) });
});

test('successful credential refresh reloads me instead of retaining stale cached roles and plan', async ({ page }) => {
  await signIn(page);
  await page.evaluate(() => {
    const principal = JSON.parse(localStorage.getItem('principal') ?? '{}');
    localStorage.setItem('principal', JSON.stringify({ ...principal, roles: [], plan_code: 'enterprise' }));
    localStorage.setItem('access_token', 'expired-access-token-for-refresh-test');
  });
  const refreshed = page.waitForResponse((response) => response.url().endsWith('/auth/refresh'));
  const me = page.waitForResponse((response) => response.url().endsWith('/auth/me') && response.status() === 200);
  await page.goto('/admin');
  expect((await refreshed).status()).toBe(200);
  expect(await (await me).json()).toMatchObject({ user_id: admin.user.user_id, plan_code: 'free', roles: expect.arrayContaining(['platform_admin']) });
  await expect(page.getByRole('tab', { name: '用户账号管理', exact: true })).toBeVisible();
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('principal') ?? '{}'))).toMatchObject({ plan_code: 'free', roles: expect.arrayContaining(['platform_admin']) });
});

test('self platform role removal immediately clears credentials and account caches after 401', async ({ page, request }) => {
  const account = await register(request, 'self-revoke');
  const before = await userDetail(request, account.user.user_id);
  expect((await mutate(request, `/admin/users/${account.user.user_id}/platform-role`, {
    platform_admin: true, reason: 'K3 自撤前授权', expected_version: before.row_version,
  }, 'PUT')).status()).toBe(200);
  const session = await login(request, account);
  await signIn(page, account);
  await openUser(page, account);
  await page.getByRole('button', { name: '撤销平台管理员', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '撤销平台管理员', exact: true });
  await dialog.getByLabel('操作原因', { exact: true }).fill('K3 自撤原因');
  await dialog.getByRole('button', { name: '确认操作', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  expect(await page.evaluate(() => ['principal', 'access_token', 'refresh_token'].map((key) => localStorage.getItem(key)))).toEqual([null, null, null]);
  expect((await api(request, '/auth/me', session)).status()).toBe(401);
  expect((await request.post(`${apiURL}/auth/refresh`, { data: { refresh_token: session.refresh_token } })).status()).toBe(401);
  await signIn(page, ordinary);
  await expect(page.getByText('无访问权限', { exact: true })).toBeVisible();
  await expect(page.getByText(account.user.email, { exact: true })).toHaveCount(0);
});

test('a platform administrator can restore their own suspended tenant without changing account state', async ({ page, request }) => {
  await signIn(page);
  await openTenant(page, admin);
  await confirmAction(page, '挂起租户', `/admin/tenants/${admin.user.tenant_id}/suspend`, 'K3 挂起原因');
  await expect(page.getByRole('dialog', { name: '租户详情', exact: true }).getByText('已挂起', { exact: true })).toBeVisible();
  expect(await (await api(request, '/auth/me')).json()).toMatchObject({ tenant_status: 'suspended', roles: expect.arrayContaining(['platform_admin']) });
  await confirmAction(page, '恢复租户', `/admin/tenants/${admin.user.tenant_id}/resume`, 'K3 恢复原因');
  await expect(page.getByRole('dialog', { name: '租户详情', exact: true }).getByText('正常', { exact: true })).toBeVisible();
  const tenant = await tenantDetail(request, admin.user.tenant_id);
  expect(tenant.status).toBe('active');
  expect(tenant.orders).toEqual([]);
  expect(tenant.audit_logs).toContainEqual(expect.objectContaining({ action: 'tenant.resume', reason: 'K3 恢复原因' }));
  expect((await userDetail(request, admin.user.user_id)).status).toBe('active');
});

test('a real authentication database outage preserves credentials through a failed refresh and can recover', async ({ page }) => {
  await signIn(page);
  await page.evaluate(() => localStorage.setItem('access_token', 'expired-access-token-during-outage'));
  const credentials = await page.evaluate(() => ['principal', 'access_token', 'refresh_token'].map((key) => localStorage.getItem(key)));
  requireRunner();
  // Use the actual isolated database failure boundary. Renaming the table
  // breaks the auth query while keeping every fixture row for recovery.
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-c', 'ALTER TABLE users RENAME TO users_k3_outage'], { stdio: 'pipe' });
  try {
    const unavailable = page.waitForResponse((response) => response.url().endsWith('/auth/refresh'), { timeout: 10_000 });
    await page.goto('/admin');
    expect((await unavailable).status()).toBe(503);
    await expect(page).toHaveURL(/\/admin$/);
    expect(await page.evaluate(() => ['principal', 'access_token', 'refresh_token'].map((key) => localStorage.getItem(key)))).toEqual(credentials);
    await expect(page.getByRole('tab', { name: '用户账号管理', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: '重新加载登录状态', exact: true })).toBeVisible();
  } finally {
    execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-c', 'ALTER TABLE users_k3_outage RENAME TO users'], { stdio: 'pipe' });
  }
  const recovered = page.waitForResponse((response) => response.url().endsWith('/auth/me') && response.status() === 200);
  await page.getByRole('button', { name: '重新加载登录状态', exact: true }).click();
  expect((await recovered).status()).toBe(200);
  await expect(page.getByRole('tab', { name: '用户账号管理', exact: true })).toBeVisible();
});

test('a malformed cached principal is replaced by the current server identity', async ({ page }) => {
  await signIn(page);
  await page.evaluate(() => localStorage.setItem('principal', '{broken cached JSON'));
  const me = page.waitForResponse((response) => response.url().endsWith('/auth/me') && response.status() === 200);
  await page.goto('/admin');
  expect((await me).status()).toBe(200);
  await expect(page.getByRole('tab', { name: '用户账号管理', exact: true })).toBeVisible();
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('principal') ?? '{}'))).toMatchObject({ user_id: admin.user.user_id });
});

test('a rejected real notification settings save reports failure and leaves PostgreSQL unchanged', async ({ page }) => {
  await signIn(page);
  const settings = page.waitForResponse((response) => response.url().endsWith('/admin/settings')
    && response.request().method() === 'GET');
  await page.getByRole('tab', { name: '通知服务', exact: true }).click();
  expect((await settings).status()).toBe(200);
  await page.getByPlaceholder('noreply@your-domain.com').fill('k3-save-failure@example.invalid');
  await page.getByPlaceholder('发件人名称（盘古舆情）').fill('K3 failed notification save');
  requireRunner();
  const snapshot = () => execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-c',
    "SELECT COALESCE(jsonb_object_agg(key,jsonb_build_object('value',value,'updated_at',updated_at) ORDER BY key),'{}'::jsonb)::text FROM platform_settings",
  ], { encoding: 'utf8', stdio: 'pipe', timeout: 30_000 }).trim();
  const before = snapshot();
  // NOT VALID preserves existing rows but rejects every new INSERT/UPDATE,
  // including the actual notification form's first upsert. No supplier is used.
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-c',
    'ALTER TABLE platform_settings ADD CONSTRAINT k3_reject_notification_save CHECK (false) NOT VALID',
  ], { stdio: 'pipe', timeout: 30_000 });
  try {
    const rejected = page.waitForResponse((response) => response.url().endsWith('/admin/settings')
      && response.request().method() === 'PUT');
    await page.getByRole('button', { name: /保存邮件配置/ }).click();
    const response = await rejected;
    expect(response.status()).toBe(500);
    expect(response.request().postDataJSON()).toMatchObject({
      email_from_address: 'k3-save-failure@example.invalid', email_from_name: 'K3 failed notification save',
    });
    expect(snapshot()).toBe(before);
    await expect(page.getByRole('alert').filter({ hasText: /配置保存失败|保存失败|服务暂时不可用/ })).toBeVisible();
    await expect(page.getByText('配置已保存，即刻生效', { exact: true })).toHaveCount(0);
  } finally {
    execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-c',
      'ALTER TABLE platform_settings DROP CONSTRAINT IF EXISTS k3_reject_notification_save',
    ], { stdio: 'pipe', timeout: 30_000 });
  }
  expect(snapshot()).toBe(before);
});

test('a real notification settings load outage shows failure and recovers through retry', async ({ page }) => {
  await signIn(page);
  requireRunner();
  const snapshot = () => execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-c',
    "SELECT COALESCE(jsonb_object_agg(key,jsonb_build_object('value',value,'updated_at',updated_at) ORDER BY key),'{}'::jsonb)::text FROM platform_settings",
  ], { encoding: 'utf8', stdio: 'pipe', timeout: 30_000 }).trim();
  const before = snapshot();
  // Break only the real settings read boundary; preserve every fixture row.
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-c',
    'ALTER TABLE platform_settings RENAME TO platform_settings_k3_read_outage',
  ], { stdio: 'pipe', timeout: 30_000 });
  try {
    const failed = page.waitForResponse((response) => response.url().endsWith('/admin/settings')
      && response.request().method() === 'GET' && response.status() === 500);
    await page.getByRole('tab', { name: '通知服务', exact: true }).click();
    expect((await failed).status()).toBe(500);
    await expect(page.getByRole('alert').filter({ hasText: /配置加载失败|加载失败|服务暂时不可用/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /^重\s*试$/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /保存邮件配置/ })).toHaveCount(0);
    await expect(page.getByText('配置已保存，即刻生效', { exact: true })).toHaveCount(0);
  } finally {
    execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-c',
      'ALTER TABLE platform_settings_k3_read_outage RENAME TO platform_settings',
    ], { stdio: 'pipe', timeout: 30_000 });
  }
  expect(snapshot()).toBe(before);
  const recovered = page.waitForResponse((response) => response.url().endsWith('/admin/settings')
    && response.request().method() === 'GET' && response.status() === 200);
  await page.getByRole('button', { name: /^重\s*试$/ }).click();
  expect((await recovered).status()).toBe(200);
  await expect(page.getByRole('button', { name: /保存邮件配置/ })).toBeVisible();
  await expect(page.getByRole('alert').filter({ hasText: /配置加载失败|加载失败/ })).toHaveCount(0);
  expect(snapshot()).toBe(before);
});

test('notification configuration returns server masks and clearly distinguishes sandbox acceptance', async ({ page, request }) => {
  const fakeKey = 'k6a-isolated-resend-key';
  const updated = await request.put(`${apiURL}/admin/settings`, { headers: { Authorization: `Bearer ${admin.access_token}` }, data: {
    email_provider: 'resend', email_from_address: 'sender@example.invalid', resend_api_key: fakeKey,
  } });
  expect(updated.status()).toBe(200);
  const saved = await updated.json();
  expect(saved.settings.resend_api_key).toBe('********');
  expect(JSON.stringify(saved)).not.toContain(fakeKey);
  await signIn(page);
  const loaded = page.waitForResponse(r => r.url().endsWith('/admin/settings') && r.request().method() === 'GET');
  await page.getByRole('tab', { name: '通知服务', exact: true }).click();
  expect((await loaded).status()).toBe(200);
  await expect(page.getByText(/本轮验收仅使用隔离沙箱/)).toBeVisible();
  await expect(page.getByText(fakeKey, { exact: true })).toHaveCount(0);
  await page.getByPlaceholder('发件人名称（盘古舆情）').fill('K6a sandbox');
  const savedResponse = page.waitForResponse(r => r.url().endsWith('/admin/settings') && r.request().method() === 'PUT');
  await page.getByRole('button', { name: /保存邮件配置/ }).click();
  expect((await savedResponse).status()).toBe(200);
  const current = await api(request, '/admin/settings');
  expect((await current.json()).settings.resend_api_key).toBe('********');
  requireRunner();
  const retained = execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-c',
    "SELECT (value='k6a-isolated-resend-key')::text FROM platform_settings WHERE key='resend_api_key'",
  ], { encoding: 'utf8', stdio: 'pipe', timeout: 30_000 }).trim();
  expect(retained).toBe('true');
});
