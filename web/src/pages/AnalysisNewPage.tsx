import { Card, Form, Input, Select, Button, Steps, Typography } from 'antd';
import { useState } from 'react';

const steps = [
  { title: '分析类型' },
  { title: '关键词设置' },
  { title: '数据源选择' },
  { title: '确认提交' },
];

export default function AnalysisNewPage() {
  const [current, setCurrent] = useState(0);

  return (
    <div>
      <Typography.Title level={4}>新建分析</Typography.Title>
      <Card style={{ borderRadius: 16 }}>
        <Steps current={current} items={steps} style={{ marginBottom: 24 }} />
        <Form layout="vertical">
          <Form.Item label="分析名称" name="name" rules={[{ required: true }]}>
            <Input size="large" placeholder="例如：新品上线舆情监测" />
          </Form.Item>
          <Form.Item label="分析类型" name="type">
            <Select size="large" options={[
              { value: 'event', label: '舆情事件分析' },
              { value: 'brand', label: '品牌声誉监测' },
              { value: 'competitor', label: '竞品动态追踪' },
              { value: 'industry', label: '行业趋势洞察' },
            ]} />
          </Form.Item>
          <Form.Item label="关键词" name="keywords">
            <Select mode="tags" size="large" placeholder="输入关键词后按回车" style={{ width: '100%' }} />
          </Form.Item>
          <div style={{ display: 'flex', gap: 8 }}>
            <Button onClick={() => setCurrent(Math.max(0, current - 1))}>上一步</Button>
            <Button type="primary" onClick={() => setCurrent(Math.min(3, current + 1))} style={{ borderRadius: 9999 }}>下一步</Button>
          </div>
        </Form>
      </Card>
    </div>
  );
}
