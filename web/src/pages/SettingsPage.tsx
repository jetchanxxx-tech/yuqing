import { useQuery } from '@tanstack/react-query';
import { App, Button, Card, Descriptions, Divider, Popconfirm, Space, Tag, Typography } from 'antd';
import { LogoutOutlined } from '@ant-design/icons';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../stores/auth';
import { getPlans } from '../api/billing';

const ROLE_LABELS: Record<string, string> = {
  tenant_admin: '租户管理员',
  platform_admin: '平台管理员',
  admin: '平台管理员',
  analyst: '分析师',
  viewer: '只读成员',
};

export default function SettingsPage() {
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
    <div style={{ maxWidth: 720 }}>
      <Typography.Title level={4}>账户设置</Typography.Title>
      <Card style={{ borderRadius: 16, marginBottom: 16 }}>
        <Typography.Title level={5} style={{ marginTop: 0 }}>
          账户信息
        </Typography.Title>
        <Descriptions
          column={{ xs: 1, sm: 2 }}
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
    </div>
  );
}
