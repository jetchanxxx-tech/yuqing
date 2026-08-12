import { Card, Table, Tag, Button, Typography, Space } from 'antd';

export default function AdminPage() {
  return (
    <div>
      <Typography.Title level={4}>管理后台</Typography.Title>
      <Card style={{ borderRadius: 16 }}>
        <Table dataSource={[
          { id: 'T01', name: '测试租户', plan: 'free', status: 'active', users: 1, created: '2026-08-11' },
        ]} rowKey="id" columns={[
          { title: '租户', dataIndex: 'name', key: 'name' },
          { title: '套餐', dataIndex: 'plan', key: 'plan' },
          { title: '状态', dataIndex: 'status', key: 'status', render: (s: string) => <Tag color={s === 'active' ? 'success' : 'warning'}>{s}</Tag> },
          { title: '用户数', dataIndex: 'users', key: 'users' },
          { title: '创建时间', dataIndex: 'created', key: 'created' },
          { title: '操作', key: 'action', render: () => (
            <Space>
              <Button size="small" danger>挂起</Button>
              <Button size="small">详情</Button>
            </Space>
          )},
        ]} />
      </Card>
    </div>
  );
}
