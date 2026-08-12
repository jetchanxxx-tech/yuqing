import { Card, Table, Tag, Typography } from 'antd';

export default function InvoicesPage() {
  return (
    <div>
      <Typography.Title level={4}>账单记录</Typography.Title>
      <Card style={{ borderRadius: 16 }}>
        <Table dataSource={[]} rowKey="id" columns={[
          { title: '账单编号', dataIndex: 'id', key: 'id' },
          { title: '周期', dataIndex: 'period', key: 'period' },
          { title: '金额', dataIndex: 'amount', key: 'amount' },
          { title: '状态', dataIndex: 'status', key: 'status', render: () => <Tag color="success">已支付</Tag> },
        ]} locale={{ emptyText: '暂无账单' }} />
      </Card>
    </div>
  );
}
