import { expect, test } from '@playwright/test';
import { randomBytes } from 'node:crypto';

test('related keywords and optional research question survive confirmation and actual API persistence', async ({ page, request }) => {
  const email = `research-${randomBytes(7).toString('hex')}@example.invalid`;
  const password = 'Research-hosted-sandbox-only-2026!';
  const registered = await request.post('http://127.0.0.1:8080/api/v1/auth/register', {
    data: { email, password, name: '研究主题验收' },
  });
  expect(registered.status()).toBe(201);
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto('/analyses/new');
  await page.getByPlaceholder('例如：新品发布会舆情监测').fill('iPhone18系列新品反馈');
  await page.getByRole('button', { name: '手动选择视角', exact: true }).click();
  await page.getByRole('button', { name: '下一步', exact: true }).click();
  await expect(page.getByText('多个关键词共同描述同一研究主题，材料无需逐词命中。', { exact: true })).toBeVisible();
  for (const keyword of ['iphone18', 'iphone18pro', 'iphone18max']) {
    await page.getByRole('combobox').fill(keyword);
    await page.getByRole('combobox').press('Enter');
  }
  await page.getByLabel('关注问题（选填）', { exact: true }).fill('了解系列新品续航与价格反馈');
  await page.getByRole('button', { name: '下一步', exact: true }).click();
  await page.getByRole('checkbox', { name: '微博', exact: true }).check();
  await page.getByRole('button', { name: '下一步', exact: true }).click();
  await expect(page.getByText('了解系列新品续航与价格反馈', { exact: true })).toBeVisible();
  await expect(page.getByText('46 分钟', { exact: false })).toBeVisible();
  const submitted = page.waitForResponse(response => response.url().endsWith('/api/v1/analyses') && response.request().method() === 'POST');
  await page.getByRole('button', { name: '提交分析', exact: true }).click();
  const response = await submitted;
  expect(response.status()).toBe(201);
  expect(response.request().postDataJSON()).toMatchObject({ research_question: '了解系列新品续航与价格反馈', keywords: ['iphone18', 'iphone18pro', 'iphone18max'] });
  await expect(page).toHaveURL(/\/analyses\/[^/]+$/);
  await page.reload();
  await expect(page.getByText('了解系列新品续航与价格反馈', { exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/account-admin/analysis-research-question.png' });
});

test('semantic audit distinguishes empty results, failed platforms, background and unused candidates', async ({ page, request }) => {
  const email = `research-audit-${randomBytes(7).toString('hex')}@example.invalid`;
  const password = 'Research-hosted-sandbox-only-2026!';
  expect((await request.post('http://127.0.0.1:8080/api/v1/auth/register', { data: { email, password, name: '采集审计验收' } })).status()).toBe(201);
  await page.goto('/login');
  await page.getByLabel('邮箱', { exact: true }).fill(email);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: /^登\s*录$/ }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  // This renderer fixture deliberately contains distinct failures; actual API persistence is verified above.
  await page.route('**/api/v1/analyses/semantic-audit-fixture', route => route.fulfill({ json: {
    id: 'semantic-audit-fixture', name: '多平台采集审计', state: 'failed', progress: 25, doc_count: 1,
    research_intent: { version: 'semantic-v2', topic: 'iPhone18系列续航研究', entities: ['iPhone18 Pro'], focus: ['续航'] },
    retrieval_coverage: { admission_version: 'semantic-v2', provider_candidates: 4, accepted_count: 2, direct_count: 1, context_count: 1,
      unusable_count: 0, irrelevant_count: 1, source_mismatch_count: 0,
      per_platform: [
        { source: 'weibo', status: 'queried', queries: 1, provider_candidates: 4, processed_count: 3, body_count: 2, summary_count: 1, direct_count: 1, context_count: 1, rejected_count: 1, unprocessed_count: 1, failed_count: 0 },
        { source: 'douyin', status: 'failed', queries: 1, provider_candidates: 0, processed_count: 0, direct_count: 0, context_count: 0, failed_count: 1 },
        { source: 'wechat', status: 'queried', queries: 1, provider_candidates: 0, processed_count: 0, direct_count: 0, context_count: 0 },
      ],
      candidate_audit: [{ id: 'candidate-rejected', title: '其他品牌续航广告', url: 'https://weibo.com/original', final_url: 'https://weibo.com/final',
        source_type: 'weibo', content_kind: 'body', fetch_status: 'success', status: 'rejected', reason: 'semantic_irrelevant',
        semantic_classification: 'irrelevant', semantic_reason: '材料只讨论其他品牌，与本轮研究对象无关',
        date_sources: { provider: '2026-10-10', body: '2026-10-10' }, evidence_spans: ['其他品牌广告'] }],
    },
  } }));
  await page.route('**/api/v1/analyses/semantic-audit-fixture/timeline*', route => route.fulfill({ json: {
    nodes: [], edges: [], unlocated: [], coverage: { scope: 'fixture', source_count: 0, duplicate_count: 0, timed_count: 0, unlocated_count: 0, relation_reason: '' }, warnings: [], next_cursor: '',
  } }));
  await page.goto('/analyses/semantic-audit-fixture');
  await expect(page.getByText('iPhone18系列续航研究', { exact: true })).toBeVisible();
  await expect(page.getByText('直接证据 1 条', { exact: true })).toBeVisible();
  await expect(page.getByText('背景材料 1 条', { exact: true })).toBeVisible();
  await expect(page.getByText('检索失败', { exact: true })).toBeVisible();
  await expect(page.getByText('已检索，无候选', { exact: true })).toBeVisible();
  await page.getByText('候选材料与处理原因', { exact: true }).click();
  await expect(page.getByText('材料只讨论其他品牌，与本轮研究对象无关', { exact: true })).toBeVisible();
  await expect(page.getByText('其他品牌广告', { exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/account-admin/analysis-semantic-audit.png' });
});
