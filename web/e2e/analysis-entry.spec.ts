import { expect, test, type Page } from '@playwright/test';

const templates = [
  ['品牌日常口碑', '品牌声誉监测'],
  ['新品上市', '舆情事件分析'],
  ['产品质量投诉', '品牌声誉监测'],
  ['竞品动态', '竞品动态追踪'],
  ['突发危机', '舆情事件分析'],
  ['营销活动复盘', '舆情事件分析'],
] as const;

test.setTimeout(90_000);

async function openForm(page: Page, query = '') {
  await page.addInitScript(() => {
    localStorage.setItem('principal', JSON.stringify({ user_id: 'user', tenant_id: 'tenant', email: 'test@example.com', roles: [], plan_code: 'free' }));
    localStorage.setItem('access_token', 'test-token');
  });
  await page.route('**/api/v1/billing/**', (route) => route.fulfill({ json: { balance: 10, tokens_quota: 1000000, tokens_used: 0 } }));
  await page.goto(`/analyses/new${query}`, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('heading', { name: '新建分析' })).toBeVisible();
}

async function submit(page: Page) {
  await page.getByPlaceholder('例如：新品发布会舆情监测').fill('测试分析');
  await page.getByRole('button', { name: '下一步' }).click();
  await page.locator('.ant-select-selection-search-input').fill('测试关键词');
  await page.locator('.ant-select-selection-search-input').press('Enter');
  await page.getByRole('button', { name: '下一步' }).click();
  await page.getByText('微博', { exact: true }).click();
  await page.getByRole('button', { name: '下一步' }).click();
  await expect(page.getByText(/本次分析消耗/)).toBeVisible();
  await page.getByRole('button', { name: '提交分析' }).click();
}

test('six templates map to planned views and submit only legacy fields', async ({ page }) => {
  await openForm(page);
  await page.getByRole('button', { name: '按模板' }).click();
  for (const [label, view] of templates) {
    await page.getByText(label, { exact: true }).click();
    await expect(page.getByText(`默认分析视角：${view}`)).toBeVisible();
  }
  await page.getByText('新品上市', { exact: true }).click();
  let body: Record<string, unknown> | undefined;
  await page.route('**/api/v1/analyses', async (route) => {
    body = route.request().postDataJSON();
    await route.fulfill({ json: { id: 'created-id', state: 'queued' } });
  });
  await submit(page);
  expect(body).toMatchObject({ analysis_type: 'event', name: '测试分析', keywords: ['测试关键词'] });
  expect(body).not.toHaveProperty('template_id');
  expect(body).not.toHaveProperty('schedule');
});

test('template view can be changed before single-run submission', async ({ page }) => {
  await openForm(page);
  await page.getByRole('button', { name: '按模板' }).click();
  await page.getByText('产品质量投诉', { exact: true }).click();
  await page.locator('.ant-select-selector', { hasText: '品牌声誉监测' }).click();
  await page.locator('.ant-select-dropdown .ant-select-item-option-content', { hasText: '舆情事件分析' }).click();
  let body: Record<string, unknown> | undefined;
  await page.route('**/api/v1/analyses', async (route) => {
    body = route.request().postDataJSON();
    await route.fulfill({ json: { id: 'created-id', state: 'queued' } });
  });
  await submit(page);
  expect(body?.analysis_type).toBe('event');
  expect(body).not.toHaveProperty('template_id');
});

test('manual four views and trending keyword prefill remain available', async ({ page }) => {
  await openForm(page, '?keywords=%E7%83%AD%E6%A6%9C%E8%AF%8D');
  await page.getByRole('button', { name: '手动选择视角' }).click();
  for (const view of ['舆情事件分析', '品牌声誉监测', '竞品动态追踪', '行业趋势洞察']) {
    await expect(page.getByText(view, { exact: true })).toBeVisible();
  }
  await page.getByText('行业趋势洞察', { exact: true }).click();
  await page.getByPlaceholder('例如：新品发布会舆情监测').fill('手动分析');
  await page.getByRole('button', { name: '下一步' }).click();
  await expect(page.locator('.ant-select-selection-item', { hasText: '热榜词' })).toBeVisible();
});

test('manual industry view submits the legacy analysis type', async ({ page }) => {
  await openForm(page);
  await page.getByRole('button', { name: '手动选择视角' }).click();
  await page.getByText('行业趋势洞察', { exact: true }).click();
  let body: Record<string, unknown> | undefined;
  await page.route('**/api/v1/analyses', async (route) => {
    body = route.request().postDataJSON();
    await route.fulfill({ json: { id: 'created-id', state: 'queued' } });
  });
  await submit(page);
  expect(body?.analysis_type).toBe('industry');
  expect(body).not.toHaveProperty('template_id');
});

