import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createHash, createHmac, randomBytes } from 'node:crypto';

const databaseURL = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const api = 'http://127.0.0.1:8080/api/v1';
const password = 'K7-original-password-2026';
const nextPassword = 'K7-recovered-password-2026';
function sql(statement: string, values: Record<string, string> = {}) {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== databaseURL) throw new Error('Hosted disposable PostgreSQL required');
  return execFileSync('psql', [databaseURL, '-v', 'ON_ERROR_STOP=1', '-A', '-t', ...Object.entries(values).flatMap(([k, v]) => ['-v', `${k}=${v}`])], { input: statement, encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'] }).trim();
}
async function account(request: APIRequestContext) {
  const email = `k7-${randomBytes(8).toString('hex')}@example.invalid`;
  const r = await request.post(`${api}/auth/register`, { data: { email, password, name: 'K7 Identity' } });
  expect(r.status()).toBe(201);
  const session = await r.json();
  return { ...session, email };
}
function credential(uid: string, purpose: string, target: string, opts: { expired?: boolean; used?: boolean } = {}) {
  const sms = purpose.startsWith('phone_');
  const value = sms ? '730142' : randomBytes(32).toString('base64url');
  const hash = sms ? createHmac('sha256', 'account-admin-ci-only-no-production-identity').update(`yuqing-verification-v1\0${purpose}\0${target}\0${value}`).digest('hex') : createHash('sha256').update(value).digest('hex');
  const table = sms ? 'sms_verification_codes' : 'verification_tokens';
  const columns = sms ? 'phone,code_hash' : 'target,token_hash,type';
  const fields = sms ? ":'target',:'hash'" : ":'target',:'hash',:'purpose'";
  sql(`INSERT INTO ${table}(id,user_id,issuer_user_id,purpose,${columns},issued_version,issuer_version,expires_at,delivery_status,used_at) SELECT :'id',id,id,:'purpose',${fields},token_version,token_version,now()+interval '${opts.expired ? '-1 minute' : '10 minutes'}','accepted',${opts.used ? 'now()' : 'NULL'} FROM users WHERE id=:'uid';`, { id: randomBytes(12).toString('hex'), uid, purpose, target, hash });
  return value;
}
async function login(page: Page, email: string, pass = password) {
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(pass);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
}
async function cleared(page: Page) {
  expect(await page.evaluate(() => ['access_token', 'refresh_token', 'principal'].map(k => localStorage.getItem(k)))).toEqual([null, null, null]);
}

test('email reset scrubs the fragment, preserves password on 503 and revokes both original tokens', async ({ page, request }) => {
  const a = await account(request);
  const token = credential(a.user.user_id, 'password_reset', a.email);
  await login(page, a.email);
  const urls: string[] = [];
  page.on('request', r => urls.push(r.url()));
  await page.goto(`/reset-password#token=${token}`);
  await expect(page).toHaveURL(/\/reset-password$/);
  await page.getByLabel('新密码', { exact: true }).fill(nextPassword);
  await page.getByLabel('确认新密码', { exact: true }).fill(nextPassword);
  await page.route('**/auth/password-reset/confirm', async route => { await route.fulfill({ status: 503, contentType: 'application/json', body: '{"code":"SERVICE_UNAVAILABLE"}' }); });
  await page.getByRole('button', { name: '确认重置密码' }).click();
  await expect(page.getByRole('alert').filter({ hasText: '暂时不可用' }).first()).toBeVisible();
  await expect(page.getByLabel('新密码', { exact: true })).toHaveValue(nextPassword);
  expect(await page.evaluate(() => localStorage.getItem('access_token'))).toBeTruthy();
  await page.unroute('**/auth/password-reset/confirm');
  await page.getByRole('button', { name: '确认重置密码' }).click();
  await expect(page.getByText('密码已重置，请重新登录', { exact: true })).toBeVisible();
  await cleared(page);
  expect(urls.every(u => !u.includes(token))).toBe(true);
  expect((await request.get(`${api}/auth/me`, { headers: { Authorization: `Bearer ${a.access_token}` } })).status()).toBe(401);
  expect((await request.post(`${api}/auth/refresh`, { data: { refresh_token: a.refresh_token } })).status()).toBe(401);
  await login(page, a.email, nextPassword);
});

test('verified phone login and phone reset use the real account and clear identity after recovery', async ({ page, request }) => {
  const a = await account(request);
  const phone = '13900000722';
  sql("UPDATE users SET phone=:'phone',phone_verified_at=now() WHERE id=:'uid';", { phone, uid: a.user.user_id });
  const code = credential(a.user.user_id, 'phone_login', phone);
  await page.goto('/login');
  await page.getByRole('tab', { name: '手机号登录' }).click();
  await page.getByLabel('手机号', { exact: true }).fill(phone);
  await page.getByLabel('验证码', { exact: true }).fill(code);
  await page.getByRole('button', { name: '手机登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('principal')!).user_id)).toBe(a.user.user_id);
  const resetCode = credential(a.user.user_id, 'phone_reset', phone);
  await page.goto('/forgot-password');
  await page.getByRole('tab', { name: '手机找回' }).click();
  await page.getByLabel('手机号', { exact: true }).fill(phone);
  await page.getByLabel('验证码', { exact: true }).fill(resetCode);
  await page.getByLabel('新密码', { exact: true }).fill(nextPassword);
  await page.getByLabel('确认新密码', { exact: true }).fill(nextPassword);
  await page.getByRole('button', { name: '确认重置密码' }).click();
  await expect(page.getByText('密码已重置，请重新登录', { exact: true })).toBeVisible();
  await cleared(page);
  await login(page, a.email, nextPassword);
});

test('email change link permits inline re-login, commits despite unavailable notice channel and clears session', async ({ page, request }) => {
  const a = await account(request);
  const nextEmail = `changed-${randomBytes(8).toString('hex')}@example.invalid`;
  const token = credential(a.user.user_id, 'email_change', nextEmail);
  await page.goto(`/email-change#token=${token}`);
  await expect(page).toHaveURL(/\/email-change$/);
  await page.getByLabel('邮箱', { exact: true }).fill(a.email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录后继续确认' }).click();
  await page.getByRole('button', { name: '确认更换邮箱', exact: true }).click();
  await expect(page.getByText('邮箱已更换，请重新登录', { exact: true })).toBeVisible();
  await cleared(page);
  expect(sql("SELECT email || ':' || token_version FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe(`${nextEmail}:1`);
  expect(sql("SELECT count(*) FROM verification_tokens WHERE user_id=:'uid' AND purpose='email_change' AND notice_target=:'old' AND notice_state IN ('pending','processing','failed');", { uid: a.user.user_id, old: a.email })).toBe('1');
  await login(page, nextEmail);
});

test('email change target conflict preserves old identity and the reusable confirmation', async ({ page, request }) => {
  const a = await account(request); const b = await account(request);
  const token = credential(a.user.user_id, 'email_change', b.email);
  await login(page, a.email);
  await page.goto(`/email-change#token=${token}`);
  await page.getByRole('button', { name: '确认更换邮箱', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: '邮箱已被占用' }).first()).toBeVisible();
  expect(sql("SELECT email || ':' || token_version FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe(`${a.email}:0`);
  expect(sql("SELECT (used_at IS NULL)::text FROM verification_tokens WHERE user_id=:'uid' AND purpose='email_change';", { uid: a.user.user_id })).toBe('true');
});

for (const state of ['expired', 'used'] as const) test(`password reset shows an explicit ${state} link state`, async ({ page, request }) => {
  const a = await account(request);
  const token = credential(a.user.user_id, 'password_reset', a.email, { [state]: true });
  await page.goto(`/reset-password#token=${token}`);
  await page.getByLabel('新密码', { exact: true }).fill(nextPassword);
  await page.getByLabel('确认新密码', { exact: true }).fill(nextPassword);
  await page.getByRole('button', { name: '确认重置密码' }).click();
  await expect(page.getByRole('alert').filter({ hasText: state === 'expired' ? '链接已过期' : '链接已使用' }).first()).toBeVisible();
});

test('forgot password preserves input on unavailable channel and shows admission without delivery claim', async ({ page }) => {
  // Earlier configuration tests deliberately configure the isolated Resend key.
  // Temporarily remove only the provider selection to exercise global503.
  const provider = sql("SELECT value FROM platform_settings WHERE key='email_provider';");
  sql("UPDATE platform_settings SET value='' WHERE key='email_provider';");
  try {
    await page.goto('/login');
    await page.getByRole('link', { name: '忘记密码' }).click();
    await expect(page).toHaveURL(/\/forgot-password$/);
    await expect(page.getByRole('heading', { name: '找回密码', exact: true })).toBeVisible();
    await page.getByLabel('邮箱', { exact: true }).fill('unknown-k7@example.invalid');
    const unavailable = page.waitForResponse(r => r.url().endsWith('/auth/password-reset/request'));
    await page.getByRole('button', { name: '请求重置链接' }).click();
    expect((await unavailable).status()).toBe(503);
    await expect(page.getByRole('alert').filter({ hasText: '暂时不可用' }).first()).toBeVisible();
    await expect(page.getByLabel('邮箱', { exact: true })).toHaveValue('unknown-k7@example.invalid');
  } finally { sql("UPDATE platform_settings SET value=:'provider' WHERE key='email_provider';", { provider }); }
  // The target is unknown, so the configured channel is checked but no outbound
  // provider request or new account is made; this is admission, not delivery.
  await page.getByRole('button', { name: '请求重置链接' }).click();
  await expect(page.getByRole('alert').filter({ hasText: '请求已受理' }).first()).toContainText('送达尚未确认');
});

test('isolated rebind browser consumption preserves intent on real 503 then revokes old phone and cached identity', async ({ page, request }) => {
  const a = await account(request);
  const oldPhone = '13900000761'; const newPhone = '13900000762';
  sql("UPDATE users SET phone=:'phone',phone_verified_at=now(),email_verified_at=now() WHERE id=:'uid';", { uid: a.user.user_id, phone: oldPhone });
  await login(page, a.email);
  await page.goto('/settings');
  await page.getByRole('tab', { name: '手机绑定', exact: true }).click();
  await expect(page.getByText('139****0761', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '更换手机号', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('手机号', { exact: true }).fill(newPhone);
  await dialog.getByLabel('当前密码', { exact: true }).fill(password);
  const failed = page.waitForResponse(r => r.url().endsWith('/user/phone/send-code'));
  await dialog.getByRole('button', { name: '发送验证码', exact: true }).click();
  expect((await failed).status()).toBe(503);
  await expect(dialog.getByRole('alert')).toContainText('暂时不可用');
  await expect(dialog.getByLabel('手机号', { exact: true })).toHaveValue(newPhone);
  await expect(dialog.getByLabel('当前密码', { exact: true })).toHaveValue(password);
  expect(await page.evaluate(() => localStorage.getItem('access_token'))).toBeTruthy();
  const code = credential(a.user.user_id, 'phone_bind', newPhone);
  // Explicit sandbox acknowledgement only: actual Go Inbox issuance is tested
  // in PG HTTP contracts; this fixture isolates real browser/HTTP consumption.
  await page.route('**/user/phone/send-code', route => route.fulfill({ status: 200, contentType: 'application/json', body: '{"message":"sandbox request accepted; delivery unconfirmed","expires_in":300}' }));
  await dialog.getByRole('button', { name: '发送验证码', exact: true }).click();
  await dialog.getByLabel('验证码', { exact: true }).fill(code);
  const bound = page.waitForResponse(r => r.url().endsWith('/user/phone/bind'));
  await dialog.getByRole('button', { name: '确认绑定', exact: true }).click();
  expect((await bound).status()).toBe(200);
  await expect(page).toHaveURL(/\/login$/);
  await cleared(page);
  expect(sql("SELECT phone || ':' || token_version FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe(`${newPhone}:1`);
  await login(page, a.email);
  await page.goto('/settings');
  await page.getByRole('tab', { name: '手机绑定', exact: true }).click();
  await expect(page.getByText('139****0762', { exact: true })).toBeVisible();
  await expect(page.getByText('139****0761', { exact: true })).toHaveCount(0);
});

test('email change settings request reports unavailable channel and preserves form and session', async ({ page, request }) => {
  const a = await account(request);
  await login(page, a.email);
  await page.goto('/settings');
  await page.getByRole('tab', { name: '账户安全', exact: true }).click();
  await page.getByLabel('当前密码', { exact: true }).fill(password);
  await page.getByLabel('新邮箱', { exact: true }).fill('new-intent@example.invalid');
  const attempted = page.waitForResponse(r => r.url().endsWith('/user/email-change/request'));
  await page.getByRole('button', { name: '请求更换邮箱', exact: true }).click();
  expect((await attempted).status()).toBe(503);
  await expect(page.getByRole('alert').filter({ hasText: '暂时不可用' }).first()).toBeVisible();
  await expect(page.getByLabel('当前密码', { exact: true })).toHaveValue(password);
  await expect(page.getByLabel('新邮箱', { exact: true })).toHaveValue('new-intent@example.invalid');
  expect(await page.evaluate(() => localStorage.getItem('access_token'))).toBeTruthy();
  expect(sql("SELECT email FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe(a.email);
});

test('wrong current password on phone binding is one attempt and preserves the valid session and form', async ({ page, request }) => {
  const a = await account(request);
  await login(page, a.email);
  await page.goto('/settings');
  await page.getByRole('tab', { name: '手机绑定', exact: true }).click();
  await page.getByRole('button', { name: '绑定手机号', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('手机号', { exact: true }).fill('13900000791');
  await dialog.getByLabel('当前密码', { exact: true }).fill('incorrect-password-123');
  const calls: string[] = [];
  page.on('request', r => { if (r.url().endsWith('/user/phone/send-code') || r.url().endsWith('/auth/refresh')) calls.push(r.url()); });
  await dialog.getByRole('button', { name: '发送验证码', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('密码、验证码不正确');
  await expect(page).toHaveURL(/\/settings$/);
  await expect(dialog.getByLabel('当前密码', { exact: true })).toHaveValue('incorrect-password-123');
  expect(calls.filter(url => url.endsWith('/user/phone/send-code'))).toHaveLength(1);
  expect(calls.filter(url => url.endsWith('/auth/refresh'))).toHaveLength(0);
  expect(await page.evaluate(() => localStorage.getItem('access_token'))).toBeTruthy();
});

test('one wrong bind code spends one attempt while genuine revocation still logs out and clears identity', async ({ page, request }) => {
  const a = await account(request);
  const phone = '13900000793';
  const code = credential(a.user.user_id, 'phone_bind', phone);
  await login(page, a.email);
  await page.goto('/settings');
  await page.getByRole('tab', { name: '手机绑定', exact: true }).click();
  await page.getByRole('button', { name: '绑定手机号', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('手机号', { exact: true }).fill(phone);
  await dialog.getByLabel('当前密码', { exact: true }).fill(password);
  // Only the delivered fixture's acknowledgement is synthetic; consumption,
  // attempts, revocation, refresh and subsequent session clearing are real.
  await page.route('**/user/phone/send-code', route => route.fulfill({ status: 200, contentType: 'application/json', body: '{"message":"sandbox request accepted; delivery unconfirmed","expires_in":300}' }));
  await dialog.getByRole('button', { name: '发送验证码', exact: true }).click();
  await dialog.getByLabel('验证码', { exact: true }).fill('000000');
  const calls: string[] = [];
  page.on('request', r => { if (r.url().endsWith('/user/phone/bind') || r.url().endsWith('/auth/refresh')) calls.push(r.url()); });
  await dialog.getByRole('button', { name: '确认绑定', exact: true }).click();
  await expect(dialog.getByRole('alert').filter({ hasText: '密码、验证码不正确' })).toBeVisible();
  await expect(dialog.getByLabel('验证码', { exact: true })).toHaveValue('000000');
  expect(calls.filter(url => url.endsWith('/user/phone/bind'))).toHaveLength(1);
  expect(calls.filter(url => url.endsWith('/auth/refresh'))).toHaveLength(0);
  expect(sql("SELECT attempts FROM sms_verification_codes WHERE user_id=:'uid' AND purpose='phone_bind';", { uid: a.user.user_id })).toBe('1');
  sql("UPDATE users SET token_version=token_version+1 WHERE id=:'uid';", { uid: a.user.user_id });
  await dialog.getByLabel('验证码', { exact: true }).fill(code);
  await dialog.getByRole('button', { name: '确认绑定', exact: true }).click();
  await expect(page).toHaveURL(/\/login$/);
  await cleared(page);
  expect(calls.filter(url => url.endsWith('/auth/refresh'))).toHaveLength(1);
  expect(sql("SELECT (phone IS NULL)::text FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe('true');
});
