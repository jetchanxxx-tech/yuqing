import { useEffect, useState } from 'react';
import { Button, Result, Spin } from 'antd';
import { useSearchParams } from 'react-router-dom';
import { verifyEmailByToken } from '../api/user';

/** 邮箱验证落地页（公开路由 /verify-email?token=xxx，邮件链接点击进入） */
export default function VerifyEmailPage() {
  const [searchParams] = useSearchParams();
  const token = searchParams.get('token');
  const [state, setState] = useState<'verifying' | 'success' | 'error'>('verifying');
  const [error, setError] = useState('');

  useEffect(() => {
    if (!token) {
      setState('error');
      setError('验证链接无效（缺少 token）');
      return;
    }
    verifyEmailByToken(token)
      .then(() => setState('success'))
      .catch((e: { response?: { data?: { message?: string } } }) => {
        setState('error');
        setError(e.response?.data?.message ?? '验证失败，链接可能已过期');
      });
  }, [token]);

  if (state === 'verifying') {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', paddingTop: 120 }}>
        <Spin tip="正在验证邮箱...">
          <div style={{ minHeight: 120, minWidth: 240 }} />
        </Spin>
      </div>
    );
  }

  if (state === 'success') {
    return (
      <Result
        status="success"
        title="邮箱验证成功"
        subTitle="您现在可以使用全部功能了"
        extra={<Button type="primary" href="/dashboard">前往控制台</Button>}
      />
    );
  }

  return (
    <Result
      status="error"
      title="验证失败"
      subTitle={error}
      extra={<Button href="/settings">返回用户中心重新发送</Button>}
    />
  );
}
