import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';

const database = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const api = 'http://127.0.0.1:8080/api/v1';
const password = 'K11-hosted-sandbox-only-2026';
function sql(statement: string, values: Record<string, string> = {}) {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== database) throw new Error('Hosted disposable database required');
  return execFileSync('psql', [database, '-v', 'ON_ERROR_STOP=1', '-A', '-t', ...Object.entries(values).flatMap(([k, v]) => ['-v', `${k}=${v}`])], { input: statement, encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'] }).trim();
}
async function account(request: APIRequestContext, admin = false) {
  const email = `k11-${randomBytes(7).toString('hex')}@example.invalid`;
  const r = await request.post(`${api}/auth/register`, { data: { email, password, name: 'K11 Browser' } });
  expect(r.status()).toBe(201);
  const a = await r.json();
  if (admin) sql("INSERT INTO platform_user_roles(user_id,role) VALUES(:'uid','platform_admin');", { uid: a.user.user_id });
  return { ...a, email };
}
async function login(page: Page, email: string) {
  await page.goto('/login'); await page.getByLabel('邮箱', { exact: true }).fill(email); await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click(); await expect(page).toHaveURL(/\/dashboard$/);
}

test('K11 manual plan confirmation preserves reason through rollback and reloads actual source without money changes', async ({ page, request }) => {
  const a = await account(request, true); const tid = a.user.tenant_id;
  const ledger = sql("SELECT count(*) FROM credit_transactions WHERE tenant_id=:'tid';", { tid });
  await login(page, a.email); await page.goto('/admin'); await page.getByRole('tab', { name: '租户与套餐管理', exact: true }).click();
  await page.getByPlaceholder('搜索租户名称或 ID').fill(tid); await page.getByRole('button', { name: '搜索', exact: true }).click();
  await page.getByRole('row').filter({ hasText: tid }).getByRole('button', { name: '查看详情', exact: true }).click();
  await page.getByRole('button', { name: '修改套餐权益', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '修改套餐权益', exact: true });
  await dialog.getByRole('combobox', { name: '目标套餐', exact: true }).press('ArrowDown'); await page.getByRole('option', { name: /（pro）$/ }).click();
  await dialog.getByLabel('操作原因', { exact: true }).fill('K11 approved existing plan');
  sql("CREATE FUNCTION k11_plan_ui_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='tenant.plan_change' THEN RAISE EXCEPTION 'isolated plan audit rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER k11_plan_ui_reject BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION k11_plan_ui_reject();");
  try {
    const result = page.waitForResponse(r => r.url().endsWith('/plan-change') && r.request().method() === 'POST');
    await dialog.getByRole('button', { name: '确认权益变更', exact: true }).click(); expect((await result).status()).toBe(500);
    await expect(dialog.getByRole('alert').last()).toBeVisible(); await expect(dialog.getByLabel('操作原因', { exact: true })).toHaveValue('K11 approved existing plan');
    expect(sql("SELECT plan_code||':'||balance FROM report_credits WHERE tenant_id=:'tid';", { tid })).toBe('free:1');
  } finally { sql('DROP TRIGGER k11_plan_ui_reject ON audit_logs; DROP FUNCTION k11_plan_ui_reject();'); }
  const confirm = dialog.getByRole('button', { name: '确认权益变更', exact: true }); await expect(confirm).toBeEnabled(); await expect(confirm).toHaveAttribute('aria-busy', 'false');
  const result = page.waitForResponse(r => r.url().endsWith('/plan-change')); await confirm.click(); expect((await result).status()).toBe(200);
  await expect(dialog).toHaveCount(0); const detail = page.getByRole('dialog', { name: '租户详情', exact: true }); await expect(detail).toContainText('人工调整');
  expect(sql("SELECT plan_code||':'||plan_source||':'||balance FROM report_credits WHERE tenant_id=:'tid';", { tid })).toBe('pro:manual:1');
  expect(sql("SELECT count(*) FROM credit_transactions WHERE tenant_id=:'tid';", { tid })).toBe(ledger);
  expect(sql("SELECT count(*) FROM orders WHERE tenant_id=:'tid';", { tid })).toBe('0');
  await page.reload(); await page.getByRole('tab', { name: '租户与套餐管理', exact: true }).click(); await page.getByPlaceholder('搜索租户名称或 ID').fill(tid); await page.getByRole('button', { name: '搜索', exact: true }).click();
  await page.getByRole('row').filter({ hasText: tid }).getByRole('button', { name: '查看详情', exact: true }).click(); await expect(page.getByRole('dialog', { name: '租户详情', exact: true })).toContainText('人工调整');
});

test('K11 preferences expose connected task mail and preserve a rejected save before reload', async ({ page, request }) => {
  const a = await account(request); const uid = a.user.user_id;
  await login(page, a.email); await page.goto('/settings'); await page.getByRole('tab', { name: '通知偏好', exact: true }).click();
  const toggle = page.getByRole('switch', { name: '任务完成邮件', exact: true }); await expect(toggle).toBeChecked(); await toggle.click();
  await expect(page.getByRole('switch', { name: '告警通知', exact: true })).toBeDisabled(); await expect(page.getByRole('switch', { name: '账单提醒', exact: true })).toBeDisabled(); await expect(page.getByRole('switch', { name: '产品更新', exact: true })).toBeDisabled();
  sql("CREATE FUNCTION k11_prefs_ui_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.notification_prefs IS DISTINCT FROM OLD.notification_prefs THEN RAISE EXCEPTION 'isolated preference rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER k11_prefs_ui_reject BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION k11_prefs_ui_reject();");
  try {
    const result = page.waitForResponse(r => r.url().endsWith('/notification-preferences') && r.request().method() === 'PUT');
    await page.getByRole('button', { name: '保存通知偏好', exact: true }).click(); expect((await result).status()).toBe(503);
    await expect(page.getByRole('alert').last()).toBeVisible(); await expect(toggle).not.toBeChecked();
    expect(sql("SELECT notification_prefs->>'task_completed' FROM users WHERE id=:'uid';", { uid })).toBe('true');
  } finally { sql('DROP TRIGGER k11_prefs_ui_reject ON users; DROP FUNCTION k11_prefs_ui_reject();'); }
  const save = page.getByRole('button', { name: '保存通知偏好', exact: true }); await expect(save).toBeEnabled(); await expect(save).toHaveAttribute('aria-busy', 'false');
  const result = page.waitForResponse(r => r.url().endsWith('/notification-preferences') && r.request().method() === 'PUT'); await save.click(); expect((await result).status()).toBe(200);
  await page.reload(); await page.getByRole('tab', { name: '通知偏好', exact: true }).click(); await expect(page.getByRole('switch', { name: '任务完成邮件', exact: true })).not.toBeChecked();
});
