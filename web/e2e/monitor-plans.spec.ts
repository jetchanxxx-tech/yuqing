import { expect, test, type Page } from '@playwright/test';

test.setTimeout(90_000);

const unavailable = (source: string, reason: string) => ({ state: 'unavailable', source, reason });
const preview = {
  analysis_type_resolution: { analysis_type: 'brand', source: 'template_default', explanation: '品牌视角' },
  config: {
    keywords: { state: 'proposed', value: ['示例品牌'], source: 'input:brand_name' },
    exclude_words: { state: 'proposed', value: [], source: 'template:review_required' },
    sources: unavailable('runtime:news', '来源未授权或套餐不支持'),
    monitoring_cycle: unavailable('runtime:scheduler', '调度未接通'),
    risk_tags: unavailable('runtime:rules', '标签未接通'),
    alert_rules: unavailable('runtime:notifications', '预警未接通'),
    report_template: unavailable('runtime:renderer', '模板未接通'),
  },
  warnings: ['sources: 来源未授权或套餐不支持'],
};

async function openPage(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('principal', JSON.stringify({ user_id: 'user', tenant_id: 'tenant', email: 'test@example.com', roles: ['analyst'], plan_code: 'free' }));
    localStorage.setItem('access_token', 'test-token');
  });
  await page.goto('/monitor-plans', { waitUntil: 'domcontentloaded' });
}

test('preview edits, save draft, reopen, and PATCH with revision without enabling execution', async ({ page }) => {
  const templates = [{ template_id: 'brand_daily', template_version: 1, name: '品牌日常口碑', default_analysis_type: 'brand' }];
  let saved: Record<string, unknown> | undefined;
  const posts: Record<string, unknown>[] = [];
  const patches: Record<string, unknown>[] = [];
  await page.route('**/api/v1/monitor-plans**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (path.endsWith('/templates')) return route.fulfill({ json: { templates } });
    if (path.endsWith('/preview')) { posts.push(request.postDataJSON()); return route.fulfill({ json: preview }); }
    if (path.endsWith('/monitor-plans') && request.method() === 'GET') return route.fulfill({ json: { plans: saved ? [saved] : [], total: saved ? 1 : 0, limit: 20, offset: 0 } });
    if (path.endsWith('/monitor-plans') && request.method() === 'POST') {
      const body = request.postDataJSON();
      posts.push(body);
      saved = { ...body, plan_id: 'draft-1', revision: 1, state: 'draft', tenant_id: 'tenant', owner_id: 'user' };
      return route.fulfill({ status: 201, json: saved });
    }
    if (path.endsWith('/draft-1') && request.method() === 'GET') return route.fulfill({ json: saved });
    if (path.endsWith('/draft-1') && request.method() === 'PATCH') {
      const body = request.postDataJSON();
      patches.push(body);
      if (body.revision !== saved?.revision) return route.fulfill({ status: 409, json: { code: 'CONFLICT', message: 'stale revision', request_id: 'r' } });
      saved = { ...saved, ...body, revision: 2 };
      return route.fulfill({ json: saved });
    }
    return route.abort();
  });
  await openPage(page);
  await expect(page.getByRole('heading', { name: '监测方案草稿' })).toBeVisible();
  await page.getByRole('button', { name: '新建草稿' }).click();
  await page.getByRole('button', { name: '品牌日常口碑' }).click();
  await page.getByRole('textbox', { name: '关注主体' }).fill('示例品牌');
  await page.getByRole('button', { name: '预览候选配置' }).click();
  await expect(page.getByRole('textbox', { name: '关键词' })).toHaveValue('示例品牌');
  await expect(page.getByText('来源未授权或套餐不支持', { exact: true })).toBeVisible();
  await expect(page.getByText('监测周期：不可用')).toBeVisible();
  await expect(page.getByRole('combobox', { name: '数据源候选' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '运行方案' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '发送预警' })).toHaveCount(0);
  await page.getByRole('textbox', { name: '关键词' }).fill('修改词, 第二个词');
  await page.getByRole('textbox', { name: '排除词' }).fill('噪声');
  await page.getByRole('button', { name: '保存草稿' }).click();
  expect(posts[0]).toMatchObject({ template_id: 'brand_daily', template_version: 1, inputs: { brand_name: '示例品牌' } });
  expect(posts[1]).toMatchObject({ template_id: 'brand_daily', analysis_type: 'brand', state: 'draft' });
  expect((posts[1].config as typeof preview.config).keywords.value).toEqual(['修改词', '第二个词']);
  expect((posts[1].config as typeof preview.config).exclude_words.value).toEqual(['噪声']);
  expect((posts[1].config as typeof preview.config).sources.state).toBe('unavailable');
  await page.getByRole('button', { name: '返回草稿列表' }).click();
  await page.getByRole('button', { name: '打开草稿' }).click();
  await expect(page.getByRole('textbox', { name: '关键词' })).toHaveValue('修改词, 第二个词');
  await page.getByRole('textbox', { name: '关键词' }).fill('再次编辑');
  await page.getByRole('button', { name: '保存修改' }).click();
  expect(patches[0]).toMatchObject({ revision: 1, state: 'draft' });
  expect((patches[0].config as typeof preview.config).keywords.value).toEqual(['再次编辑']);
  saved = { ...saved, revision: 3 };
  await page.getByRole('textbox', { name: '关键词' }).fill('冲突编辑');
  await page.getByRole('button', { name: '保存修改' }).click();
  await expect(page.getByText(/版本冲突：草稿已被修改/)).toBeVisible();
  expect(patches[1].revision).toBe(2);
});

