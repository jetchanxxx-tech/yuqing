// Runner-only provider fixture. The production binary has no retrieval route.
import http from 'node:http';
import https from 'node:https';
import { readFileSync, realpathSync } from 'node:fs';
import { join } from 'node:path';
import { createHash, randomUUID, timingSafeEqual } from 'node:crypto';

const directory = process.env.YUQING_NOTIFICATION_SANDBOX_DIR || '';
if (process.env.GITHUB_ACTIONS !== 'true' || process.env.RUNNER_ENVIRONMENT !== 'github-hosted' || !directory.startsWith('/tmp/yuqing-notification-ci-') || realpathSync(directory) !== directory) throw new Error('Hosted disposable notification fixture required');
const token = JSON.parse(readFileSync(join(directory, 'control.json'), 'utf8')).token;
const messages = [];
let mode = 'accept';
const digest = value => createHash('sha256').update(value).digest('hex');
const send = (res, status, value) => { res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(value)); };
async function body(req) {
  let size = 0; const chunks = [];
  for await (const chunk of req) { size += chunk.length; if (size > 65536) throw new Error('Input limit'); chunks.push(chunk); }
  return Buffer.concat(chunks).toString('utf8');
}
const emailPurpose = subject => {
  if (subject.startsWith('账户邮箱已更换')) return 'email_changed_notice';
  if (subject.startsWith('确认更换邮箱')) return 'email_change';
  if (subject.startsWith('激活账户')) return 'set_password';
  if (subject.startsWith('重置密码')) return 'password_reset';
  if (subject.startsWith('验证邮箱')) return 'email_verify';
  throw new Error('Unknown subject');
};
async function provider(req, res) {
  try {
    let recipient, purpose, payload, vendor;
    if (req.headers.host === 'api.resend.com' && req.method === 'POST' && req.url === '/emails') {
      if (req.headers.authorization !== 'Bearer sandbox-resend-ci') return send(res, 403, { error: 'isolated credentials required' });
      const input = JSON.parse(await body(req));
      if (input.to?.length !== 1 || !input.to[0].endsWith('@example.invalid') || !String(input.from).includes('sender@example.invalid')) throw new Error('Reserved recipient required');
      recipient = input.to[0]; purpose = emailPurpose(input.subject); vendor = 'resend';
      const link = String(input.html).match(/http:\/\/127\.0\.0\.1:4173\/(?:verify-email|reset-password|email-change|activate)#token=[A-Za-z0-9_%.-]+/)?.[0];
      if (purpose !== 'email_changed_notice' && !link) throw new Error('Purpose link absent');
      payload = purpose === 'email_changed_notice' ? {} : { link, token: new URL(link).hash.slice(7) };
    } else if (req.headers.host === 'dysmsapi.aliyuncs.com') {
      const query = new URL(req.url, 'https://dysmsapi.aliyuncs.com').searchParams;
      const authorization = String(req.headers.authorization || '');
      const signed = query.get('AccessKeyId') === 'sandbox-aliyun-ci' && !!query.get('Signature') || authorization.includes('sandbox-aliyun-ci');
      if (!signed || (query.get('Action') || req.headers['x-acs-action']) !== 'SendSms' || query.get('SignName') !== 'CI Sandbox') return send(res, 403, { Code: 'Denied', Message: 'isolated signed request required' });
      recipient = query.get('PhoneNumbers'); vendor = 'aliyun';
      if (!/^13900006\d{3}$/.test(recipient)) throw new Error('Reserved phone required');
      purpose = ({ CI_BIND: 'phone_bind', CI_LOGIN: 'phone_login', CI_RESET: 'phone_reset' })[query.get('TemplateCode')];
      payload = JSON.parse(query.get('TemplateParam'));
      if (!purpose || !/^\d{6}$/.test(payload.code)) throw new Error('Purpose/code invalid');
    } else return send(res, 404, { error: 'isolated provider host required' });
    if (messages.length >= 256) return send(res, 429, { error: 'fixture budget exhausted' });
    const id = randomUUID(); const accepted = mode === 'accept';
    messages.push({ id, purpose, vendor, recipient, payload, accepted, delivered: false, provider_id: digest(id) });
    if (mode === 'timeout') { const timer = setTimeout(() => send(res, 504, {}), 12000); res.on('close', () => clearTimeout(timer)); return; }
    if (mode === 'reject') return send(res, 422, vendor === 'aliyun' ? { Code: 'Rejected', Message: 'sandbox rejection' } : { name: 'sandbox_rejected', message: 'sandbox rejection' });
    if (mode === 'malformed') return send(res, 200, vendor === 'aliyun' ? { Code: 'OK', RequestId: id } : {});
    return send(res, 200, vendor === 'aliyun' ? { Code: 'OK', BizId: id, RequestId: id, Message: 'OK' } : { id });
  } catch { send(res, 400, { error: 'invalid isolated provider request' }); }
}
async function controller(req, res) {
  try {
    if (req.method === 'GET' && req.url === '/health') return send(res, 200, { ready: true });
    const supplied = Buffer.from(String(req.headers['x-sandbox-token'] || ''));
    const expected = Buffer.from(token);
    if (supplied.length !== expected.length || !timingSafeEqual(supplied, expected)) return send(res, 403, { error: 'fixture control denied' });
    if (req.method === 'GET' && req.url === '/messages') return send(res, 200, messages.map(({ id, purpose, vendor, recipient, accepted, delivered, provider_id }) => ({ id, purpose, vendor, target_hash: digest(recipient), accepted, delivered, provider_id })));
    const input = JSON.parse(await body(req));
    if (req.method === 'POST' && req.url === '/mode' && ['accept', 'reject', 'malformed', 'timeout'].includes(input.mode)) { mode = input.mode; return send(res, 200, { mode }); }
    if (req.method === 'POST' && req.url === '/deliver') {
      const message = messages.find(m => m.id === input.id);
      if (!message?.accepted) return send(res, 409, { error: 'No accepted sandbox delivery' });
      message.delivered = true;
      return send(res, 200, { purpose: message.purpose, ...message.payload });
    }
    send(res, 404, { error: 'unknown isolated action' });
  } catch { send(res, 400, { error: 'invalid isolated action' }); }
}
const servers = [
  https.createServer({ cert: readFileSync(join(directory, 'certificate.pem')), key: readFileSync(join(directory, 'key.pem')) }, provider),
  http.createServer(provider), http.createServer(controller),
];
await Promise.all(servers.map((server, i) => new Promise((resolve, reject) => { server.once('error', reject); server.listen([9443, 9080, 9081][i], '127.0.0.1', resolve); })));
console.log('Offline SDK fixture ready: no real provider requests or recipient delivery.');
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => { for (const server of servers) server.close(); process.exit(0); });
