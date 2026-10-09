import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';

const databaseURL = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const apiURL = 'http://127.0.0.1:8080/api/v1';
const password = 'K5-cloud-verification-2026';

test('phone unbinding remains available for suspended tenant with invalid plan and clears browser session', async ({ page, request }) => {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== databaseURL) throw new Error('Disposable hosted database required');
  const email = `k5-unbind-${Date.now()}@example.invalid`;
  const registered = await request.post(`${apiURL}/auth/register`, { data: { email, name: 'K5 Verification', password } });
  expect(registered.status()).toBe(201);
  const session = await registered.json();
  expect(session.user.user_id).toMatch(/^[0-9A-HJKMNP-TV-Z]{26}$/);
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-v', `uid=${session.user.user_id}`], {
    input: `UPDATE users SET email_verified_at=now(),phone='13900000516',phone_verified_at=now() WHERE id=:'uid';`, stdio: ['pipe', 'pipe', 'pipe'],
  });
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-v', `tid=${session.user.tenant_id}`], {
    input: `UPDATE tenants SET status='suspended' WHERE id=:'tid'; UPDATE report_credits SET plan_code='invalid-verification-fixture' WHERE tenant_id=:'tid';`, stdio: ['pipe', 'pipe', 'pipe'],
  });
  await page.goto('/settings');
  await page.getByRole('tab', { name: '手机绑定', exact: true }).click();
  await page.getByRole('button', { name: '解绑手机号', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '确认解绑手机号', exact: true });
  await dialog.getByLabel('为了账户安全，请输入登录密码确认').fill(password);
  const completed = page.waitForResponse(r => r.url().endsWith('/user/phone/unbind') && r.request().method() === 'POST');
  await dialog.getByRole('button', { name: '确认解绑', exact: true }).click();
  expect((await completed).status()).toBe(200);
  await expect(page).toHaveURL(/\/login$/);
  expect(await page.evaluate(() => [localStorage.getItem('access_token'), localStorage.getItem('refresh_token')])).toEqual([null, null]);
  const stale = await request.get(`${apiURL}/user/profile`, { headers: { Authorization: `Bearer ${session.access_token}` } });
  expect(stale.status()).toBe(401);
  const state = execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-A', '-t', '-v', `uid=${session.user.user_id}`], {
    input: `SELECT (phone IS NULL AND phone_verified_at IS NULL)::text || ':' || token_version || ':' || row_version FROM users WHERE id=:'uid';`, encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'],
  }).trim();
  expect(state).toBe('true:1:1');
});

test('email landing removes bearer query and consumes the credential once', async ({ page, request }) => {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== databaseURL) throw new Error('Disposable hosted database required');
  const { createHash, randomBytes } = await import('node:crypto');
  const email = `k5-email-${Date.now()}@example.invalid`;
  const registered = await request.post(`${apiURL}/auth/register`, { data: { email, name: 'K5 Email', password } });
  expect(registered.status()).toBe(201);
  const session = await registered.json();
  expect(session.user.user_id).toMatch(/^[0-9A-HJKMNP-TV-Z]{26}$/);
  const bearer = randomBytes(32).toString('base64url');
  const hash = createHash('sha256').update(bearer).digest('hex');
  execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-v', `uid=${session.user.user_id}`, '-v', `email=${email}`, '-v', `hash=${hash}`], {
    input: `INSERT INTO verification_tokens(id,user_id,issuer_user_id,type,purpose,target,token_hash,issued_version,issuer_version,expires_at,delivery_status) VALUES('browser-' || :'uid',:'uid',:'uid','email_verify','email_verify',:'email',:'hash',0,0,now()+interval '24 hours','accepted');`, stdio: ['pipe', 'pipe', 'pipe'],
  });
  const requestURLs: string[] = [];
  page.on('request', req => requestURLs.push(req.url()));
  await page.goto(`/verify-email#token=${bearer}`);
  await expect(page.getByText('邮箱验证成功', { exact: true })).toBeVisible();
  await expect(page).toHaveURL(/\/verify-email$/);
  expect(await page.locator('meta[name="referrer"]').getAttribute('content')).toBe('no-referrer');
  expect(requestURLs.every(url => !url.includes(bearer))).toBe(true);
  const replay = await request.post(`${apiURL}/auth/verify-email`, { data: { token: bearer } });
  expect(replay.status()).toBe(404);
});