test('preview refusal keeps draft save disabled and does not bypass plan checks', async ({ page }) => {
  let creates = 0;
  await page.route('**/api/v1/monitor-plans**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/templates')) return route.fulfill({ json: { templates: [{ template_id: 'brand_daily', template_version: 1, name: '品牌日常口碑', default_analysis_type: 'brand' }] } });
    if (path.endsWith('/preview')) return route.fulfill({ status: 403, json: { code: 'FORBIDDEN', message: '套餐无权限', request_id: 'r' } });
    if (route.request().method() === 'POST') creates++;
    return route.fulfill({ json: { plans: [], total: 0 } });
  });
  await openPage(page);
  await page.getByRole('button', { name: '新建草稿' }).click();
  await page.getByRole('button', { name: '品牌日常口碑' }).click();
  await page.getByRole('textbox', { name: '关注主体' }).fill('示例品牌');
  await page.getByRole('button', { name: '预览候选配置' }).click();
  await expect(page.getByText(/预览失败/)).toBeVisible();
  await expect(page.getByRole('button', { name: '保存草稿' })).toBeDisabled();
  expect(creates).toBe(0);
});

test('list loads older drafts beyond the first page and reopens them', async ({ page }) => {
  const plan = (index: number) => ({ plan_id: `draft-${index}`, name: `第${index}份草稿`, revision: 1, state: 'draft',
    template_id: 'brand_daily', template_version: 1, analysis_type: 'brand', inputs: { brand_name: `品牌${index}` }, config: preview.config });
  const older = plan(21);
  await page.route('**/api/v1/monitor-plans**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/templates')) return route.fulfill({ json: { templates: [] } });
    if (url.pathname.endsWith('/draft-21')) return route.fulfill({ json: older });
    if (url.searchParams.get('offset') === '20') return route.fulfill({ json: { plans: [older], total: 1 } });
    return route.fulfill({ json: { plans: Array.from({ length: 20 }, (_, index) => plan(index + 1)), total: 20 } });
  });
  await openPage(page);
  await page.getByRole('button', { name: '加载更多' }).click();
  await expect(page.getByText('第21份草稿')).toBeVisible();
  await page.getByText('第21份草稿').locator('xpath=ancestor::div[contains(@class,"ant-card-body")][1]').getByRole('button', { name: '打开草稿' }).click();
  await expect(page.getByRole('textbox', { name: '关键词' })).toHaveValue('示例品牌');
});
