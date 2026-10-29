import { expect, test } from '@playwright/test';
import { randomBytes } from 'node:crypto';
import { execFileSync } from 'node:child_process';

const db = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const api = 'http://127.0.0.1:8080/api/v1';

test('failed analysis explains a recorded supplier content rejection', async ({ page, request }) => {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== db) throw new Error('Hosted disposable database required');
  const email = `rejection-${randomBytes(8).toString('hex')}@example.invalid`;
  const password = `Rejection-2026-${randomBytes(18).toString('hex')}`;
  const response = await request.post(`${api}/auth/register`, { data: { email, password, name: '供应商响应验收' } });
  expect(response.status()).toBe(201);
  const actor = await response.json();
  const aid = `rejection-${actor.user.user_id}`;
  const coverage = { admission_version: 'semantic-v2', insight_material_coverage: {
    version: 'complete-chunks-v1', phases: [{ phase: 'dimension:heat', read_spans: [], unprocessed: [
      { document_id: 'doc-a', start: 0, end: 20, material_class: 'direct',
        reason: '模型供应商内容审核拒绝处理本次材料', code: 'llm_content_rejected' },
    ] }],
  } };
  execFileSync('psql', [db, '-v', 'ON_ERROR_STOP=1', '-v', `aid=${aid}`,
    '-v', `uid=${actor.user.user_id}`, '-v', `tid=${actor.user.tenant_id}`, '-v', `coverage=${JSON.stringify(coverage)}`], {
    input: `INSERT INTO analyses(id,tenant_id,created_by,name,state,progress,error_code,retrieval_coverage)
      VALUES(:'aid',:'tid',:'uid','供应商响应验收','failed',60,'insight_failed',:'coverage'::jsonb);`, encoding: 'utf8',
  });
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto(`/analyses/${aid}`);
  await expect(page.getByText('当前模型供应商未通过部分材料的内容审核，本次报告未生成。请联系管理员核查供应商返回记录。', { exact: true })).toBeVisible();
  await expect(page.getByText('采集未完成，请检查关键词、所选来源及采集记录后重试。', { exact: true })).toHaveCount(0);
  await page.screenshot({ path: 'test-results/account-admin/provider-rejection.png', fullPage: true });
});
