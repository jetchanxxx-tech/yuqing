import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Card,
  Descriptions,
  Divider,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import type { FormInstance } from 'antd';
import { LogoutOutlined, SafetyCertificateOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../stores/auth';
import { getPlans } from '../api/billing';
import {
  bindPhone,
  changePassword,
  getProfile,
  sendPhoneCode,
  sendVerificationEmail,
  unbindPhone,
  updateProfile,
  type UserProfile,
} from '../api/user';

const ROLE_LABELS: Record<string, string> = {
  tenant_admin: '租户管理员',
  platform_admin: '平台管理员',
  admin: '平台管理员',
  analyst: '分析师',
  viewer: '只读成员',
};

const TIMEZONES = ['Asia/Shanghai', 'Asia/Hong_Kong', 'Asia/Tokyo', 'Asia/Singapore', 'America/New_York', 'Europe/London'];

export default function SettingsPage() {
  const { principal } = useAuth();
  if (!principal) return null;

  return (
    <div style={{ maxWidth: 780, margin: '0 auto' }}>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        用户中心
      </Typography.Title>
      <Tabs
        defaultActiveKey="overview"
        items={[
          { key: 'overview', label: '账户概览', children: <OverviewTab /> },
          { key: 'profile', label: '个人资料', children: <ProfileTab /> },
          { key: 'security', label: '账户安全', children: <SecurityTab /> },
          { key: 'phone', label: '手机绑定', children: <PhoneTab /> },
        ]}
      />
    </div>
  );
}

// ════════════════════════════════════════════════════════════
// 账户概览（原设置页内容）
// ════════════════════════════════════════════════════════════

function OverviewTab() {
  const { message, modal } = App.useApp();
  const { principal, logout } = useAuth();
  const navigate = useNavigate();

  const plansQ = useQuery({ queryKey: ['billing', 'plans'], queryFn: getPlans, staleTime: 60_000 });
  const plan = plansQ.data?.find((p) => p.code === principal?.plan_code);

  if (!principal) return null;

  const confirmLogout = () => {
    modal.confirm({
      title: '退出登录？',
      content: '退出后需要重新登录才能查看监测数据',
      okText: '退出登录',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => {
        logout();
        message.success('已退出登录');
        navigate('/login', { replace: true });
      },
    });
  };

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card style={{ borderRadius: 16 }}>
        <Typography.Title level={5}>账户信息</Typography.Title>
        <Descriptions
          column={1}
          items={[
            { key: 'email', label: '邮箱', children: principal.email },
            {
              key: 'plan',
              label: '当前套餐',
              children: (
                <Space>
                  <span>{plan?.name ?? principal.plan_code}</span>
                  <Button size="small" type="link" style={{ padding: 0 }} onClick={() => navigate('/plans')}>
                    切换套餐
                  </Button>
                </Space>
              ),
            },
            {
              key: 'roles',
              label: '角色',
              children: principal.roles.map((r) => (
                <Tag key={r} style={{ marginInlineEnd: 4 }}>
                  {ROLE_LABELS[r] ?? r}
                </Tag>
              )),
            },
            { key: 'user', label: '用户 ID', children: <Typography.Text code style={{ fontSize: 12 }}>{principal.user_id}</Typography.Text> },
            { key: 'tenant', label: '租户 ID', children: <Typography.Text code style={{ fontSize: 12 }}>{principal.tenant_id}</Typography.Text> },
          ]}
        />
      </Card>

      <Card style={{ borderRadius: 16 }}>
        <Typography.Title level={5}>使用帮助</Typography.Title>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 4 }}>
          · 报告与原始文档的保存时长取决于当前套餐的数据保留策略（30 / 90 / 365 天）
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 4 }}>
          · Token 用量明细可在「用量」页查看，超额部分按套餐费率计费
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary">
          · 如需更多帮助，可通过客服邮箱联系：support@yuqing.example.com
        </Typography.Paragraph>
      </Card>

      <Divider plain>危险操作</Divider>
      <Card style={{ borderRadius: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <Typography.Text strong>退出登录</Typography.Text>
            <div>
              <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                清除本地登录状态，下次访问需要重新登录
              </Typography.Text>
            </div>
          </div>
          <Popconfirm title="确定退出登录？" okText="退出" cancelText="取消" onConfirm={confirmLogout}>
            <Button danger icon={<LogoutOutlined />}>
              退出登录
            </Button>
          </Popconfirm>
        </div>
      </Card>
    </Space>
  );
}

