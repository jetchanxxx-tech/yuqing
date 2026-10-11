import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Space, Switch, Typography } from 'antd';
import { getNotificationPreferences, setNotificationPreferences } from '../api/user';
import { adminErrorMessage } from '../api/admin';
import { useAuth } from '../stores/auth';
import { ErrorBlock, LoadingBlock } from './PageState';

export default function NotificationPreferencesTab() {
  const { principal } = useAuth();
  const client = useQueryClient();
  const key = ['notification-preferences', principal?.user_id];
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => getNotificationPreferences(signal), enabled: Boolean(principal) });
  const [enabled, setEnabled] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [saved, setSaved] = useState(false);
  useEffect(() => { if (query.data && !dirty) setEnabled(query.data.preferences.task_completed); }, [query.data, dirty]);
  const save = async () => {
    setBusy(true); setError(null); setSaved(false);
    try { const result = await setNotificationPreferences({ task_completed: enabled }); client.setQueryData(key, result); setDirty(false); setSaved(true); }
    catch (failure) { setError(failure); }
    finally { setBusy(false); }
  };
  if (query.isLoading) return <LoadingBlock rows={4} />;
  if (query.isError || !query.data) return <ErrorBlock description="通知偏好加载失败" onRetry={() => void query.refetch()} />;
  return <Card title="通知偏好">
    <Alert type="info" showIcon message="任务完成邮件发送到当前已验证邮箱" description="账户安全提醒继续独立发送。真实通道送达仍待联调，已提交的发送可能继续完成。" style={{ marginBottom: 16 }} />
    {error !== null && <Alert type="error" showIcon message={adminErrorMessage(error)} style={{ marginBottom: 16 }} />}
    {saved && <Alert type="success" message="通知偏好已保存" style={{ marginBottom: 16 }} />}
    <Space direction="vertical" size="large" style={{ width: '100%' }}>
      <Space><Switch aria-label="任务完成邮件" checked={enabled} disabled={busy} onChange={value => { setEnabled(value); setDirty(true); setSaved(false); }} /><Typography.Text>任务完成邮件</Typography.Text></Space>
      {['告警通知', '账单提醒', '产品更新'].map(label => <Space key={label}><Switch aria-label={label} checked={false} disabled /><Typography.Text type="secondary">{label} · 待接线</Typography.Text></Space>)}
      <Button type="primary" aria-label="保存通知偏好" aria-busy={busy} loading={busy} disabled={busy || !dirty} onClick={() => void save()}>保存通知偏好</Button>
    </Space>
  </Card>;
}
