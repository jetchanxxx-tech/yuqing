import { useState } from 'react';
import { Alert, Button, Checkbox, Form, Input, Modal, Space, Typography } from 'antd';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../stores/auth';
import { previewClosure, requestClosure, type ClosurePreview } from '../api/accountClosure';
import { identityError } from '../lib/identity';

const blockerText: Record<string, string> = {
  LAST_PLATFORM_ADMIN: '请先保留另一位有效的平台管理员。',
  TEAM_ADMIN_HANDOFF: '请先将团队管理权交给现有成员。',
  UNSETTLED_ORDERS: '请先处理未结订单、退款或争议。',
  UNSETTLED_INVOICES: '请先结清账单或处理账单争议。',
  UNSETTLED_TASKS: '请先等待任务及用量结算完成。',
};

export function AccountClosureRequest() {
  const [preview, setPreview] = useState<ClosurePreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [password, setPassword] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [teams, setTeams] = useState<string[]>([]);
  const { logout } = useAuth();
  const navigate = useNavigate();
  const load = async () => {
    setBusy(true); setError('');
    try { setPreview(await previewClosure()); setTeams([]); setConfirmed(false); setPassword(''); }
    catch (e) { setError(identityError(e)); }
    finally { setBusy(false); }
  };
  const sole = preview?.tenants.filter(t => t.members === 1) ?? [];
  const send = async () => {
    if (busy || !preview || !confirmed || !password || sole.some(t => !teams.includes(t.id))) return;
    setBusy(true); setError('');
    try { await requestClosure(password, teams); logout(); navigate('/login', { replace: true }); }
    catch (e) { setError(identityError(e)); }
    finally { setBusy(false); }
  };
  return <Space direction="vertical" style={{ width: '100%' }}>
    <Typography.Text strong>注销账号</Typography.Text>
    <Typography.Text>申请后有 7 天撤回期。等待期仅可查看注销状态和撤回申请。</Typography.Text>
    {error && !preview && <Alert type="error" showIcon message={error} />}
    <Button danger aria-label="注销账号预检" aria-busy={busy} loading={busy} disabled={busy} onClick={() => void load()}>注销账号预检</Button>
    <Modal title="注销账号" open={!!preview} footer={null} onCancel={() => { if (!busy) { setPreview(null); setError(''); } }} closable={!busy} maskClosable={!busy}>
      <Space direction="vertical" style={{ width: '100%' }}>
        <Alert type="warning" showIcon message="7 天内可用原密码重新登录并撤回。到期执行后不可恢复。" description="财务流水保留并脱敏；团队报告和文件按当前套餐保留期处理。其他成员的团队和资产会保留。" />
        {preview?.tenants.map(t => <div key={t.id}>
          <Typography.Text strong>{t.name}</Typography.Text>
          <div>{t.members} 位成员 · {t.plan_code} · {t.retention_days == null ? '保留政策待核对' : `文件保留 ${t.retention_days} 天`}</div>
          {t.members === 1 && <Checkbox checked={teams.includes(t.id)} disabled={busy} onChange={e => setTeams(e.target.checked ? [...teams, t.id] : teams.filter(id => id !== t.id))}>我确认关闭这个仅我一人的团队</Checkbox>}
        </div>)}
        {preview?.blockers.map(b => <Alert key={b} type="error" message={blockerText[b.split(':')[0]] || '当前账户存在尚未处理的注销限制。'} />)}
        {error && <Alert type="error" showIcon message={error} />}
        <Form layout="vertical" onFinish={() => void send()}>
          <Form.Item label="当前密码" htmlFor="closure-password" required><Input.Password id="closure-password" value={password} disabled={busy} onChange={e => setPassword(e.target.value)} autoComplete="current-password" /></Form.Item>
          <Checkbox checked={confirmed} disabled={busy} onChange={e => setConfirmed(e.target.checked)}>我已阅读影响并申请注销</Checkbox>
          <Space style={{ marginTop: 16 }}>
            <Button danger type="primary" htmlType="submit" aria-label="申请七天后注销" aria-busy={busy} loading={busy} disabled={busy || !password || !confirmed || !!preview?.blockers.length || sole.some(t => !teams.includes(t.id))}>申请七天后注销</Button>
            <Button disabled={busy} onClick={() => void load()}>重新预检</Button>
          </Space>
        </Form>
      </Space>
    </Modal>
  </Space>;
}