// ════════════════════════════════════════════════════════════
// 个人资料（昵称 / 时区 / 邮箱验证）
// ════════════════════════════════════════════════════════════

function ProfileTab() {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [form] = Form.useForm();
  const profileQ = useQuery({ queryKey: ['user', 'profile'], queryFn: getProfile });

  const profile = profileQ.data;

  useEffect(() => {
    if (profile) form.setFieldsValue({ name: profile.name, timezone: profile.timezone || 'Asia/Shanghai' });
  }, [profile, form]);

  const saveM = useMutation({
    mutationFn: (v: { name: string; timezone: string }) => updateProfile(v),
    onSuccess: () => {
      message.success('资料已保存');
      queryClient.invalidateQueries({ queryKey: ['user', 'profile'] });
    },
  });

  const sendM = useMutation({
    mutationFn: sendVerificationEmail,
    onSuccess: (msg) => message.success(msg || '验证邮件已发送，请查收'),
  });

  if (profile && !profile.email_verified) {
    return (
      <>
        <Alert
          type="warning"
          showIcon
          message="邮箱未验证"
          description="验证邮箱后可解锁全部功能（未验证也可免费试用 1 次分析）。"
          action={
            <Button size="small" loading={sendM.isPending} onClick={() => sendM.mutate()}>
              发送验证邮件
            </Button>
          }
          style={{ marginBottom: 16 }}
        />
        <ProfileForm form={form} profile={profile} saving={saveM.isPending} onSave={(v) => saveM.mutate(v)} />
      </>
    );
  }
  return <ProfileForm form={form} profile={profile} saving={saveM.isPending} onSave={(v) => saveM.mutate(v)} />;
}

function ProfileForm({
  form,
  profile,
  saving,
  onSave,
}: {
  form: FormInstance<{ name: string; timezone: string }>;
  profile?: UserProfile;
  saving: boolean;
  onSave: (v: { name: string; timezone: string }) => void;
}) {
  return (
    <Card style={{ borderRadius: 16 }}>
      <Form form={form} layout="vertical" onFinish={onSave}>
        <Form.Item
          label="昵称"
          name="name"
          rules={[
            { required: true, message: '请输入昵称' },
            { min: 2, max: 20, message: '昵称长度 2-20 个字符' },
          ]}
        >
          <Input placeholder="2-20 个字符" />
        </Form.Item>
        <Form.Item label="邮箱">
          <Space>
            <Input value={profile?.email} disabled style={{ width: 260 }} />
            {profile?.email_verified ? <Tag color="success">已验证</Tag> : <Tag color="warning">未验证</Tag>}
          </Space>
        </Form.Item>
        <Form.Item label="手机号">
          <Space>
            <Input value={profile?.phone || '未绑定'} disabled style={{ width: 260 }} />
          </Space>
        </Form.Item>
        <Form.Item label="时区" name="timezone">
          <Select options={TIMEZONES.map((tz) => ({ value: tz, label: tz }))} />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={saving}>
          保存更改
        </Button>
      </Form>
    </Card>
  );
}

// ════════════════════════════════════════════════════════════
// 账户安全（修改密码）
// ════════════════════════════════════════════════════════════

