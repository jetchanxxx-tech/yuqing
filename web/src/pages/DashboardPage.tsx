import { Card, Row, Col, Statistic, Table, Typography } from 'antd';
import { ArrowUpOutlined, ArrowDownOutlined } from '@ant-design/icons';

export default function DashboardPage() {
  return (
    <div>
      <Typography.Title level={4}>数据面板</Typography.Title>
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}><Card><Statistic title="分析任务" value={12} /></Card></Col>
        <Col span={6}><Card><Statistic title="采集文档" value={2847} /></Card></Col>
        <Col span={6}><Card><Statistic title="正面情绪" value={65.3} suffix="%" prefix={<ArrowUpOutlined style={{ color: '#02b940' }} />} /></Card></Col>
        <Col span={6}><Card><Statistic title="负面情绪" value={12.1} suffix="%" prefix={<ArrowDownOutlined style={{ color: '#ff2442' }} />} /></Card></Col>
      </Row>
      <Row gutter={16}>
        <Col span={12}><Card title="声量趋势"><div style={{ height: 200, background: '#fafafa', borderRadius: 8 }} /></Card></Col>
        <Col span={12}><Card title="来源分布"><div style={{ height: 200, background: '#fafafa', borderRadius: 8 }} /></Card></Col>
      </Row>
      <Card title="热门内容" style={{ marginTop: 16 }}>
        <Table dataSource={[]} columns={[
          { title: '标题', dataIndex: 'title', key: 'title' },
          { title: '来源', dataIndex: 'source', key: 'source' },
          { title: '情感', dataIndex: 'sentiment', key: 'sentiment' },
        ]} />
      </Card>
    </div>
  );
}
