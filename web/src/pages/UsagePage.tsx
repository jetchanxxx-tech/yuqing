import { Card, Progress, Row, Col, Typography } from 'antd';

export default function UsagePage() {
  return (
    <div>
      <Typography.Title level={4}>用量概览</Typography.Title>
      <Row gutter={16}>
        <Col span={8}>
          <Card style={{ borderRadius: 16, textAlign: 'center' }}>
            <Typography.Title level={2} style={{ color: '#FF2442' }}>0/1M</Typography.Title>
            <div>Token 用量</div>
            <Progress percent={0} showInfo={false} strokeColor="#FF2442" />
          </Card>
        </Col>
        <Col span={8}>
          <Card style={{ borderRadius: 16, textAlign: 'center' }}>
            <Typography.Title level={2}>0/5</Typography.Title>
            <div>分析次数（本月）</div>
            <Progress percent={0} showInfo={false} />
          </Card>
        </Col>
        <Col span={8}>
          <Card style={{ borderRadius: 16, textAlign: 'center' }}>
            <Typography.Title level={2}>体验版</Typography.Title>
            <div>当前套餐</div>
          </Card>
        </Col>
      </Row>
    </div>
  );
}
