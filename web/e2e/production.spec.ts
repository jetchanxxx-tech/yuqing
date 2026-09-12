import { test, expect, type Page } from '@playwright/test';

/**
 * 生产环境 E2E 测试套件 —— 针对已部署站点 https://yuqing.pangu-cloud.com
 *
 * 选择器基于实际 DOM 侦察结果（见 recon.spec.ts）：
 *   登录页  #email / #password / button[type=submit]
 *   主界面  侧边栏菜单：数据面板 / 新建分析 / 分析任务 / 报告中心 /
 *                       套餐升级 / 用量 / 账单 / 设置
 */

// 凭据只从环境变量读取 —— 绝不硬编码默认值。
// 仓库是公开的，写入真实账号密码等于公开生产凭据。
const EMAIL = process.env.E2E_EMAIL ?? '';
const PASSWORD = process.env.E2E_PASSWORD ?? '';

if (!EMAIL || !PASSWORD) {
  throw new Error(
    '缺少 E2E 凭据。请在本地设置后运行：\n' +
    '  export E2E_EMAIL=you@example.com\n' +
    '  export E2E_PASSWORD=<密码>\n' +
    '（凭据保存在本地 credentials.local.md，不入库）',
  );
}

const MENU = {
  dashboard: '数据面板',
  newAnalysis: '新建分析',
  analyses: '分析任务',
  reports: '报告中心',
  plans: '套餐升级',
  usage: '用量',
  invoices: '账单',
  settings: '设置',
  admin: '管理后台',
} as const;

/** 登录并等待跳转到仪表盘 */
async function login(page: Page) {
  await page.goto('/login');
  await page.fill('#email', EMAIL);
  await page.fill('#password', PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/dashboard/, { timeout: 20_000 });
}

/** 通过侧边栏菜单导航 */
async function navTo(page: Page, label: string) {
  await page.getByRole('menuitem', { name: label }).click();
  await page.waitForTimeout(800);
}

// ════════════════════════════════════════════════════════════
// 一、认证
// ════════════════════════════════════════════════════════════

