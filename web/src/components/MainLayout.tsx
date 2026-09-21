import { Layout, Menu, Button, Dropdown } from 'antd';
import { Outlet, useNavigate, useLocation } from 'react-router-dom';
import { DashboardOutlined, PlusOutlined, FileTextOutlined, CreditCardOutlined, SettingOutlined, LogoutOutlined, BarChartOutlined, FireOutlined } from '@ant-design/icons';
import { useAuth } from '../stores/auth';

const { Header, Sider, Content } = Layout;

const menuItems = [
  { key: '/dashboard', icon: <DashboardOutlined />, label: '数据面板' },
  { key: '/trends', icon: <FireOutlined />, label: '热榜' },
  { key: '/analyses/new', icon: <PlusOutlined />, label: '新建分析' },
  { key: '/analyses', icon: <BarChartOutlined />, label: '分析任务' },
  { key: '/reports', icon: <FileTextOutlined />, label: '报告中心' },
  { key: '/plans', icon: <CreditCardOutlined />, label: '套餐与额度' },
  { key: '/usage', icon: <BarChartOutlined />, label: '用量' },
  { key: '/invoices', icon: <FileTextOutlined />, label: '账单' },
  { key: '/settings', icon: <SettingOutlined />, label: '设置' },
];

export function MainLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { principal, logout } = useAuth();

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider width={200} style={{ background: '#fff', borderRight: '1px solid rgba(0,0,0,0.08)' }}>
        <div style={{ padding: '20px 16px', fontWeight: 700, fontSize: 18, color: '#FF2442', display: 'flex', alignItems: 'center', gap: 8 }}>
          盘古舆情
          <span style={{
            fontSize: 11, fontWeight: 500, color: '#fff', background: 'rgba(255,36,66,0.85)',
            borderRadius: 4, padding: '1px 6px', letterSpacing: 1,
          }}>
            BETA
          </span>
        </div>
        <Menu
          mode="inline"
          selectedKeys={[location.pathname]}
          items={menuItems}
          onClick={({ key }) => navigate(key)}
          style={{ border: 'none' }}
        />
      </Sider>
      <Layout>
        <Header style={{ background: '#fff', padding: '0 24px', display: 'flex', justifyContent: 'flex-end', alignItems: 'center', borderBottom: '1px solid rgba(0,0,0,0.08)' }}>
          <Dropdown menu={{
            items: [
              { key: 'logout', label: '退出登录', icon: <LogoutOutlined />, onClick: logout },
            ],
          }}>
            <Button type="text">{principal?.email}</Button>
          </Dropdown>
        </Header>
        <Content style={{ padding: 24, background: '#f5f5f5' }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  );
}
