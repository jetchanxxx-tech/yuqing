import { defineConfig, devices } from '@playwright/test';
import { realpathSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

// This suite opens portable HTML files, with no backend or production traffic.
// Browser execution is restricted to the user's approved hosted workflow.
const workspace = fileURLToPath(new URL('../../', import.meta.url));
if (process.env.GITHUB_ACTIONS !== 'true' || process.env.RUNNER_ENVIRONMENT !== 'github-hosted' ||
    !process.env.GITHUB_WORKSPACE || realpathSync(process.env.GITHUB_WORKSPACE) !== realpathSync(workspace) ||
    !/^[a-f0-9]{40}$/.test(process.env.GITHUB_SHA ?? '')) {
  throw new Error('Marketing acceptance requires the exact source on a GitHub hosted runner.');
}

export default defineConfig({
  testDir: './marketing',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: true,
  timeout: 45_000,
  outputDir: join(workspace, 'test-results/marketing/browser'),
  reporter: [['list'], ['json', { outputFile: join(workspace, 'test-results/marketing/browser-results.json') }]],
  use: {
    actionTimeout: 5_000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    serviceWorkers: 'block',
    reducedMotion: 'reduce',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
