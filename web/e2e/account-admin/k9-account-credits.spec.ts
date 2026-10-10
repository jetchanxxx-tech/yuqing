import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
const db = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const api = 'http://127.0.0.1:8080/api/v1';
const password = 'K9-original-password-2026';
function sql(statement: string, values: Record<string, string> = {}) {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== db) throw new Error('Hosted disposable PostgreSQL required');
  return execFileSync('psql', [db, '-v', 'ON_ERROR_STOP=1', '-A', '-t', ...Object.entries(values).flatMap(([k,v]) => ['-v',`${k}=${v}`])], { input: statement, encoding: 'utf8', stdio: ['pipe','pipe','pipe'] }).trim();
}
async function admin(request: APIRequestContext) {
  const email = `k9-admin-${randomBytes(7).toString('hex')}@example.invalid`;
  const r = await request.post(`${api}/auth/register`, { data: { email, password, name: 'K9 Browser Admin' } }); expect(r.status()).toBe(201);
  const a = await r.json();
  sql("INSERT INTO platform_user_roles(user_id,role) VALUES(:'uid','platform_admin');", { uid: a.user.user_id });
  return { ...a, email };
}
async function login(page: Page, email: string, pass = password) {
  await page.goto('/login'); await page.getByLabel('邮箱', { exact: true }).fill(email); await page.getByLabel('密码', { exact: true }).fill(pass);
  await page.getByRole('button', { name: /^登\s*录$/ }).click(); await expect(page).toHaveURL(/\/dashboard$/);
}
async function user(page: Page, email: string) {
  await page.goto('/admin'); await page.getByRole('tab', { name: '用户账号管理', exact: true }).click();
  await page.getByPlaceholder('搜索邮箱、昵称或用户 ID').fill(email); await page.getByRole('button', { name: '搜索', exact: true }).click();
  await page.getByRole('row').filter({hasText:email}).getByRole('button',{name:'查看详情',exact:true}).click();
}

test('K9 create keeps committed account discoverable after unavailable activation and detail retries explicitly', async ({page,request}) => {
  const a=await admin(request); const email=`k9-pending-${randomBytes(7).toString('hex')}@example.invalid`;
  const provider=sql("SELECT value FROM platform_settings WHERE key='email_provider';");sql("INSERT INTO platform_settings(key,value) VALUES('email_provider','') ON CONFLICT(key) DO UPDATE SET value='';");
  try {
    await login(page,a.email);await page.goto('/admin');await page.getByRole('tab',{name:'用户账号管理',exact:true}).click();
    await page.getByRole('button',{name:'新建账号',exact:true}).click();const dialog=page.getByRole('dialog',{name:'新建账号',exact:true});
    await dialog.getByLabel('邮箱',{exact:true}).fill(email);await dialog.getByLabel('昵称',{exact:true}).fill('K9 Pending Browser');await dialog.getByLabel('初始团队名称',{exact:true}).fill('K9 Trial Team');
    const creating=page.waitForResponse(r=>r.url().endsWith('/admin/users')&&r.request().method()==='POST');
    await dialog.getByRole('button',{name:'创建待激活账号',exact:true}).click();const response=await creating;expect(response.status()).toBe(201);const created=await response.json();
    expect(created.activation.state).toBe('failed');await expect(dialog.getByRole('alert')).toContainText('账号已创建');await expect(dialog.getByRole('alert')).toContainText('激活发送失败');
    await dialog.getByRole('button',{name:'查看账号',exact:true}).click();const detail=page.getByRole('dialog',{name:'用户详情',exact:true});await expect(detail).toContainText('待激活');
    const resend=page.waitForResponse(r=>r.url().endsWith('/activation-resend'));await detail.getByRole('button',{name:'重发激活邮件',exact:true}).click();expect((await resend).status()).toBe(503);
    await expect(detail.getByRole('alert').first()).toContainText('不可用');
    expect(sql("SELECT count(*) FROM users WHERE email=:'email';",{email})).toBe('1');expect(sql("SELECT count(*) FROM credit_transactions WHERE tenant_id=:'tid' AND reason='trial';",{tid:created.tenant_id})).toBe('1');
    await page.reload();await user(page,email);await expect(page.getByRole('dialog',{name:'用户详情',exact:true})).toContainText('发送失败');
  } finally {sql("UPDATE platform_settings SET value=:'provider' WHERE key='email_provider';",{provider});}
});

