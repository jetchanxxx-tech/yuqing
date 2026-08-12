import { Card, Table, Tag, Typography } from 'antd';
import { Link } from 'react-router-dom';

const mockData = [
  { id: '01HERE', name: '新品发布会舆情', type: '事件分析', state: 'completed', created: '2026-08-10' },
  { id: '01HERF', name: 'Q3品牌声誉月报', type: '品牌声誉', state: 'analyzing', created: '2026-08-11' },
];

const stateColors: Record<string, string> = {
  draft: 'default', queued: 'processing', fetching: 'processing',
  analyzing: 'processing', generating_report: 'processing',
  completed: 'success', failed: 'error', canceled: 'warning',
};

export default function AnalysisListPage() {
  return (
    <div>
      <Typography.Title level={4}>分析任务</Typography.Title>
      <Card style={{ borderRadius: 16 }}>
        <Table dataSource={mockData} rowKey="id" columns={[
          { title: '名称', dataIndex: 'name', key: 'name', render: (text: string, r: any) => <Link to={`/analyses/${r.id}`}>{text}</Link> },
          { title: '类型', dataIndex: 'type', key: 'type' },
          { title: '状态', dataIndex: 'state', key: 'state', render: (s: string) => <Tag color={stateColors[s]}>{s}</Tag> },
          { title: '创建时间', dataIndex: 'created', key: 'created' },
        ]} />
      </Card>
    </div>
  );
}
