import { Card, Descriptions, Tag, Typography, Tabs } from 'antd';
import { useParams } from 'react-router-dom';

export default function AnalysisDetailPage() {
  const { id } = useParams();

  return (
    <div>
      <Typography.Title level={4}>分析详情</Typography.Title>
      <Card style={{ borderRadius: 16, marginBottom: 16 }}>
        <Descriptions column={2}>
          <Descriptions.Item label="ID">{id}</Descriptions.Item>
          <Descriptions.Item label="状态"><Tag color="success">completed</Tag></Descriptions.Item>
          <Descriptions.Item label="创建时间">2026-08-11 14:00</Descriptions.Item>
          <Descriptions.Item label="完成时间">2026-08-11 14:05</Descriptions.Item>
        </Descriptions>
      </Card>
      <Card style={{ borderRadius: 16 }}>
        <Tabs items={[
          { key: 'docs', label: '原始文档', children: <div>暂无数据</div> },
          { key: 'sentiment', label: '情感分析', children: <div>暂无数据</div> },
          { key: 'topics', label: '话题聚类', children: <div>暂无数据</div> },
          { key: 'report', label: '分析报告', children: <div>暂无数据</div> },
        ]} />
      </Card>
    </div>
  );
}