test('K9 activation fragment is scrubbed and one password consumption requires subsequent login', async ({page,request}) => {
  const a=await admin(request);const email=`k9-activate-${randomBytes(7).toString('hex')}@example.invalid`;const uid=randomBytes(12).toString('hex');const token=randomBytes(32).toString('base64url');
  sql("INSERT INTO users(id,email,name,password_hash,status) VALUES(:'uid',:'email','K9 activation','unusable','pending_activation'); INSERT INTO verification_tokens(id,user_id,issuer_user_id,purpose,type,target,token_hash,issued_version,issuer_version,expires_at,delivery_status) VALUES(:'vid',:'uid',:'actor','set_password','set_password',:'email',:'hash',0,0,now()+interval '10 minutes','accepted');",{uid,email,vid:randomBytes(12).toString('hex'),actor:a.user.user_id,hash:createHash('sha256').update(token).digest('hex')});
  await login(page,a.email);const urls:string[]=[];page.on('request',r=>urls.push(r.url()));await page.goto(`/activate#token=${token}`);await expect(page).toHaveURL(/\/activate$/);
  await page.getByLabel('新密码',{exact:true}).fill('ActivatedPassword123');await page.getByLabel('确认新密码',{exact:true}).fill('ActivatedPassword123');
  const confirmed=page.waitForResponse(r=>r.url().endsWith('/auth/activation/confirm'));await page.getByRole('button',{name:'激活并设置密码',exact:true}).click();expect((await confirmed).status()).toBe(200);
  await expect(page.getByText('账号已激活，请登录',{exact:true})).toBeVisible();expect(await page.evaluate(()=>localStorage.getItem('access_token'))).toBeNull();expect(urls.every(url=>!url.includes(token))).toBe(true);
  expect(sql("SELECT status||':'||token_version FROM users WHERE id=:'uid';",{uid})).toBe('active:1');
  expect((await request.post(`${api}/auth/activation/confirm`,{data:{token,new_password:'ReplayPassword123'}})).status()).toBeGreaterThanOrEqual(400);
  await login(page,email,'ActivatedPassword123');
});

test('K9 credit confirmation retains reason and idempotency on failure then reloads real ledger and balance', async ({page,request}) => {
  const a=await admin(request);await login(page,a.email);await page.goto('/admin');await page.getByRole('tab',{name:'租户与套餐管理',exact:true}).click();
  await page.getByPlaceholder('搜索租户名称或 ID').fill(a.user.tenant_id);await page.getByRole('button',{name:'搜索',exact:true}).click();await page.getByRole('row').filter({hasText:a.user.tenant_id}).getByRole('button',{name:'查看详情',exact:true}).click();
  await page.getByRole('button',{name:'调整报告额度',exact:true}).click();const dialog=page.getByRole('dialog',{name:'调整报告额度',exact:true});await dialog.getByLabel('调整数量',{exact:true}).fill('3');await dialog.getByLabel('操作原因',{exact:true}).fill('K9 service compensation');
  sql("CREATE FUNCTION k9_browser_reject() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='credit.adjustment' THEN RAISE EXCEPTION 'isolated audit rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER k9_browser_reject BEFORE INSERT ON audit_logs FOR EACH ROW EXECUTE FUNCTION k9_browser_reject();");
  let firstBody:Record<string,unknown>;
  try {const failing=page.waitForResponse(r=>r.url().endsWith('/credit-adjustments'));await dialog.getByRole('button',{name:'确认额度调整',exact:true}).click();const r=await failing;expect(r.status()).toBe(500);firstBody=r.request().postDataJSON();await expect(dialog.getByRole('alert')).toBeVisible();await expect(dialog.getByLabel('操作原因',{exact:true})).toHaveValue('K9 service compensation');expect(sql("SELECT balance FROM report_credits WHERE tenant_id=:'tid';",{tid:a.user.tenant_id})).toBe('1');}
  finally {sql('DROP TRIGGER k9_browser_reject ON audit_logs; DROP FUNCTION k9_browser_reject();');}
  const retry=dialog.getByRole('button',{name:'确认额度调整',exact:true});await expect(retry).toBeEnabled();await expect(retry).toHaveAttribute('aria-busy','false');
  const [r]=await Promise.all([page.waitForResponse(r=>r.url().endsWith('/credit-adjustments')),retry.click()]);expect(r.status()).toBe(200);expect(r.request().postDataJSON()).toEqual(firstBody!);
  await expect(dialog).toHaveCount(0);const detail=page.getByRole('dialog',{name:'租户详情',exact:true});await expect(detail.getByRole('row').filter({hasText:'K9 service compensation'}).first()).toBeVisible();expect(sql("SELECT balance FROM report_credits WHERE tenant_id=:'tid';",{tid:a.user.tenant_id})).toBe('4');
  const replay=await request.post(`${api}/admin/tenants/${a.user.tenant_id}/credit-adjustments`,{headers:{Authorization:`Bearer ${a.access_token}`},data:firstBody!});expect(replay.status()).toBe(200);expect(sql("SELECT count(*) FROM credit_transactions WHERE tenant_id=:'tid' AND reason='admin_adjust';",{tid:a.user.tenant_id})).toBe('1');
});
