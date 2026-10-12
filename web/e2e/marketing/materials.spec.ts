import { expect, test, type Page } from '@playwright/test';
import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const workspace = process.env.GITHUB_WORKSPACE!;
const evidence = join(workspace, 'test-results/marketing/screenshots');
mkdirSync(evidence, { recursive: true });
const localPage = (name: string) => pathToFileURL(join(workspace, name)).href;
const errors = new WeakMap<Page, string[]>();
const traffic = new WeakMap<Page, string[]>();

test.beforeEach(async ({ page, context }) => {
  errors.set(page, []);
  traffic.set(page, []);
  page.on('pageerror', error => errors.get(page)!.push(error.message));
  // Exercise the actual portable experience. Any automatic HTTP request is a
  // failure, including remote fonts, analytics or production API calls.
  await context.route(/^https?:\/\//, route => {
    traffic.get(page)!.push(route.request().url());
    return route.abort();
  });
});
test.afterEach(async ({ page }) => {
  expect(errors.get(page)).toEqual([]);
  expect(traffic.get(page)).toEqual([]);
});

async function noHorizontalOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
}

test('website is portable and readable across desktop, tablet and phone', async ({ page }) => {
  for (const [width, height] of [[1440, 960], [768, 1024], [375, 812]]) {
    await page.setViewportSize({ width, height });
    await page.goto(localPage('landing/index.html'));
    await expect(page.locator('h1')).toContainText('公开');
    await noHorizontalOverflow(page);
    await expect(page.locator('a[href="../demo/demo.html"]').first()).toBeVisible();
    await expect(page.locator('a[href="https://yuqing2.pangu-cloud.com/register"]').first()).toBeVisible();
    await page.screenshot({ path: join(evidence, `website-${width}.png`), fullPage: true });
  }
});

test('compatibility aliases preserve the portable website and demo', async () => {
  expect(readFileSync(join(workspace, 'landing/index.html'), 'utf8')).toEqual(readFileSync(join(workspace, 'landing/index-redesign.html'), 'utf8'));
  expect(readFileSync(join(workspace, 'demo/demo.html'), 'utf8')).toEqual(readFileSync(join(workspace, 'demo/index.html'), 'utf8'));
});

test('website mobile navigation, evidence tabs and FAQ work with keyboard controls', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto(localPage('landing/index.html'));
  await page.locator('#menu-toggle').click();
  await expect(page.locator('#menu-toggle')).toHaveAttribute('aria-expanded', 'true');
  await page.keyboard.press('Escape');
  await expect(page.locator('#menu-toggle')).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('#menu-toggle')).toBeFocused();
  await page.locator('#menu-toggle').click();
  await page.locator('#site-navigation a[href="#pricing"]').click();
  await expect(page.locator('#menu-toggle')).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('.price-card[data-plan="lite"]')).toContainText('4 次报告额度');
  await page.locator('#tab-evidence').focus();
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('#tab-insight')).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator('#panel-insight')).toBeVisible();
  await expect(page.locator('#panel-evidence')).toBeHidden();
  await page.keyboard.press('End');
  await expect(page.locator('#panel-report')).toBeVisible();
  await page.locator('#faq-monitoring summary').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#faq-monitoring')).toHaveAttribute('open', '');
  await expect(page.locator('#faq-monitoring p')).toBeVisible();
});

