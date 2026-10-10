import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createHash, randomBytes } from 'node:crypto';

const db = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const api = 'http://127.0.0.1:8080/api/v1';
const control = 'http://127.0.0.1:9081';
const password = 'K6bOriginalPassword2026';
const nextPassword = 'K6bRecoveredPassword2026';
const settings = { email_provider: 'resend', resend_api_key: 'sandbox-resend-ci', email_from_address: 'sender@example.invalid', email_from_name: 'CI Sandbox', sms_provider: 'aliyun', sms_access_key_id: 'sandbox-aliyun-ci', sms_access_key_secret: 'sandbox-aliyun-secret', sms_sign_name: 'CI Sandbox', sms_bind_phone_template_code: 'CI_BIND', sms_phone_login_template_code: 'CI_LOGIN', sms_phone_reset_template_code: 'CI_RESET' };
const keys = Object.keys(settings);
const token = JSON.parse(readFileSync(join(process.env.YUQING_NOTIFICATION_SANDBOX_DIR!, 'control.json'), 'utf8')).token as string;
const headers = { 'X-Sandbox-Token': token };
const hash = (value: string) => createHash('sha256').update(value).digest('hex');
type Acceptance = { id: string; purpose: string; vendor: string; target_hash: string; accepted: boolean; delivered: boolean; provider_id: string };
type Payload = { purpose: string; link?: string; token?: string; code?: string };
let previous = '{}';
function sql(statement: string, values: Record<string, string> = {}) {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== db) throw new Error('Hosted disposable PostgreSQL required');
  return execFileSync('psql', [db, '-v', 'ON_ERROR_STOP=1', '-A', '-t', ...Object.entries(values).flatMap(([key, value]) => ['-v', `${key}=${value}`])], { input: statement, encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'] }).trim();
}
async function account(request: APIRequestContext, administrator = false) {
  const email = `k6b-${randomBytes(8).toString('hex')}@example.invalid`;
  const response = await request.post(`${api}/auth/register`, { data: { email, password, name: 'K6b SDK recipient' } });
  expect(response.status()).toBe(201); const session = await response.json();
  if (administrator) sql("INSERT INTO platform_user_roles(user_id,role) VALUES(:'uid','platform_admin');", { uid: session.user.user_id });
  return { ...session, email };
}
async function mode(request: APIRequestContext, value: string) {
  expect((await request.post(`${control}/mode`, { headers, data: { mode: value } })).status()).toBe(200);
}
async function inbox(request: APIRequestContext): Promise<Acceptance[]> {
  const response = await request.get(`${control}/messages`, { headers }); expect(response.status()).toBe(200); return response.json();
}
async function accepted(request: APIRequestContext, purpose: string, target: string) {
  await expect.poll(async () => (await inbox(request)).filter(message => message.purpose === purpose && message.target_hash === hash(target) && message.accepted).length).toBeGreaterThan(0);
  const message = (await inbox(request)).filter(message => message.purpose === purpose && message.target_hash === hash(target) && message.accepted).at(-1)!;
  expect(message.delivered).toBe(false); expect(message).not.toHaveProperty('token'); expect(message).not.toHaveProperty('code'); expect(message).not.toHaveProperty('link');
  return message;
}
async function deliver(request: APIRequestContext, message: Acceptance): Promise<Payload> {
  const response = await request.post(`${control}/deliver`, { headers, data: { id: message.id } }); expect(response.status()).toBe(200);
  expect((await inbox(request)).find(item => item.id === message.id)?.delivered).toBe(true);
  return response.json();
}
function receipt(uid: string, purpose: string, message: Acceptance) {
  const table = purpose.startsWith('phone_') ? 'sms_verification_codes' : 'verification_tokens';
  const raw = sql(`SELECT delivery_receipt::text FROM ${table} WHERE user_id=:'uid' AND purpose=:'purpose' ORDER BY created_at DESC LIMIT 1;`, { uid, purpose });
  const value = JSON.parse(raw); expect(value.provider).toBe(message.vendor); expect(value.purpose).toBe(purpose); expect(value.state).toBe('accepted'); expect(value.provider_id).toBe(`sha256:${message.provider_id}`);
  expect(value).not.toHaveProperty('recipient'); expect(value).not.toHaveProperty('payload'); expect(value).not.toHaveProperty('token'); expect(value).not.toHaveProperty('code');
  console.log(`K6b ${purpose}: actual ${message.vendor} SDK accepted, payload-free receipt verified, explicit simulated delivery.`);
}
async function login(page: Page, email: string, currentPassword = password) {
  await page.goto('/login'); await page.getByLabel('邮箱', { exact: true }).fill(email); await page.getByLabel('密码', { exact: true }).fill(currentPassword);
  await page.getByRole('button', { name: /^登\s*录$/ }).click(); await expect(page).toHaveURL(/\/dashboard$/);
}
async function submitPassword(page: Page, payload: Payload, activation = false) {
  const urls: string[] = []; const observe = (request: { url(): string }) => urls.push(request.url()); page.on('request', observe);
  await page.goto(payload.link!); await expect(page).toHaveURL(activation ? /\/activate$/ : /\/reset-password$/);
  await page.getByLabel('新密码', { exact: true }).fill(nextPassword); await page.getByLabel('确认新密码', { exact: true }).fill(nextPassword);
  await page.getByRole('button', { name: activation ? '激活并设置密码' : '确认重置密码', exact: true }).click();
  await expect(page.getByText(activation ? '账号已激活，请登录' : '密码已重置，请重新登录', { exact: true })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem('access_token'))).toBeNull(); expect(urls.every(url => !url.includes(payload.token!))).toBe(true); page.off('request', observe);
}
test.beforeEach(async ({ request }) => {
  previous = sql("SELECT COALESCE(json_object_agg(key,value),'{}') FROM platform_settings WHERE key=ANY(string_to_array(:'keys',','));", { keys: keys.join(',') });
  const operator = await account(request, true);
  expect((await request.put(`${api}/admin/settings`, { headers: { Authorization: `Bearer ${operator.access_token}` }, data: settings })).status()).toBe(200);
  await mode(request, 'accept');
});
test.afterEach(async ({ request }) => {
  await mode(request, 'accept');
  sql("DELETE FROM platform_settings WHERE key=ANY(string_to_array(:'keys',',')); INSERT INTO platform_settings(key,value) SELECT key,value FROM json_each_text(:'previous'::json);", { keys: keys.join(','), previous });
});

