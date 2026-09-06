import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright config for the web E2E smoke suite.
 *
 * Setup (one time, in web/):
 *   npm i -D @playwright/test && npx playwright install chromium
 *
 * Run (from web/):
 *   npx playwright test --config e2e/playwright.config.ts
 *
 * The SPA smoke tests expect the Vite dev server (or `vite preview` after a
 * build) on :5173 — playwright starts it automatically via webServer unless
 * one is already listening.
 */
export default defineConfig({
  testDir: '.',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'npm run dev',
    url: 'http://localhost:5173',
    reuseExistingServer: true,
    timeout: 60_000,
  },
});