test('demo preserves samples, source inspection, five dimensions and local quota state', async ({ page }) => {
  await page.goto(localPage('demo/demo.html'));
  await expect(page.getByTestId('sample-notice')).toContainText('虚构');
  await page.getByTestId('save-draft').click();
  await expect(page.getByTestId('draft-status')).toContainText('不');
  await expect(page.getByTestId('quota-top')).toContainText('8');
  await expect(page.getByTestId('setup-panel')).toBeVisible();
  await page.getByTestId('start-analysis').click();
  await expect(page.getByTestId('quota-top')).toContainText('7');
  await page.getByTestId('pause-demo').click();
  await expect(page.getByTestId('progress-panel')).toHaveAttribute('data-state', 'paused');
  await page.getByTestId('skip-progress').click();
  await expect(page.getByTestId('materials-panel')).toBeVisible();
  await expect(page.getByTestId('count-candidate')).toHaveText('20');
  await expect(page.getByTestId('count-summary')).toHaveText('15');
  await expect(page.getByTestId('count-effective')).toHaveText('10');
  await page.getByTestId('evidence-source').selectOption('weibo');
  await expect(page.getByTestId('evidence-item')).toHaveCount(2);
  await page.getByTestId('inspect-source').first().click();
  await expect(page.getByTestId('source-dialog')).toBeVisible();
  await expect(page.locator('#source-url')).toContainText('https://example.com/');
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('source-dialog')).toBeHidden();
  await page.getByTestId('materials-next').click();
  await expect(page.getByTestId('analysis-dimension')).toHaveCount(5);
  await page.screenshot({ path: join(evidence, 'demo-analysis-desktop.png'), fullPage: true });
  await page.getByTestId('nav-account').click();
  await expect(page.getByTestId('quota-account')).toHaveText('7');
  await expect(page.getByTestId('credit-ledger')).toContainText('预留');
});

test('demo quick mode and one source affect the actual materials and dimensions', async ({ page }) => {
  await page.goto(localPage('demo/demo.html'));
  await page.getByTestId('mode-quick').check();
  for (const source of ['douyin', 'toutiao', 'xigua', 'wechat']) {
    await page.locator(`input[name="source"][value="${source}"]`).uncheck();
  }
  await page.getByTestId('start-analysis').click();
  await page.getByTestId('skip-progress').click();
  await expect(page.getByTestId('count-effective')).toHaveText('2');
  await expect(page.getByTestId('count-candidate')).toHaveText('4');
  await page.getByTestId('materials-next').click();
  await expect(page.getByTestId('analysis-dimension')).toHaveCount(3);
  expect(await page.getByTestId('analysis-dimension').evaluateAll(nodes => nodes.map(node => node.getAttribute('data-dimension')))).toEqual(['heat', 'sentiment', 'deep_cause']);
});

test('demo cancellation and insufficient samples refund once and prevent reports', async ({ page }) => {
  await page.goto(localPage('demo/demo.html'));
  await page.getByTestId('start-analysis').click();
  await page.getByTestId('pause-demo').click();
  await page.getByTestId('cancel-demo').click();
  await expect(page.getByTestId('progress-panel')).toHaveAttribute('data-state', 'canceled');
  await expect(page.getByTestId('quota-top')).toContainText('8');
  await expect(page.getByTestId('nav-report')).toBeDisabled();
  await page.getByTestId('reset-demo').click();
  await page.getByTestId('keywords').fill('完全不存在的虚构检索词');
  await page.getByTestId('start-analysis').click();
  await page.getByTestId('skip-progress').click();
  await expect(page.getByTestId('progress-panel')).toHaveAttribute('data-state', 'failed');
  await expect(page.getByTestId('quota-top')).toContainText('8');
  await expect(page.getByTestId('nav-analysis')).toBeDisabled();
  await expect(page.getByTestId('nav-report')).toBeDisabled();
  await page.getByTestId('nav-materials').click();
  await expect(page.getByTestId('count-effective')).toHaveText('0');
  await expect(page.getByTestId('evidence-item')).toHaveCount(0);
  await page.getByTestId('reset-demo').click();
  await expect(page.getByTestId('setup-panel')).toBeVisible();
  await expect(page.getByTestId('keywords')).toHaveValue('青禾茶饮, 青禾轻乳茶, 新品糖度');
});

test('narrowed sample conclusions do not mention filtered-out topics', async ({ page }) => {
  await page.goto(localPage('demo/demo.html'));
  await page.getByTestId('keywords').fill('青禾轻乳茶');
  await page.getByTestId('start-analysis').click();
  await page.getByTestId('skip-progress').click();
  await expect(page.getByTestId('count-effective')).toHaveText('5');
  await page.getByTestId('materials-next').click();
  await expect(page.getByTestId('result-summary')).not.toContainText('门店等候');
  await expect(page.locator('[data-dimension="background"]')).not.toContainText('门店等候');
});

