import { defineConfig, devices } from '@playwright/test';

/** 侦察专用配置：只跑 recon.spec.ts，输出页面结构用于编写选择器。 */
export default defineConfig({
  testDir: '.',
  testMatch: '**/recon.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL || 'https://yuqing.pangu-cloud.com',
    ignoreHTTPSErrors: true,
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
