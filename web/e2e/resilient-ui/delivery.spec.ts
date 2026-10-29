import { expect, test } from '@playwright/test';

test('brief delivery shows collection channels and resumes as a new accepted run', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('access_token', 'offline-ui-token'));
  await page.route('**/api/v1/**', route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/auth/me')) return route.fulfill({ json: { user_id: 'ui-user', tenant_id: 'ui-tenant',
      email: 'ui@example.invalid', name: '界面验收', roles: ['admin'], user_status: 'active', tenant_status: 'active',
      plan_code: 'pro', timezone: 'Asia/Shanghai', avatar_url: '' } });
    if (path.endsWith('/reports')) return route.fulfill({ json: { reports: [], total: 0 } });
    return route.fulfill({ json: {} });
  });
  const aid = 'resilient-ui-brief';
  const coverage = { admission_version: 'semantic-v2', provider_candidates: 0, accepted_count: 0,
    discovery_channels: [{ source: 'toutiao', channel: 'local_discovery', status: 'ok', discovered_count: 3,
      body_count: 2, summary_count: 0, accepted_count: 0, failed_count: 1 }],
    per_platform: [], candidate_audit: [],
  };
  await page.route(`**/api/v1/analyses/${aid}`, route => route.fulfill({ json: {
    id: aid, name: '供应商故障仍交付资料', state: 'completed', progress: 100,
    delivery_level: 'brief', delivery_reasons: ['模型暂不可用，材料相关性待核验'],
    retrieval_coverage: coverage,
  } }));
  await page.route(`**/api/v1/analyses/${aid}/result`, route => route.fulfill({ json: {
    id: aid, state: 'completed', delivery_level: 'brief', delivery_reasons: ['模型暂不可用，材料相关性待核验'],
    documents: [], sentiments: { positive: 0, negative: 0, neutral: 0, items: [] }, topics: [], dimensions: [],
    report: { id: 'saved-brief', format: 'html', content: '<html><body><h1>资料简报</h1><p>尚未进行语义研判</p></body></html>' },
  } }));
  await page.route(`**/api/v1/analyses/${aid}/timeline*`, route => route.fulfill({ json: {
    nodes: [], edges: [], unlocated: [], coverage: { scope: 'collected', source_count: 0, duplicate_count: 0,
      timed_count: 0, unlocated_count: 0, relation_reason: '' },
  } }));
  await page.goto(`/analyses/${aid}`);
  await expect(page.getByText('资料简报', { exact: true }).first()).toBeVisible();
  await expect(page.getByText('模型暂不可用，材料相关性待核验', { exact: true }).first()).toBeVisible();
  await expect(page.getByRole('heading', { name: '采集通路' })).toBeVisible();
  await expect(page.getByRole('cell', { name: '自主发现', exact: true })).toBeVisible();
  let resumed = false;
  await page.route(`**/api/v1/analyses/${aid}/resume`, route => {
    expect(route.request().method()).toBe('POST');
    resumed = true;
    return route.fulfill({ json: { id: aid, current_run_id: 'new-accepted-run', state: 'queued' } });
  });
  await page.getByRole('button', { name: '继续补充', exact: true }).click();
  await expect(page.getByText('继续补充将创建新一轮运行，并按当前套餐接纳；原报告版本仍可下载。', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '确认继续', exact: true }).click();
  await expect.poll(() => resumed).toBe(true);
  await page.screenshot({ path: 'test-results/resilient-ui/delivery.png', fullPage: true });
});
