import { defineConfig, devices } from '@playwright/test';

/**
 * 生产环境 E2E 配置 —— 直接测试已部署站点（不起本地 server）。
 *
 * 运行（在 web/ 目录）:
 *   npx playwright test --config e2e/production.config.ts
 *   npx playwright test --config e2e/production.config.ts --headed   # 可视化
 *
 * 环境变量:
 *   E2E_BASE_URL   目标站点，默认 https://yuqing.pangu-cloud.com
 *   E2E_EMAIL      测试账号邮箱
 *   E2E_PASSWORD   测试账号密码
 */
export default defineConfig({
  testDir: '.',
  testMatch: '**/production.spec.ts',
  fullyParallel: false,
  workers: 1,               // 串行执行，避免相互干扰
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: [['list'], ['html', { outputFolder: 'e2e/report', open: 'never' }]],
  use: {
    baseURL: process.env.E2E_BASE_URL || 'https://yuqing.pangu-cloud.com',
    // Windows 客户端无法访问证书吊销服务器（CRYPT_E_REVOCATION_OFFLINE），
    // 对 Let's Encrypt 证书会握手失败 —— 这是客户端侧限制，非服务端问题。
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