test('K6b actual Resend verification and reset are explicitly delivered and consumed by the browser', async ({ page, request }) => {
  const a = await account(request); await login(page, a.email); await page.goto('/settings'); await page.getByRole('tab', { name: '个人资料', exact: true }).click();
  const sending = page.waitForResponse(response => response.url().endsWith('/auth/send-verification-email'));
  await page.getByRole('button', { name: '发送验证邮件', exact: true }).click(); expect((await sending).status()).toBe(200);
  const verification = await accepted(request, 'email_verify', a.email); receipt(a.user.user_id, 'email_verify', verification); const payload = await deliver(request, verification);
  expect((await request.post(`${api}/auth/password-reset/confirm`, { data: { token: payload.token, new_password: nextPassword } })).status()).toBe(400);
  await page.goto(payload.link!); await expect(page).toHaveURL(/\/verify-email$/); await expect(page.getByText('邮箱验证成功', { exact: true })).toBeVisible();
  expect(sql("SELECT (email_verified_at IS NOT NULL)::text FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe('true');
  expect((await request.post(`${api}/auth/verify-email`, { data: { token: payload.token } })).status()).toBe(400);
  await page.goto('/forgot-password'); await page.getByLabel('邮箱', { exact: true }).fill(a.email);
  const resetting = page.waitForResponse(response => response.url().endsWith('/auth/password-reset/request')); await page.getByRole('button', { name: '请求重置链接', exact: true }).click(); expect((await resetting).status()).toBe(202);
  await expect(page.getByRole('alert').filter({ hasText: '请求已受理' })).toContainText('送达尚未确认');
  const reset = await accepted(request, 'password_reset', a.email); receipt(a.user.user_id, 'password_reset', reset); await submitPassword(page, await deliver(request, reset));
  expect((await request.get(`${api}/auth/me`, { headers: { Authorization: `Bearer ${a.access_token}` } })).status()).toBe(401);
  expect((await request.post(`${api}/auth/refresh`, { data: { refresh_token: a.refresh_token } })).status()).toBe(401); await login(page, a.email, nextPassword);
});

test('K6b actual email change notice retries and Aliyun bind login reset consume purpose-specific delivery', async ({ page, request }) => {
  test.setTimeout(120000);
  const a = await account(request); const changed = `k6b-change-${randomBytes(7).toString('hex')}@example.invalid`; const phone = '13900006001';
  await login(page, a.email); await page.goto('/settings'); await page.getByRole('tab', { name: '账户安全', exact: true }).click();
  await page.getByLabel('当前密码', { exact: true }).fill(password); await page.getByLabel('新邮箱', { exact: true }).fill(changed);
  const sending = page.waitForResponse(response => response.url().endsWith('/user/email-change/request')); await page.getByRole('button', { name: '请求更换邮箱', exact: true }).click(); expect((await sending).status()).toBe(202);
  const change = await accepted(request, 'email_change', changed); receipt(a.user.user_id, 'email_change', change); const payload = await deliver(request, change);
  await mode(request, 'reject'); await page.goto(payload.link!); await expect(page).toHaveURL(/\/email-change$/); await page.getByRole('button', { name: '确认更换邮箱', exact: true }).click();
  await expect(page.getByText('邮箱已更换，请重新登录', { exact: true })).toBeVisible();
  await expect.poll(() => sql("SELECT notice_state FROM verification_tokens WHERE user_id=:'uid' AND purpose='email_change' ORDER BY created_at DESC LIMIT 1;", { uid: a.user.user_id }), { timeout: 30000 }).toBe('failed');
  expect(sql("SELECT email||':'||token_version FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe(`${changed}:1`);
  await mode(request, 'accept'); sql("UPDATE verification_tokens SET notice_next_attempt=now() WHERE user_id=:'uid' AND purpose='email_change' AND notice_state='failed';", { uid: a.user.user_id });
  await expect.poll(() => sql("SELECT notice_state||':'||notice_target FROM verification_tokens WHERE user_id=:'uid' AND purpose='email_change' ORDER BY created_at DESC LIMIT 1;", { uid: a.user.user_id }), { timeout: 30000 }).toBe('accepted:');
  const notice = await accepted(request, 'email_changed_notice', a.email); await deliver(request, notice);
  const acceptedAt = Date.now();
  await login(page, changed); await page.goto('/settings'); await page.getByRole('tab', { name: '手机绑定', exact: true }).click(); await page.getByRole('button', { name: '绑定手机号', exact: true }).click();
  let dialog = page.getByRole('dialog', { name: '绑定手机号', exact: true }); await dialog.getByLabel('手机号', { exact: true }).fill(phone); await dialog.getByLabel('当前密码', { exact: true }).fill(password);
  const binding = page.waitForResponse(response => response.url().endsWith('/user/phone/send-code')); await dialog.getByRole('button', { name: '发送验证码', exact: true }).click(); expect((await binding).status()).toBe(200);
  const bind = await accepted(request, 'phone_bind', phone); receipt(a.user.user_id, 'phone_bind', bind); const bindPayload = await deliver(request, bind);
  dialog = page.getByRole('dialog', { name: '绑定手机号（步骤 2/2）', exact: true }); await dialog.getByLabel('验证码', { exact: true }).fill(bindPayload.code!); await dialog.getByRole('button', { name: '确认绑定', exact: true }).click(); await expect(page).toHaveURL(/\/login$/);
  await page.getByRole('tab', { name: '手机号登录', exact: true }).click(); await page.getByLabel('手机号', { exact: true }).fill(phone);
  const logging = page.waitForResponse(response => response.url().endsWith('/auth/phone/send-code')); await page.getByRole('button', { name: '请求短信验证码', exact: true }).click(); expect((await logging).status()).toBe(202);
  const loginMessage = await accepted(request, 'phone_login', phone); receipt(a.user.user_id, 'phone_login', loginMessage); await page.getByLabel('验证码', { exact: true }).fill((await deliver(request, loginMessage)).code!);
  await page.getByRole('button', { name: /^手\s*机\s*登\s*录$/ }).click(); await expect(page).toHaveURL(/\/dashboard$/); expect(await page.evaluate(() => JSON.parse(localStorage.getItem('principal')!).user_id)).toBe(a.user.user_id);
  await page.goto('/forgot-password'); await page.getByRole('tab', { name: '手机找回', exact: true }).click(); await page.getByLabel('手机号', { exact: true }).fill(phone);
  const resetting = page.waitForResponse(response => response.url().endsWith('/auth/password-reset/request')); await page.getByRole('button', { name: '请求短信验证码', exact: true }).click(); expect((await resetting).status()).toBe(202);
  const reset = await accepted(request, 'phone_reset', phone); receipt(a.user.user_id, 'phone_reset', reset); await page.getByLabel('验证码', { exact: true }).fill((await deliver(request, reset)).code!); await page.getByLabel('新密码', { exact: true }).fill(nextPassword); await page.getByLabel('确认新密码', { exact: true }).fill(nextPassword);
  await page.getByRole('button', { name: '确认重置密码', exact: true }).click(); await expect(page.getByText('密码已重置，请重新登录', { exact: true })).toBeVisible(); await login(page, changed, nextPassword);
  await expect.poll(() => Date.now() - acceptedAt, { timeout: 30000 }).toBeGreaterThan(21000);
  expect((await inbox(request)).filter(message => message.purpose === 'email_changed_notice' && message.target_hash === hash(a.email) && message.accepted)).toHaveLength(1);
});

test('K6b activation works after SDK receipt succeeds but dispatch-attempt finalization fails', async ({ page, request }) => {
  const operator = await account(request, true); const email = `k6b-activate-${randomBytes(7).toString('hex')}@example.invalid`;
  sql("CREATE FUNCTION k6b_attempt_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='accepted' THEN RAISE EXCEPTION 'isolated attempt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER k6b_attempt_failure BEFORE UPDATE ON account_notification_attempts FOR EACH ROW EXECUTE FUNCTION k6b_attempt_failure();");
  let created: { user_id: string };
  try {
    await login(page, operator.email); await page.goto('/admin'); await page.getByRole('tab', { name: '用户账号管理', exact: true }).click(); await page.getByRole('button', { name: '新建账号', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '新建账号', exact: true }); await dialog.getByLabel('邮箱', { exact: true }).fill(email); await dialog.getByLabel('昵称', { exact: true }).fill('SDK Activation');
    const creating = page.waitForResponse(response => response.url().endsWith('/admin/users') && response.request().method() === 'POST'); await dialog.getByRole('button', { name: '创建待激活账号', exact: true }).click(); const response = await creating; expect(response.status()).toBe(201); created = await response.json();
    await expect(dialog.getByRole('alert')).toContainText('发送结果待确认'); expect(sql("SELECT state FROM account_notification_attempts WHERE user_id=:'uid';", { uid: created.user_id })).toBe('pending');
  } finally { sql('DROP TRIGGER k6b_attempt_failure ON account_notification_attempts; DROP FUNCTION k6b_attempt_failure();'); }
  const activation = await accepted(request, 'set_password', email); receipt(created!.user_id, 'set_password', activation); const payload = await deliver(request, activation); await submitPassword(page, payload, true);
  expect((await request.post(`${api}/auth/activation/confirm`, { data: { token: payload.token, new_password: nextPassword } })).status()).toBe(400);
  expect(sql("SELECT status||':'||token_version FROM users WHERE id=:'uid';", { uid: created!.user_id })).toBe('active:1'); await login(page, email, nextPassword);
});

test('K6b rejected malformed timeout receipts and persistence failure never become consumable delivery', async ({ page, request }) => {
  test.setTimeout(120000);
  for (const failure of ['reject', 'malformed', 'timeout']) {
    const a = await account(request); await mode(request, failure);
    expect((await request.post(`${api}/auth/password-reset/request`, { data: { email: a.email } })).status()).toBe(202);
    const messages = (await inbox(request)).filter(message => message.target_hash === hash(a.email) && message.purpose === 'password_reset'); expect(messages).toHaveLength(1); expect(messages[0].accepted).toBe(false);
    expect((await request.post(`${control}/deliver`, { headers, data: { id: messages[0].id } })).status()).toBe(409);
    expect(sql("SELECT delivery_status||':'||(used_at IS NOT NULL)::text FROM verification_tokens WHERE user_id=:'uid' AND purpose='password_reset';", { uid: a.user.user_id })).toBe('rejected:true');
    expect(sql("SELECT token_version FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe('0');
  }
  await mode(request, 'accept'); const a = await account(request);
  sql("CREATE FUNCTION k6b_receipt_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.delivery_status='accepted' AND NEW.user_id=" + "'" + a.user.user_id + "'" + " THEN RAISE EXCEPTION 'isolated receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER k6b_receipt_failure BEFORE UPDATE ON verification_tokens FOR EACH ROW EXECUTE FUNCTION k6b_receipt_failure();");
  try { expect((await request.post(`${api}/auth/send-verification-email`, { headers: { Authorization: `Bearer ${a.access_token}` } })).status()).toBeGreaterThanOrEqual(500); }
  finally { sql('DROP TRIGGER k6b_receipt_failure ON verification_tokens; DROP FUNCTION k6b_receipt_failure();'); }
  const verification = await accepted(request, 'email_verify', a.email); const payload = await deliver(request, verification);
  expect(sql("SELECT delivery_status FROM verification_tokens WHERE user_id=:'uid' AND purpose='email_verify';", { uid: a.user.user_id })).toBe('pending');
  await page.goto(payload.link!); await expect(page.getByText('验证失败', { exact: true })).toBeVisible(); expect(sql("SELECT (email_verified_at IS NULL)::text FROM users WHERE id=:'uid';", { uid: a.user.user_id })).toBe('true');
  const unknown = `k6b-unknown-${randomBytes(6).toString('hex')}@example.invalid`;
  expect((await request.post(`${api}/auth/password-reset/request`, { data: { email: unknown } })).status()).toBe(202); expect((await inbox(request)).filter(message => message.target_hash === hash(unknown))).toHaveLength(0);
  expect(sql("SELECT count(*) FROM users WHERE email=:'email';", { email: unknown })).toBe('0');
  const expired = await account(request); expect((await request.post(`${api}/auth/send-verification-email`, { headers: { Authorization: `Bearer ${expired.access_token}` } })).status()).toBe(200);
  const expiredPayload = await deliver(request, await accepted(request, 'email_verify', expired.email)); sql("UPDATE verification_tokens SET expires_at=now()-interval '1 second' WHERE user_id=:'uid' AND purpose='email_verify';", { uid: expired.user.user_id });
  expect((await request.post(`${api}/auth/verify-email`, { data: { token: expiredPayload.token } })).status()).toBe(400);
});
