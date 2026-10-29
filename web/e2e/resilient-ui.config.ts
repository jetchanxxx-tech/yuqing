import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './resilient-ui',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 45_000,
  expect: { timeout: 8_000 },
  outputDir: '../test-results/resilient-ui',
  reporter: [['list'], ['html', { outputFolder: 'e2e/report/resilient-ui', open: 'never' }]],
  use: { baseURL: 'http://127.0.0.1:4177', locale: 'zh-CN', timezoneId: 'Asia/Shanghai',
    screenshot: 'only-on-failure', trace: 'retain-on-failure' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: { command: 'npm run preview -- --host 127.0.0.1 --port 4177',
    url: 'http://127.0.0.1:4177', reuseExistingServer: false, timeout: 30_000 },
});
