import { Card, Row, Col, Button, Typography, Tag } from 'antd';
import { CheckCircleOutlined } from '@ant-design/icons';

const plans = [
  { code: 'free', name: '体验版', price: '免费', quota: '1M tokens/月', analyses: 5, seats: 1, features: ['HTML报告', '5个数据源', '30天保留'] },
  { code: 'pro', name: '专业版', price: '¥99/月', quota: '10M tokens/月', analyses: 50, seats: 3, features: ['HTML+MD报告', '全部数据源', '90天保留', '辩论分析'], recommended: true },
  { code: 'business', name: '企业版', price: '¥499/月', quota: '100M tokens/月', analyses: 500, seats: 20, features: ['HTML+MD+PDF+DOCX', 'API访问', '1年保留', '辩论分析'] },
  { code: 'enterprise', name: '旗舰版', price: '议价', quota: '无限制', analyses: -1, seats: -1, features: ['全部功能', '自定义模型', '私有部署', '专属支持'] },
];

export default function PlanSelectionPage() {
  return (
    <div>
      <Typography.Title level={4}>选择套餐</Typography.Title>
      <Row gutter={16}>
        {plans.map((p) => (
          <Col span={6} key={p.code}>
            <Card style={{ borderRadius: 16, textAlign: 'center', borderColor: p.recommended ? '#FF2442' : undefined }}>
              {p.recommended && <Tag color="#FF2442" style={{ marginBottom: 8 }}>推荐</Tag>}
              <Typography.Title level={3}>{p.name}</Typography.Title>
              <Typography.Title level={2} style={{ color: '#FF2442' }}>{p.price}</Typography.Title>
              <div style={{ marginBottom: 8, color: 'rgba(0,0,0,0.62)' }}>{p.quota}</div>
              <div style={{ marginBottom: 8 }}>{p.analyses > 0 ? `${p.analyses}次/月` : '无限制'}</div>
              <div style={{ marginBottom: 16 }}>{p.seats > 0 ? `${p.seats}席位` : '无限制席位'}</div>
              <Button type={p.recommended ? 'primary' : 'default'} block style={{ borderRadius: 9999 }}>
                选择{p.name}
              </Button>
            </Card>
          </Col>
        ))}
      </Row>
    </div>
  );
}