test('manual view remains optional for existing generic analyses', async ({ page }) => {
  await openForm(page);
  await page.getByRole('button', { name: '手动选择视角' }).click();
  let body: Record<string, unknown> | undefined;
  await page.route('**/api/v1/analyses', async (route) => {
    body = route.request().postDataJSON();
    await route.fulfill({ json: { id: 'created-id', state: 'queued' } });
  });
  await submit(page);
  expect(body?.analysis_type).toBeNull();
});

test('unfinished plan configuration is not enabled or scheduled', async ({ page }) => {
  await openForm(page);
  await page.getByRole('button', { name: '按模板' }).click();
  await page.getByText('品牌日常口碑', { exact: true }).click();
  for (const label of ['排除词', '监测周期', '风险标签', '预警规则', '报告模板']) {
    await expect(page.getByText(new RegExp(`${label}.*未启用`))).toBeVisible();
  }
  await expect(page.getByText(/周期监测尚未上线/)).toBeVisible();
});


test('source picker disables unavailable platforms and submits enabled choices', async ({ page }) => {
  await openForm(page);
  await page.getByRole('button', { name: '手动选择视角' }).click();
  await page.getByPlaceholder('例如：新品发布会舆情监测').fill('来源选择验证');
  await page.getByRole('button', { name: '下一步' }).click();
  await page.locator('.ant-select-selection-search-input').fill('品牌');
  await page.locator('.ant-select-selection-search-input').press('Enter');
  await page.getByRole('button', { name: '下一步' }).click();
  for (const label of ['抖音', '今日头条', '西瓜视频', '微博', '公众号']) {
    await expect(page.getByRole('checkbox', { name: label })).toBeEnabled();
  }
  for (const label of ['小红书', 'B站']) {
    await expect(page.getByRole('checkbox', { name: label })).toBeDisabled();
  }
  await expect(page.getByText('仅检索公开网页；搜索摘要不是视频正文或评论')).toBeVisible();
  await expect(page.getByText('已支持评论')).toHaveCount(0);
  await page.getByRole('checkbox', { name: '今日头条' }).click();
  await page.getByRole('button', { name: '下一步' }).click();
  let body: Record<string, unknown> | undefined;
  await page.route('**/api/v1/analyses', async (route) => {
    body = route.request().postDataJSON();
    await route.fulfill({ json: { id: 'created-id', state: 'queued' } });
  });
  await page.getByRole('button', { name: '提交分析' }).click();
  expect(body?.sources).toEqual(['toutiao']);
});


test('keyword limit matches query engine contract', async ({ page }) => {
  await openForm(page);
  await page.getByRole('button', { name: '手动选择视角' }).click();
  await page.getByPlaceholder('例如：新品发布会舆情监测').fill('关键词数量');
  await page.getByRole('button', { name: '下一步' }).click();
  const input = page.locator('.ant-select-selection-search-input');
  for (let i=0; i<10; i++) { await input.fill(`word${i}`); await input.press('Enter'); }
  await expect(page.locator('.ant-select-selection-item')).toHaveCount(8);
  await expect(page.getByText('常用补充：')).toHaveCount(0);
});


test('failed analysis explains relevant sample shortage with durable retrieval funnel', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('principal', JSON.stringify({ user_id:'user', tenant_id:'tenant', email:'test@example.com', roles:[], plan_code:'free' }));
    localStorage.setItem('access_token','test-token');
  });
  await page.route('**/api/v1/analyses/sample-id', (route) => route.fulfill({json:{
    id:'sample-id', name:'GS8', state:'failed', progress:35, doc_count:1, error_code:'insufficient_relevant_evidence',
    retrieval_coverage:{admission_version:'lexical-v1',provider_candidates:20,unusable_count:17,irrelevant_count:2,accepted_count:1,source_mismatch_count:0}
  }}));
  await page.route('**/api/v1/analyses/sample-id/timeline**', (route) => route.fulfill({json:{nodes:[],edges:[],unlocated:[],coverage:{source_count:1,duplicate_count:0,timed_count:0,unlocated_count:0,relation_reason:''},warnings:[],next_cursor:''}}));
  await page.goto('/analyses/sample-id');
  await expect(page.getByText('有效证据不足')).toBeVisible();
  await expect(page.getByText('搜索候选 20 条')).toBeVisible();
  await expect(page.getByText('有效证据 1 条')).toBeVisible();
});