function SecurityTab() {
  const { message } = App.useApp();
  const { logout } = useAuth();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [form] = Form.useForm();
  const profileQ = useQuery({ queryKey: ['user', 'profile'], queryFn: getProfile });

  const changeM = useMutation({
    mutationFn: (v: { old: string; next: string }) => changePassword(v.old, v.next),
    onSuccess: () => {
      setOpen(false);
      form.resetFields();
      message.success('密码修改成功，请重新登录');
      logout();
      navigate('/login', { replace: true });
    },
  });

  return (
    <Card style={{ borderRadius: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Space direction="vertical" size={0}>
          <Space>
            <SafetyCertificateOutlined />
            <Typography.Text strong>登录密码</Typography.Text>
          </Space>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {profileQ.data?.password_changed_at
              ? `上次修改：${new Date(profileQ.data.password_changed_at).toLocaleString()}`
              : '从未修改过密码'}
          </Typography.Text>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            至少 8 位，需同时包含字母和数字
          </Typography.Text>
        </Space>
        <Button type="primary" onClick={() => setOpen(true)}>
          修改密码
        </Button>
      </div>

      <Modal
        title="修改密码"
        open={open}
        onCancel={() => setOpen(false)}
        confirmLoading={changeM.isPending}
        onOk={() => form.submit()}
        okText="确认修改"
        cancelText="取消"
        destroyOnHidden
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={(v) => changeM.mutate({ old: v.oldPassword, next: v.newPassword })}
        >
          <Form.Item label="旧密码" name="oldPassword" rules={[{ required: true, message: '请输入旧密码' }]}>
            <Input.Password />
          </Form.Item>
          <Form.Item
            label="新密码"
            name="newPassword"
            rules={[
              { required: true, message: '请输入新密码' },
              { min: 8, message: '密码至少 8 位' },
              {
                pattern: /^(?=.*[A-Za-z])(?=.*\d)\S+$/,
                message: '需同时包含字母和数字',
              },
            ]}
          >
            <Input.Password />
          </Form.Item>
          <Form.Item
            label="确认新密码"
            name="confirm"
            dependencies={['newPassword']}
            rules={[
              { required: true, message: '请再次输入新密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue('newPassword') === value) return Promise.resolve();
                  return Promise.reject(new Error('两次输入的密码不一致'));
                },
              }),
            ]}
          >
            <Input.Password />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
}

// ════════════════════════════════════════════════════════════
// 手机绑定（绑定 2 步 + 解绑密码确认）
// ════════════════════════════════════════════════════════════

function PhoneTab() {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const profileQ = useQuery({ queryKey: ['user', 'profile'], queryFn: getProfile });
  const [bindOpen, setBindOpen] = useState(false);
  const [unbindOpen, setUnbindOpen] = useState(false);

  const profile = profileQ.data;
  const bound = !!profile?.phone_verified;

  const refresh = () => queryClient.invalidateQueries({ queryKey: ['user', 'profile'] });

  return (
    <Card style={{ borderRadius: 16 }}>
      <Descriptions column={1} items={[{
        key: 'phone',
        label: '手机号',
        children: (
          <Space>
            <span>{profile ? profile.phone || '未绑定' : '—'}</span>
            {bound && <Tag color="success">已验证</Tag>}
          </Space>
        ),
      }]} />
      <Typography.Paragraph type="secondary" style={{ fontSize: 13 }}>
        绑定手机号后可用于登录、找回密码与支付验证（登录功能将在 F19 上线）。
      </Typography.Paragraph>
      {bound ? (
        <Button danger onClick={() => setUnbindOpen(true)}>解绑手机号</Button>
      ) : (
        <Button type="primary" onClick={() => setBindOpen(true)}>绑定手机号</Button>
      )}

      <BindPhoneModal
        open={bindOpen}
        onClose={() => setBindOpen(false)}
        onBound={() => {
          setBindOpen(false);
          refresh();
          message.success('手机号绑定成功');
        }}
      />
      <UnbindPhoneModal
        open={unbindOpen}
        phone={profile?.phone ?? ''}
        onClose={() => setUnbindOpen(false)}
        onUnbound={() => {
          setUnbindOpen(false);
          refresh();
          message.success('已解绑');
        }}
      />
    </Card>
  );
}

function BindPhoneModal({ open, onClose, onBound }: { open: boolean; onClose: () => void; onBound: () => void }) {
  const { message } = App.useApp();
  const [step, setStep] = useState<1 | 2>(1);
  const [phone, setPhone] = useState('');
  const [countdown, setCountdown] = useState(0);
  const [code, setCode] = useState('');

  useEffect(() => {
    if (!open) {
      setStep(1);
      setPhone('');
      setCode('');
      setCountdown(0);
    }
  }, [open]);

  useEffect(() => {
    if (countdown <= 0) return;
    const t = setTimeout(() => setCountdown((c) => c - 1), 1000);
    return () => clearTimeout(t);
  }, [countdown]);

  const sendM = useMutation({
    mutationFn: () => sendPhoneCode(phone),
    onSuccess: () => {
      message.success('验证码已发送');
      setCountdown(60);
      setStep(2);
    },
  });

  const bindM = useMutation({
    mutationFn: () => bindPhone(phone, code),
    onSuccess: onBound,
  });

  return (
    <Modal
      title={`绑定手机号${step === 2 ? '（步骤 2/2）' : ''}`}
      open={open}
      onCancel={onClose}
      footer={null}
      destroyOnHidden
    >
      {step === 1 ? (
        <>
          <Form layout="vertical">
            <Form.Item label="手机号" required>
              <Input
                placeholder="11 位手机号"
                value={phone}
                maxLength={11}
                onChange={(e) => setPhone(e.target.value.replace(/\D/g, ''))}
              />
            </Form.Item>
          </Form>
          <Button
            type="primary"
            block
            disabled={!/^1[3-9]\d{9}$/.test(phone)}
            loading={sendM.isPending}
            onClick={() => sendM.mutate()}
          >
            发送验证码
          </Button>
        </>
      ) : (
        <>
          <Alert type="info" showIcon message={`验证码已发送至 ${phone.slice(0, 3)}****${phone.slice(7)}`} style={{ marginBottom: 16 }} />
          <Form layout="vertical">
            <Form.Item label="验证码" required extra={
              countdown > 0
                ? `${countdown}s 后可重新发送`
                : <Button type="link" size="small" style={{ padding: 0 }} onClick={() => sendM.mutate()}>重新发送</Button>
            }>
              <Input
                placeholder="6 位验证码"
                value={code}
                maxLength={6}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
              />
            </Form.Item>
          </Form>
          <Space style={{ width: '100%', justifyContent: 'flex-end' }}>
            <Button onClick={() => setStep(1)}>上一步</Button>
            <Button type="primary" disabled={code.length !== 6} loading={bindM.isPending} onClick={() => bindM.mutate()}>
              确认绑定
            </Button>
          </Space>
          <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 12, marginBottom: 0 }}>
            绑定后可用于：手机号登录（F19）、找回密码、支付验证
          </Typography.Paragraph>
        </>
      )}
    </Modal>
  );
}

function UnbindPhoneModal({ open, phone, onClose, onUnbound }: { open: boolean; phone: string; onClose: () => void; onUnbound: () => void }) {
  const [password, setPassword] = useState('');
  const unbindM = useMutation({
    mutationFn: () => unbindPhone(password),
    onSuccess: onUnbound,
  });

  useEffect(() => {
    if (!open) setPassword('');
  }, [open]);

  return (
    <Modal
      title="确认解绑手机号"
      open={open}
      onCancel={onClose}
      confirmLoading={unbindM.isPending}
      okText="确认解绑"
      okButtonProps={{ danger: true, disabled: !password }}
      onOk={() => unbindM.mutate()}
      cancelText="取消"
      destroyOnHidden
    >
      <Alert
        type="warning"
        showIcon
        message="解绑后将无法使用手机号登录和找回密码"
        style={{ marginBottom: 16 }}
      />
      <Typography.Paragraph>
        即将解绑：<strong>{phone}</strong>
      </Typography.Paragraph>
      <Form layout="vertical">
        <Form.Item label="为了账户安全，请输入登录密码确认" required>
          <Input.Password value={password} onChange={(e) => setPassword(e.target.value)} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
