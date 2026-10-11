import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';

const db = 'postgres://postgres:testpass@127.0.0.1:5432/yuqing_account_admin_test?sslmode=disable';
const api = 'http://127.0.0.1:8080/api/v1';
const password = 'ClosurePassword2026';
const workspace = fileURLToPath(new URL('../../../', import.meta.url));
function sql(statement: string, values: Record<string,string> = {}) {
  if (process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || process.env.YUQING_TEST_PG_URL !== db) throw new Error('Hosted disposable PostgreSQL required');
  return execFileSync('psql',[db,'-v','ON_ERROR_STOP=1','-A','-t',...Object.entries(values).flatMap(([key,value])=>['-v',`${key}=${value}`])],{input:statement,encoding:'utf8',stdio:['pipe','pipe','pipe']}).trim();
}
async function account(request: APIRequestContext) {
  const email=`closure-${randomBytes(8).toString('hex')}@example.invalid`;
  const response=await request.post(`${api}/auth/register`,{data:{email,password,name:'Closure Browser Owner'}});expect(response.status()).toBe(201);
  return {...await response.json(),email};
}
async function login(page:Page,email:string) {
  await page.goto('/login');await page.getByLabel('邮箱',{exact:true}).fill(email);await page.getByLabel('密码',{exact:true}).fill(password);
  await page.getByRole('button',{name:/^登\s*录$/}).click();
}
test('closure preview explicit sole-team confirmation and restricted withdrawal preserve other data',async({page,request})=>{
  const a=await account(request);await login(page,a.email);await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto('/settings');await page.getByRole('tab',{name:'账户安全',exact:true}).click();await page.getByRole('button',{name:'注销账号预检',exact:true}).click();
  const dialog=page.getByRole('dialog',{name:'注销账号',exact:true});await expect(dialog).toContainText('7 天');await dialog.getByLabel('当前密码',{exact:true}).fill(password);
  await dialog.getByLabel('我确认关闭这个仅我一人的团队',{exact:true}).check();await dialog.getByLabel('我已阅读影响并申请注销',{exact:true}).check();
  const sending=page.waitForResponse(response=>response.url().endsWith('/user/account-closure')&&response.request().method()==='POST');await dialog.getByRole('button',{name:'申请七天后注销',exact:true}).click();expect((await sending).status()).toBe(202);
  await expect(page).toHaveURL(/\/login$/);await login(page,a.email);await expect(page).toHaveURL(/\/account-closure$/);await expect(page.getByText('注销等待期',{exact:true})).toBeVisible();
  expect((await request.get(`${api}/auth/me`,{headers:{Authorization:`Bearer ${a.access_token}`}})).status()).toBe(401);
  await page.getByLabel('当前密码',{exact:true}).fill(password);await page.getByRole('button',{name:'撤回注销申请',exact:true}).click();await expect(page).toHaveURL(/\/login$/);
  await login(page,a.email);await expect(page).toHaveURL(/\/dashboard$/);expect(sql("SELECT status||':'||token_version FROM users WHERE id=:'uid';",{uid:a.user.user_id})).toBe('active:2');
  expect(sql("SELECT balance FROM report_credits WHERE tenant_id=:'tid';",{tid:a.user.tenant_id})).toBe('1');
});

test('prebuilt closure CLI rechecks deadline and blockers then anonymizes without deleting financial facts',async({request})=>{
  const a=await account(request);const uid=a.user.user_id,tid=a.user.tenant_id;
  const response=await request.post(`${api}/user/account-closure`,{headers:{Authorization:`Bearer ${a.access_token}`},data:{password,confirmed:true,close_tenant_ids:[tid]}});expect(response.status()).toBe(202);
  const cli=join(workspace,'platform/bin/yuqing-cli');
  const execute=()=>spawnSync(cli,['account-closure','execute','--user-id',uid],{cwd:workspace,env:{...process.env,YUQING_CONFIG:process.env.YUQING_E2E_CONFIG},encoding:'utf8'});
  const early=execute();expect(early.status,'prebuilt CLI must safely report waiting before the seven-day deadline').toBe(0);
  expect(sql("SELECT status FROM users WHERE id=:'uid';",{uid})).toBe('closure_pending');
  sql("UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second' WHERE user_id=:'uid' AND state='pending'; INSERT INTO orders(id,tenant_id,sku_code,kind,credits,amount_cents,channel,state,expires_at) VALUES(:'oid',:'tid','free','plan',1,1,'test','pending',now()+interval '1 hour');",{uid,tid,oid:`block-${uid}`});
  const blocked=execute();expect(blocked.status,'prebuilt CLI must preserve a blocked pending account').toBe(0);expect(sql("SELECT status FROM users WHERE id=:'uid';",{uid})).toBe('closure_pending');
  sql("UPDATE orders SET state='closed' WHERE id=:'oid';",{oid:`block-${uid}`});
  const complete=execute();expect(complete.status,'prebuilt CLI must finish the eligible confirmed closure').toBe(0);
  expect(sql("SELECT status||':'||name||':'||(phone IS NULL)::text FROM users WHERE id=:'uid';",{uid})).toBe('closed:已注销用户:true');
  expect(sql("SELECT count(*) FROM credit_transactions WHERE tenant_id=:'tid';",{tid})).toBe('1');expect(sql("SELECT count(*) FROM orders WHERE tenant_id=:'tid';",{tid})).toBe('1');
  expect(sql("SELECT count(*) FROM users WHERE email=:'email';",{email:a.email})).toBe('0');
  expect(execute().status).toBe(0);expect(sql("SELECT count(*) FROM account_closures WHERE user_id=:'uid' AND state='completed';",{uid})).toBe('1');
  const reused=await request.post(`${api}/auth/register`,{data:{email:a.email,password,name:'New immutable identity'}});expect(reused.status()).toBe(201);expect((await reused.json()).user.user_id).not.toBe(uid);
});
