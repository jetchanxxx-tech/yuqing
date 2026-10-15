import { expect, test } from '@playwright/test';
import { randomBytes } from 'node:crypto';

for (const [label, scene, perspective] of [
  ['新品上市', 'product_launch', 'event'],
  ['突发危机', 'crisis', 'event'],
  ['产品质量投诉', 'quality_complaint', 'brand'],
] as const) {
  test(`${label} submits a distinct scene and persists it on the actual API`, async ({ page, request }) => {
    const email = `scene-${randomBytes(7).toString('hex')}@example.invalid`;
    const password = 'Scene-hosted-sandbox-only-2026!';
    const registered = await request.post('http://127.0.0.1:8080/api/v1/auth/register', {
      data: { email, password, name: '场景验收' },
    });
    expect(registered.status()).toBe(201);
    await page.goto('/login');
    await page.getByLabel('邮箱', { exact: true }).fill(email);
    await page.getByLabel('密码', { exact: true }).fill(password);
    await page.getByRole('button', { name: /^登\s*录$/ }).click();
    await expect(page).toHaveURL(/\/dashboard$/);
    await page.goto('/analyses/new');
    await page.getByPlaceholder('例如：新品发布会舆情监测').fill(`${label}场景验收`);
    await page.getByRole('button', { name: '按模板', exact: true }).click();
    await page.getByText(label, { exact: true }).click();
    await page.getByRole('button', { name: '下一步', exact: true }).click();
    await page.getByRole('combobox').fill('场景品牌');
    await page.getByRole('combobox').press('Enter');
    await page.getByRole('button', { name: '下一步', exact: true }).click();
    await page.getByRole('checkbox', { name: '微博', exact: true }).check();
    await page.getByRole('button', { name: '下一步', exact: true }).click();
    await expect(page.getByText(`${label}（随本次任务保存并用于专项研判）`, { exact: true })).toBeVisible();
    const submitted = page.waitForResponse(response => response.url().endsWith('/api/v1/analyses') && response.request().method() === 'POST');
    await page.getByRole('button', { name: '提交分析', exact: true }).click();
    const response = await submitted;
    expect(response.status()).toBe(201);
    expect(response.request().postDataJSON()).toMatchObject({ scenario_template_id: scene, analysis_type: perspective });
    const created = await response.json();
    expect(created).toMatchObject({ scenario_template_id: scene, analysis_type: perspective });
    expect(created.id).toMatch(/^[0-9A-HJKMNP-TV-Z]{26}$/);
    await expect(page).toHaveURL(new RegExp(`/analyses/${created.id}$`));
    const reloadedDetail = page.waitForResponse(value => value.url().endsWith(`/api/v1/analyses/${created.id}`)
      && value.request().method() === 'GET');
    await page.reload();
    const detailResponse = await reloadedDetail;
    expect(detailResponse.status()).toBe(200);
    expect(await detailResponse.json()).toMatchObject({ id: created.id, name: `${label}场景验收`, scenario_template_id: scene, analysis_type: perspective });
    await expect(page.getByRole('heading', { name: `${label}场景验收`, exact: true })).toBeVisible();
    const id = new URL(page.url()).pathname.split('/').at(-1);
    const persisted = await page.evaluate(async (analysisId) => {
      const token = localStorage.getItem('access_token');
      const result = await fetch(`/api/v1/analyses/${analysisId}`, { headers: { Authorization: `Bearer ${token}` } });
      return result.json();
    }, id);
    expect(persisted).toMatchObject({ scenario_template_id: scene, analysis_type: perspective });
    await page.screenshot({ path: `test-results/account-admin/analysis-scene-${scene}.png` });
  });
}
