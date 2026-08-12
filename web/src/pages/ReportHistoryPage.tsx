import { Card, Table, Tag, Button, Typography } from 'antd';
import { DownloadOutlined } from '@ant-design/icons';

export default function ReportHistoryPage() {
  return (
    <div>
      <Typography.Title level={4}>报告中心</Typography.Title>
      <Card style={{ borderRadius: 16 }}>
        <Table dataSource={[
          { id: 'R01', title: '新品发布会舆情日报', format: 'html', status: 'completed', date: '2026-08-11' },
          { id: 'R02', title: 'Q3品牌声誉周报', format: 'markdown', status: 'completed', date: '2026-08-10' },
        ]} rowKey="id" columns={[
          { title: '报告标题', dataIndex: 'title', key: 'title' },
          { title: '格式', dataIndex: 'format', key: 'format', render: (f: string) => <Tag>{f}</Tag> },
          { title: '状态', dataIndex: 'status', key: 'status', render: () => <Tag color="success">已完成</Tag> },
          { title: '日期', dataIndex: 'date', key: 'date' },
          { title: '操作', key: 'action', render: () => <Button icon={<DownloadOutlined />} size="small">下载</Button> },
        ]} />
      </Card>
    </div>
  );
}