test('HTML sample download safely preserves user input and remains offline', async ({ page }) => {
  const payload = '<img src=x onerror="window.__unexpected=1">';
  await page.goto(localPage('demo/demo.html'));
  await page.getByTestId('analysis-title').fill(payload);
  await page.getByTestId('keywords').fill(`青禾茶饮, ${payload}`);
  await page.getByTestId('start-analysis').click();
  await page.getByTestId('skip-progress').click();
  await page.getByTestId('nav-report').click();
  await expect(page.getByTestId('report-preview-title')).toHaveText(payload);
  const event = page.waitForEvent('download');
  await page.getByTestId('download-report').click();
  const download = await event;
  expect(download.suggestedFilename()).toBe('pangu-yuqing-fictional-sample-report.html');
  const report = join(workspace, 'test-results/marketing/downloaded-sample-report.html');
  await download.saveAs(report);
  const text = readFileSync(report, 'utf8');
  expect(text).toContain('&lt;img');
  expect(text).not.toContain(payload);
  expect(text).not.toMatch(/<script\b|<img\b/i);
  await page.goto(pathToFileURL(report).href);
  await expect(page.locator('body')).toContainText(payload);
  await expect(page.locator('body')).toContainText('虚构');
  expect(await page.evaluate(() => (window as typeof window & { __unexpected?: number }).__unexpected)).toBeUndefined();
});

test('demo remains usable on mobile through all six stages and local settings', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await page.goto(localPage('demo/demo.html'));
  await noHorizontalOverflow(page);
  await page.screenshot({ path: join(evidence, 'demo-setup-mobile.png'), fullPage: true });
  await page.getByTestId('start-analysis').click();
  await noHorizontalOverflow(page);
  await page.getByTestId('skip-progress').click();
  for (const step of ['materials', 'analysis', 'report', 'account']) {
    await page.getByTestId(`nav-${step}`).click();
    await noHorizontalOverflow(page);
    await expect(page.getByTestId('sample-notice')).toBeVisible();
  }
  await page.locator('#tab-security').click();
  await page.locator('#profile-name').fill('青禾试用同事');
  await page.locator('#profile-timezone').selectOption('UTC');
  await page.locator('#notify-preference').check();
  await page.getByTestId('save-profile').click();
  await expect(page.locator('#profile-display')).toHaveText('青禾试用同事');
  await expect(page.getByTestId('profile-status')).toContainText('保存');
  await page.screenshot({ path: join(evidence, 'demo-account-mobile.png'), fullPage: true });
});

for (const [audience, count] of [['customer', 6], ['partner', 10], ['investor', 10]] as const) {
  test(`${audience} slide preview has ${count} pages, keyboard navigation and viewport fit`, async ({ page }) => {
    for (const [width, height] of [[1920, 1080], [1280, 720], [768, 1024], [375, 667], [667, 375]]) {
      await page.setViewportSize({ width, height });
      await page.goto(localPage(`docs/marketing/pangu-${audience}-0.2.5.html`));
      await expect(page.locator('.slide')).toHaveCount(count);
      for (let index = 0; index < count; index++) {
        await noHorizontalOverflow(page);
        // Check content geometry on each real slide, including short screens.
        // Hidden content must not count as passing viewport-fit evidence.
        const overflow = await page.locator('.slide').nth(index).evaluate(slide => {
          const rect = slide.getBoundingClientRect();
          if (!rect.width || !rect.height || getComputedStyle(slide).visibility === 'hidden') return ['slide-hidden'];
          return Array.from(slide.querySelectorAll('h1,h2,h3,p,li,.card,.price,.note'))
            .filter(element => element.getClientRects().length > 0)
            .filter(element => {
              const bounds = element.getBoundingClientRect();
              return bounds.left < -1 || bounds.top < -1 || bounds.right > innerWidth + 1 || bounds.bottom > innerHeight + 1;
            }).map(element => element.textContent?.slice(0, 80));
        });
        expect(overflow, `${audience} page ${index + 1} at ${width}x${height}`).toEqual([]);
        if (index < count - 1) await page.keyboard.press('ArrowRight');
      }
      await page.keyboard.press('Home');
      await expect(page.locator('[data-page-count]')).toContainText(`1`);
      await page.locator('button[data-next]').click();
      await expect(page.locator('[data-page-count]')).toContainText(`2`);
      await page.locator('button[data-prev]').click();
      await page.screenshot({ path: join(evidence, `${audience}-preview-${width}x${height}.png`) });
    }
  });
}
