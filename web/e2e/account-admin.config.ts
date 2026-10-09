import { defineConfig, devices } from '@playwright/test';
import { chmodSync, existsSync, mkdtempSync, realpathSync, statSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

/**
 * Account/admin acceptance configuration for the GitHub hosted Linux runner.
 *
 * The workflow must download the current commit's platform-binaries and
 * frontend-dist artifacts, install Playwright on the runner, and create a new
 * PostgreSQL database named yuqing_account_admin_test. No production secrets or
 * user data belong in that service. YUQING_TEST_PG_URL must be the exact local
 * service URL below; this config generates its own backend configuration.
 *
 * Run only on that runner, from web/:
 *   npx playwright test --config e2e/account-admin.config.ts
 *
 * Tests belong in e2e/account-admin/*.spec.ts as their actual features land.
 * An empty suite is not acceptance evidence; do not use --pass-with-no-tests.
 * This config does not select production.spec.ts or start a Vite dev server.
 */

const workspace = fileURLToPath(new URL('../../', import.meta.url));
const frontendURL = 'http://127.0.0.1:4173';
const apiURL = 'http://127.0.0.1:8080';
const databaseURL = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';

if (
  process.env.GITHUB_ACTIONS !== 'true' ||
  process.env.RUNNER_ENVIRONMENT !== 'github-hosted' ||
  process.env.RUNNER_OS !== 'Linux' ||
  !process.env.GITHUB_WORKSPACE ||
  realpathSync(process.env.GITHUB_WORKSPACE) !== realpathSync(workspace) ||
  !/^[a-f0-9]{40}$/.test(process.env.GITHUB_SHA ?? '')
) {
  throw new Error('Account/admin E2E requires the GitHub hosted Linux runner and its checked-out source commit.');
}
if (process.env.YUQING_TEST_PG_URL !== databaseURL) {
  throw new Error('Account/admin E2E requires its disposable PostgreSQL service: yuqing_account_admin_test on 127.0.0.1:5432.');
}
for (const [name, expected] of [
  ['E2E_BASE_URL', frontendURL],
  ['E2E_API_URL', apiURL],
  ['YUQING_PUBLIC_BASE_URL', frontendURL],
] as const) {
  if (process.env[name] && process.env[name] !== expected) {
    throw new Error(`${name} must point to the fixed runner loopback endpoint.`);
  }
}
if (process.env.E2E_EMAIL && !process.env.E2E_EMAIL.endsWith('@example.invalid')) {
  throw new Error('Account/admin E2E fixture accounts must use example.invalid addresses.');
}

const frontendDist = join(workspace, 'web/dist');
const serverBinary = join(workspace, 'platform/bin/yuqing-server');
const cliBinary = join(workspace, 'platform/bin/yuqing-cli');
for (const artifact of [join(frontendDist, 'index.html'), serverBinary, cliBinary]) {
  if (!existsSync(artifact) || !statSync(artifact).isFile()) {
    throw new Error(`Missing CI prebuilt artifact: ${artifact}`);
  }
}
for (const binary of [serverBinary, cliBinary]) {
  if ((statSync(binary).mode & 0o111) === 0) {
    throw new Error(`CI must mark its downloaded prebuilt binary executable: ${binary}`);
  }
}

const runnerTemp = process.env.RUNNER_TEMP;
if (!runnerTemp || !statSync(runnerTemp).isDirectory()) {
  throw new Error('Account/admin E2E requires the runner temporary directory.');
}
const configDirectory = mkdtempSync(join(resolve(runnerTemp), 'yuqing-account-admin-'));
// The API and migration tool run under nobody after their outbound traffic is
// blocked. Only this generated test configuration needs to be readable by them.
chmodSync(configDirectory, 0o755);
const backendConfig = join(configDirectory, 'backend.json');
writeFileSync(backendConfig, JSON.stringify({
  server: { addr: '127.0.0.1:8080', env: 'test' },
  store: { driver: 'postgres' },
  db: { primary: databaseURL, maxConns: 5 },
  queue: { driver: 'postgres' },
  storage: { driver: 'local' },
  auth: {
    jwtSecret: 'account-admin-ci-only-no-production-identity',
    accessTTL: '15m',
    refreshTTL: '1h',
  },
  // Engines and RSSHub are deliberately unconfigured; no external collectors
  // or suppliers are required to verify account and tenant authorization.
  engines: {},
  rsshub_base: '',
}), { mode: 0o644 });

// Test fixtures may invoke only the checked-out commit's prebuilt CLI to grant
// platform roles to a freshly registered, explicitly identified test account.
process.env.YUQING_E2E_CONFIG = backendConfig;

const previewConfig = join(configDirectory, 'preview.mjs');
writeFileSync(previewConfig, `export default ${JSON.stringify({
  preview: {
    proxy: { '/api': { target: apiURL, changeOrigin: true } },
  },
})};\n`, { mode: 0o644 });

// Playwright launches commands through a shell. Quote paths before composing
// commands so a workspace path cannot be interpreted as shell code.
const shellQuote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
const backendEnvironment = [
  'PATH=/usr/bin:/bin',
  `YUQING_CONFIG=${shellQuote(backendConfig)}`,
  'YUQING_BETA_SKIP_CREDITS=true',
  `YUQING_PUBLIC_BASE_URL=${shellQuote(frontendURL)}`,
].join(' ');
const backendCommand = `
set -euo pipefail
backend_uid="$(id -u nobody)"
# Dynamic settings can choose real providers, so missing secrets alone is not
# an outbound safeguard. Deny this API user's non-loopback IPv4 and IPv6 traffic
# before running either prebuilt executable. Firewall failures stop the suite.
sudo -n iptables -I OUTPUT 1 -m owner --uid-owner "$backend_uid" ! -d 127.0.0.0/8 -j REJECT
sudo -n ip6tables -I OUTPUT 1 -m owner --uid-owner "$backend_uid" ! -d ::1/128 -j REJECT
sudo -n -u nobody env -i ${backendEnvironment} ${shellQuote(cliBinary)} migrate platform
exec sudo -n -u nobody env -i ${backendEnvironment} ${shellQuote(serverBinary)}
`;

export default defineConfig({
  testDir: '.',
  testMatch: '**/account-admin/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: true,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  outputDir: 'test-results/account-admin',
  reporter: [['list'], ['html', { outputFolder: 'e2e/report/account-admin', open: 'never' }]],
  use: {
    baseURL: frontendURL,
    serviceWorkers: 'block',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: `bash -c ${shellQuote(backendCommand)}`,
      cwd: join(workspace, 'platform'),
      url: `${apiURL}/api/v1/health`,
      reuseExistingServer: false,
      timeout: 60_000,
    },
    {
      command: `node node_modules/vite/bin/vite.js preview --config ${shellQuote(previewConfig)} --outDir ${shellQuote(frontendDist)} --host 127.0.0.1 --port 4173 --strictPort`,
      cwd: join(workspace, 'web'),
      url: frontendURL,
      reuseExistingServer: false,
      timeout: 60_000,
    },
  ],
});