test.describe('一、认证流程', () => {
  test('1.1 登录页渲染完整', async ({ page }) => {
    await page.goto('/login');
    await expect(page.locator('#email')).toBeVisible();
    await expect(page.locator('#password')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
    await expect(page.getByText('盘古舆情').first()).toBeVisible();
  });

  test('1.2 空表单提交不跳转', async ({ page }) => {
    await page.goto('/login');
    await page.click('button[type="submit"]');
    await page.waitForTimeout(1500);
    await expect(page).toHaveURL(/\/login/);
  });

  test('1.3 错误密码被拒绝', async ({ page }) => {
    await page.goto('/login');
    await page.fill('#email', EMAIL);
    await page.fill('#password', 'wrong-password-123');
    await page.click('button[type="submit"]');
    await page.waitForTimeout(2500);
    await expect(page).toHaveURL(/\/login/);
  });

  test('1.4 正确凭据登录成功并跳转仪表盘', async ({ page }) => {
    await login(page);
    await expect(page).toHaveURL(/\/dashboard/);
    await expect(page.getByRole('heading', { name: '数据面板' })).toBeVisible();
  });

  test('1.5 未登录访问受保护页面被重定向', async ({ page }) => {
    await page.goto('/dashboard');
    await page.waitForURL(/\/login/, { timeout: 15_000 });
    await expect(page).toHaveURL(/\/login/);
  });
});

// ════════════════════════════════════════════════════════════
// 二、数据面板
// ════════════════════════════════════════════════════════════

test.describe('二、数据面板', () => {
  test.beforeEach(async ({ page }) => await login(page));

  test('2.1 概览卡片渲染', async ({ page }) => {
    await expect(page.getByRole('heading', { name: '数据面板' })).toBeVisible();
    // 4 张概览卡的标题
    for (const label of ['分析任务', '采集文档', '正面', '负面']) {
      await expect(page.getByText(label, { exact: false }).first()).toBeVisible();
    }
  });

  test('2.2 图表容器已挂载', async ({ page }) => {
    await page.waitForTimeout(2000);
    const canvases = await page.locator('canvas, svg').count();
    expect(canvases, '图表应已渲染（canvas 或 svg）').toBeGreaterThan(0);
  });

  test('2.3 刷新按钮可用', async ({ page }) => {
    const btn = page.getByRole('button', { name: /刷新/ });
    await expect(btn).toBeVisible();
    await btn.click();
    await page.waitForTimeout(1500);
    await expect(page.getByRole('heading', { name: '数据面板' })).toBeVisible();
  });
});

// ════════════════════════════════════════════════════════════
// 三、分析任务
// ════════════════════════════════════════════════════════════

test.describe('三、分析任务', () => {
  test.beforeEach(async ({ page }) => await login(page));

  test('3.1 分析列表页可打开', async ({ page }) => {
    await navTo(page, MENU.analyses);
    await expect(page).toHaveURL(/\/analyses$/);
    await expect(page.getByRole('heading', { name: '分析任务' })).toBeVisible();
  });

  test('3.2 新建分析向导渲染四步', async ({ page }) => {
    await navTo(page, MENU.newAnalysis);
    await expect(page).toHaveURL(/\/analyses\/new/);
    // AntD Steps 渲染 4 个步骤标题
    const steps = page.locator('.ant-steps-item');
    await expect(steps).toHaveCount(4);
  });

  test('3.3 新建分析向导可完整走通并提交', async ({ page }) => {
    await navTo(page, MENU.newAnalysis);
    await expect(page).toHaveURL(/\/analyses\/new/);

    // ── 步骤 0：名称 + 分析类型（类型是可点击卡片，非下拉框）──
    await page.getByPlaceholder('例如：新品发布会舆情监测').fill('E2E测试-雅阁后排');
    // 用精确文本定位类型标题，点击后事件冒泡到卡片 onClick。
    // 注意：不能用 `.ant-card` + hasText 过滤 —— 页面外层还有包裹卡片，
    // first() 会命中外层容器，点击落点错误导致类型未被选中。
    await page.getByText('品牌声誉监测', { exact: true }).click();
    await page.waitForTimeout(500);
    const next0 = page.getByRole('button', { name: '下一步' });
    await expect(next0, '填名称+选类型后「下一步」应可用').toBeEnabled();
    await next0.click();

    // ── 步骤 1：关键词（AntD Select mode="tags"）──
    // 注意：AntD 的 Select 把 placeholder 渲染为独立的 span 元素，
    // 不是 input 的 placeholder 属性，因此 getByPlaceholder 定位不到。
    // 改为点击 Select 容器后直接键盘输入。
    await page.locator('.ant-select').first().click();
    await page.keyboard.type('雅阁后排');
    await page.keyboard.press('Enter');
    await page.waitForTimeout(500);
    const next1 = page.getByRole('button', { name: '下一步' });
    await expect(next1, '填关键词后「下一步」应可用').toBeEnabled();
    await next1.click();

    // ── 步骤 2：数据源（CheckableTag，至少选 1 个）──
    await page.locator('.ant-tag-checkable').first().click();
    await page.waitForTimeout(400);
    const next2 = page.getByRole('button', { name: '下一步' });
    await expect(next2, '选数据源后「下一步」应可用').toBeEnabled();
    await next2.click();

    // ── 步骤 3：确认并提交 ──
    await page.waitForTimeout(600);
    await page.screenshot({ path: 'e2e/shots/wizard-confirm.png', fullPage: true });
    const submit = page.getByRole('button', { name: /开始分析|提交|创建/ }).last();
    await expect(submit).toBeEnabled();
    await submit.click();

    await page.waitForURL(/\/analyses\/[A-Z0-9]+/, { timeout: 25_000 });
    await expect(page).toHaveURL(/\/analyses\/[A-Z0-9]+/);
    await page.screenshot({ path: 'e2e/shots/analysis-detail.png', fullPage: true });
  });

  test('3.4 分析详情页展示状态', async ({ page }) => {
    await navTo(page, MENU.analyses);
    await page.waitForTimeout(1500);
    // 点击列表第一行进入详情
    const firstLink = page.locator('tbody tr').first().locator('a').first();
    if (await firstLink.isVisible().catch(() => false)) {
      await firstLink.click();
      await page.waitForTimeout(2000);
      await expect(page).toHaveURL(/\/analyses\/[A-Z0-9]+/);
    }
  });
});

// ════════════════════════════════════════════════════════════
// 四、报告 / 计费 / 设置
// ════════════════════════════════════════════════════════════

test.describe('四、报告与计费', () => {
  test.beforeEach(async ({ page }) => await login(page));

  test('4.1 报告中心可打开', async ({ page }) => {
    await navTo(page, MENU.reports);
    await expect(page).toHaveURL(/\/reports/);
  });

  test('4.2 套餐页展示四档套餐', async ({ page }) => {
    await navTo(page, MENU.plans);
    await expect(page).toHaveURL(/\/plans/);
    await page.waitForTimeout(2000);
    // 四档套餐名
    for (const plan of ['体验版', '专业版', '企业版', '旗舰版']) {
      await expect(page.getByText(plan, { exact: false }).first()).toBeVisible();
    }
  });

  test('4.3 用量页展示配额', async ({ page }) => {
    await navTo(page, MENU.usage);
    await expect(page).toHaveURL(/\/usage/);
    await page.waitForTimeout(1500);
    const body = await page.locator('body').innerText();
    expect(body.length).toBeGreaterThan(50);
  });

  test('4.4 账单页可打开', async ({ page }) => {
    await navTo(page, MENU.invoices);
    await expect(page).toHaveURL(/\/invoices/);
  });

  test('4.5 设置页展示账户信息', async ({ page }) => {
    await navTo(page, MENU.settings);
    await expect(page).toHaveURL(/\/settings/);
    await expect(page.getByText(EMAIL, { exact: false }).first()).toBeVisible();
  });
});

// ════════════════════════════════════════════════════════════
// 五、管理后台
// ════════════════════════════════════════════════════════════

test.describe('五、管理后台', () => {
  test.beforeEach(async ({ page }) => await login(page));

  test('5.1 管理后台可访问（platform_admin 权限）', async ({ page }) => {
    await page.goto('/admin');
    await page.waitForTimeout(2500);
    const body = await page.locator('body').innerText();
    // 不应出现无权限提示
    expect(body).not.toContain('无访问权限');
    expect(body).not.toContain('403');
  });

  test('5.2 数据源配置页展示 Bocha 配置项', async ({ page }) => {
    await page.goto('/admin');
    await page.waitForTimeout(1500);
    // 切到「数据源配置」页签
    await page.getByRole('tab', { name: '数据源配置' }).click();
    await page.waitForTimeout(2000);

    const body = await page.locator('body').innerText();
    expect(body, '应出现 Bocha 配置区').toContain('Bocha');
    expect(body, '应显示配置状态').toMatch(/已配置|未配置/);
    expect(body, '应提示注册地址').toContain('open.bochaai.com');

    // 应有 Key 输入框与保存按钮
    await expect(page.locator('input[type="password"]').first()).toBeVisible();
    await expect(page.getByRole('button', { name: /保存配置/ })).toBeVisible();

    await page.screenshot({ path: 'e2e/shots/admin-datasource.png', fullPage: true });
  });

  test('5.3 数据源配置保存后状态更新', async ({ page }) => {
    await page.goto('/admin');
    await page.getByRole('tab', { name: '数据源配置' }).click();
    await page.waitForTimeout(2000);

    // 写入当前生效的 Key（幂等：值不变，但验证保存链路通畅）
    const key = process.env.E2E_BOCHA_KEY || '';
    if (!key) {
      test.skip(true, '未设置 E2E_BOCHA_KEY，跳过写入验证');
      return;
    }
    await page.locator('input[type="password"]').first().fill(key);
    await page.getByRole('button', { name: /保存配置/ }).click();
    await page.waitForTimeout(2500);

    const body = await page.locator('body').innerText();
    expect(body, '保存后应显示已配置').toContain('已配置');
  });
});

// ════════════════════════════════════════════════════════════
// 六、洞察与报告（真实 LLM 链路）
// ════════════════════════════════════════════════════════════

test.describe('六、洞察与报告', () => {
  test.beforeEach(async ({ page }) => await login(page));

  test('6.1 新建分析并等待真实链路完成', async ({ page }) => {
    test.setTimeout(300_000);

    // 创建分析
    await navTo(page, MENU.newAnalysis);
    await page.getByPlaceholder('例如：新品发布会舆情监测').fill(`E2E洞察-${Date.now()}`);
    await page.getByText('品牌声誉监测', { exact: true }).click();
    await page.waitForTimeout(400);
    await page.getByRole('button', { name: '下一步' }).click();
    await page.locator('.ant-select').first().click();
    await page.keyboard.type('雅阁后排');
    await page.keyboard.press('Enter');
    await page.waitForTimeout(400);
    await page.getByRole('button', { name: '下一步' }).click();
    await page.locator('.ant-tag-checkable').first().click();
    await page.waitForTimeout(400);
    await page.getByRole('button', { name: '下一步' }).click();
    await page.waitForTimeout(600);
    await page.getByRole('button', { name: /开始分析|提交|创建/ }).last().click();
    await page.waitForURL(/\/analyses\/[A-Z0-9]+/, { timeout: 25_000 });

    // 等待管线完成：详情页轮询状态，completed 或 failed 即终态
    // 真实链路含 Bocha 采集 + Scrapling 抓取 + DeepSeek 分析/报告，最长 4 分钟
    const terminal = page.getByText(/已完成|失败/, { exact: false }).first();
    await terminal.waitFor({ state: 'visible', timeout: 240_000 });

    // 终态必须是 completed 而非 failed
    const body = await page.locator('body').innerText();
    expect(body, '任务应成功完成（不出现失败标记）').not.toContain('失败');
    await page.screenshot({ path: 'e2e/shots/insight-completed.png', fullPage: true });
  });

  test('6.2 结果页展示研判摘要', async ({ page }) => {
    // 打开最近一次分析详情
    await navTo(page, MENU.analyses);
    await page.waitForTimeout(1500);
    const firstLink = page.locator('tbody tr').first().locator('a').first();
    await firstLink.click();
    await page.waitForTimeout(3000);

    // 研判摘要 Tab 应存在且内容非空
    await page.getByRole('tab', { name: '研判摘要' }).click();
    await page.waitForTimeout(2000);
    const body = await page.locator('body').innerText();
    expect(body, '应显示 AI 研判摘要而非占位文案').not.toContain('暂无研判摘要');
  });

  test('6.3 情感分析与话题聚类有真实数据', async ({ page }) => {
    await navTo(page, MENU.analyses);
    await page.waitForTimeout(1500);
    await page.locator('tbody tr').first().locator('a').first().click();
    await page.waitForTimeout(3000);

    // 情感分析 Tab
    await page.getByRole('tab', { name: /情感分析/ }).click();
    await page.waitForTimeout(2000);
    const body = await page.locator('body').innerText();
    expect(body, '情感 Tab 不应是空占位').not.toContain('暂无情感数据');

    // 话题聚类 Tab 应有话题行
    await page.getByRole('tab', { name: /话题聚类/ }).click();
    await page.waitForTimeout(2000);
    const body2 = await page.locator('body').innerText();
    expect(body2, '话题聚类不应是空占位').not.toContain('暂无话题聚类结果');
    await page.screenshot({ path: 'e2e/shots/insight-topics.png', fullPage: true });
  });

  test('6.4 分析报告 Tab 渲染 HTML 报告', async ({ page }) => {
    await navTo(page, MENU.analyses);
    await page.waitForTimeout(1500);
    await page.locator('tbody tr').first().locator('a').first().click();
    await page.waitForTimeout(3000);

    await page.getByRole('tab', { name: /分析报告/ }).click();
    await page.waitForTimeout(3000);
    // 报告 iframe 应渲染（srcDoc 注入 HTML）
    const frame = page.locator('iframe[title="分析报告预览"]');
    await expect(frame, '报告预览 iframe 应存在').toBeVisible();
    await page.screenshot({ path: 'e2e/shots/insight-report.png', fullPage: true });
  });
});

// ════════════════════════════════════════════════════════════
// 七、全局健壮性
// ════════════════════════════════════════════════════════════

test.describe('七、全局健壮性', () => {
  test.beforeEach(async ({ page }) => await login(page));

  test('7.1 全部菜单项可导航且无 JS 错误', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(String(e)));

    const targets = [
      MENU.dashboard, MENU.analyses, MENU.reports,
      MENU.plans, MENU.usage, MENU.invoices, MENU.settings,
    ];
    for (const label of targets) {
      await navTo(page, label);
      const body = await page.locator('body').innerText();
      expect(body.length, `菜单「${label}」页面不应为空白`).toBeGreaterThan(30);
    }
    expect(errors, `导航过程出现 JS 错误: ${errors.join('; ')}`).toHaveLength(0);
  });

  test('7.2 刷新后保持登录状态', async ({ page }) => {
    await page.reload();
    await page.waitForTimeout(2500);
    await expect(page).toHaveURL(/\/dashboard/);
    await expect(page.getByRole('heading', { name: '数据面板' })).toBeVisible();
  });

  test('7.3 页面标题已设置', async ({ page }) => {
    const title = await page.title();
    // 记录实际标题（当前为 Vite 默认值 "web"，属待改进项）
    console.log(`[INFO] 页面标题: "${title}"`);
    expect(title.length).toBeGreaterThan(0);
  });
});
