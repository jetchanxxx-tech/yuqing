import { expect, test } from '@playwright/test';

/**
 * E2E smoke suite — 冒烟级，不依赖后端业务接口。
 *
 * 范围说明：当前后端 /api/v1 handlers 大部分为 NOT_IMPLEMENTED stub，
 * 因此这里只锁定「页面可渲染 / 路由守卫生效 / 演示包可打开」三条基线。
 * 待 handlers 落地后在此文件上扩展业务流用例（登录 → 建分析 → 看板）。
 *
 * 运行前提（web/ 目录）：
 *   npm i -D @playwright/test        # 尚未安装 — 见 platform/TEST_REPORT.md
 *   npx playwright install chromium
 *   npx playwright test --config e2e/playwright.config.ts
 */

test.describe('demo smoke', () => {
  test('login page renders', async ({ page }) => {
    await page.goto('/login');

    // 品牌标题（antd Typography.Title level=3 → h3）
    await expect(page.getByRole('heading', { name: '微舆舆情' })).toBeVisible();

    // 登录表单关键控件
    await expect(page.getByPlaceholder('you@example.com')).toBeVisible();
    await expect(page.getByPlaceholder('请输入密码')).toBeVisible();
    await expect(page.getByRole('link', { name: '免费注册' })).toBeVisible();

    // 页面标题断言防呆：不应落在 /dashboard
    await expect(page).toHaveURL(/\/login$/);
  });

  test('unauthenticated /dashboard redirects to /login', async ({ page }) => {
    // 新 context 无 localStorage principal → RequireAuth 必须重定向
    await page.goto('/dashboard');
    await expect(page).toHaveURL(/\/login$/, { timeout: 10_000 });
    await expect(page.getByRole('heading', { name: '微舆舆情' })).toBeVisible();
  });

  test('login form rejects nothing client-side and submits to the API stub', async ({ page }) => {
    // 契约探针：axios 401/501 都不应造成白屏，表单仍在
    await page.goto('/login');
    await page.getByPlaceholder('you@example.com').fill('smoke@example.com');
    await page.getByPlaceholder('请输入密码').fill('password-123456');
    await page.getByRole('button', { name: /登\s*录/ }).click();
    // 后端 stub 返回 501 → 页面停留在 /login 并展示错误消息（不崩溃）
    await expect(page).toHaveURL(/\/login$/, { timeout: 10_000 });
  });

  test('standalone demo opens and start button exists', async ({ page }) => {
    // demo/ 是独立离线演示包（无需服务器），直接 file:// 打开。
    // 注：本分支演示文件为 index1.html（index.html 已被替换），
    // 打包发布时若重命名为 index.html 请同步修改此处 URL。
    const demoUrl = new URL('../../demo/index1.html', import.meta.url).href;
    const response = await page.goto(demoUrl);
    expect(response?.status()).toBeLessThan(400);

    await expect(page).toHaveTitle(/雅阁后排/);
    await expect(page.getByRole('heading', { name: /微舆舆情/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /开始演示/ })).toBeVisible();
  });
});
