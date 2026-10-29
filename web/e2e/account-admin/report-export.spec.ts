import { expect, test } from '@playwright/test';
import { randomBytes } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

const db = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const api = 'http://127.0.0.1:8080/api/v1';

test('saved report HTML downloads exact published bytes and only implemented export actions are offered', async ({ page, request }) => {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== db) throw new Error('Hosted disposable database required');
  const email = `report-${randomBytes(8).toString('hex')}@example.invalid`;
  const password = randomBytes(18).toString('hex');
  const response = await request.post(`${api}/auth/register`, { data: { email, password, name: '报告导出验收' } });
  expect(response.status()).toBe(201);
  const actor = await response.json();
  const aid = `report-a-${actor.user.user_id}`, rid = `report-r-${actor.user.user_id}`;
  const html = '<!doctype html><html><body><h1>盘古舆情报告原文</h1><p>保存后保持原文。</p></body></html>';
  execFileSync('psql', [db, '-v', 'ON_ERROR_STOP=1', '-v', `aid=${aid}`, '-v', `rid=${rid}`,
    '-v', `uid=${actor.user.user_id}`, '-v', `tid=${actor.user.tenant_id}`, '-v', `html=${html}`], {
    input: `INSERT INTO analyses(id,tenant_id,created_by,name,state,progress,report_content) VALUES(:'aid',:'tid',:'uid','报告原文','completed',100,:'html');
      INSERT INTO reports(id,tenant_id,created_by,analysis_id,format,status) VALUES(:'rid',:'tid',:'uid',:'aid','html','completed');
      UPDATE report_credits SET plan_code='pro',plan_source='manual' WHERE tenant_id=:'tid';`, encoding: 'utf8',
  });
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto('/reports');
  const row = page.getByRole('row').filter({ hasText: aid });
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: '下载', exact: true }).hover();
  await expect(page.getByRole('menuitem', { name: '下载 HTML', exact: true })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '下载 Word', exact: true })).toBeVisible();
  await expect(page.getByRole('menuitem', { name: '下载 Markdown', exact: true })).toHaveCount(0);
  await expect(page.getByRole('menuitem', { name: '下载 PDF', exact: true })).toHaveCount(0);
  const downloadEvent = page.waitForEvent('download');
  await page.getByRole('menuitem', { name: '下载 HTML', exact: true }).click();
  const download = await downloadEvent;
  expect(download.suggestedFilename()).toBe(`report-${rid}.html`);
  expect(readFileSync((await download.path())!, 'utf8')).toBe(html);
  await page.screenshot({ path: 'test-results/account-admin/report-export.png', fullPage: true });
});
