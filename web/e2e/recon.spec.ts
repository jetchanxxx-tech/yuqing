import { test } from '@playwright/test';

/**
 * 侦察脚本（非测试）：输出生产站点的真实 DOM 结构，
 * 用于编写可靠的选择器。用 --grep 单独运行。
 */
test('recon: 登录页结构', async ({ page }) => {
  await page.goto('/login');
  await page.waitForLoadState('networkidle');
  console.log('=== URL ===', page.url());
  console.log('=== TITLE ===', await page.title());
  console.log('=== INPUTS ===');
  for (const el of await page.locator('input').all()) {
    console.log('  input:', await el.getAttribute('type'), '| id:', await el.getAttribute('id'),
                '| placeholder:', await el.getAttribute('placeholder'));
  }
  console.log('=== BUTTONS ===');
  for (const el of await page.locator('button').all()) {
    console.log('  button:', (await el.textContent())?.trim());
  }
  console.log('=== HEADINGS ===');
  for (const el of await page.locator('h1,h2,h3').all()) {
    console.log('  heading:', (await el.textContent())?.trim());
  }
  await page.screenshot({ path: 'e2e/shots/recon-login.png', fullPage: true });
});

test('recon: 登录后主界面结构', async ({ page }) => {
  // 凭据只从环境变量读取，不硬编码（仓库公开）
  const email = process.env.E2E_EMAIL ?? '';
  const password = process.env.E2E_PASSWORD ?? '';
  if (!email || !password) throw new Error('缺少 E2E_EMAIL / E2E_PASSWORD 环境变量');

  await page.goto('/login');
  await page.fill('input[type="text"], input#email, input[type="email"]', email);
  await page.fill('input[type="password"]', password);
  await page.click('button[type="submit"]');
  await page.waitForTimeout(3000);
  console.log('=== 登录后 URL ===', page.url());
  console.log('=== 菜单项 ===');
  for (const el of await page.locator('.ant-menu-item, nav a, aside a').all()) {
    console.log('  menu:', (await el.textContent())?.trim());
  }
  console.log('=== 页面标题 ===');
  for (const el of await page.locator('h1,h2,h3,h4').all()) {
    console.log('  heading:', (await el.textContent())?.trim());
  }
  await page.screenshot({ path: 'e2e/shots/recon-dashboard.png', fullPage: true });
});
