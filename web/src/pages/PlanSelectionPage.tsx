import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { App, Button, Card, Col, Row, Tag, Typography } from 'antd';
import { CheckOutlined, CrownFilled, ReloadOutlined } from '@ant-design/icons';
import { getPlans, subscribePlan, type Plan } from '../api/billing';
import { formatCny } from '../lib/format';
import { useAuth } from '../stores/auth';
import { ErrorBlock, LoadingBlock } from '../components/PageState';

/** 套餐权益说明（后端 plans 仅返回价格与配额，权益文案按 code 静态补充） */
const PLAN_FEATURES: Record<string, string[]> = {
  free: ['1M tokens / 月', '2 次分析 / 月', 'HTML 报告', '30 天数据保留'],
  pro: ['10M tokens / 月', '50 次分析 / 月', 'HTML + Markdown 报告', '90 天数据保留', '多 Agent 辩论研判'],
  business: ['100M tokens / 月', '500 次分析 / 月', 'PDF / DOCX 报告', '365 天数据保留', 'API 访问'],
  enterprise: ['Token 无上限', '分析次数无上限', '全部报告格式', '自定义模型接入', '专属支持'],
};

const HIGHLIGHT: Record<string, string> = {
  pro: '最受欢迎',
  enterprise: '旗舰之选',
};

export default function PlanSelectionPage() {
  const { message } = App.useApp();
  const { principal } = useAuth();
  const queryClient = useQueryClient();
  const [subscribingCode, setSubscribingCode] = useState<string | null>(null);

  const { data: plans, isLoading, isError, refetch } = useQuery({
    queryKey: ['billing', 'plans'],
    queryFn: getPlans,
  });

  const subscribeQ = useMutation({
    mutationFn: async (code: string) => {
      setSubscribingCode(code);
      return subscribePlan(code);
    },
    onSuccess: () => {
      message.success('订阅请求已提交');
      void queryClient.invalidateQueries({ queryKey: ['billing', 'usage'] });
    },
  });

  if (isLoading) {
    return (
      <div>
        <Typography.Title level={4}>选择套餐</Typography.Title>
        <Card style={{ borderRadius: 16 }}>
          <LoadingBlock rows={6} />
        </Card>
      </div>
    );
  }
  if (isError || !plans) {
    return (
      <div>
        <Typography.Title level={4}>选择套餐</Typography.Title>
        <Card style={{ borderRadius: 16 }}>
          <ErrorBlock description="套餐列表加载失败" onRetry={() => void refetch()} />
        </Card>
      </div>
    );
  }

  const currentCode = principal?.plan_code;

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div>
          <Typography.Title level={4} style={{ margin: 0 }}>
            选择套餐
          </Typography.Title>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            按 Token 用量计费，随时可升降级
          </Typography.Text>
        </div>
        <Button icon={<ReloadOutlined />} onClick={() => void refetch()}>
          刷新
        </Button>
      </div>
      <Row gutter={[16, 16]}>
        {plans.map((p: Plan) => {
          const isCurrent = p.code === currentCode;
          const recommended = Boolean(HIGHLIGHT[p.code]);
          const features = PLAN_FEATURES[p.code] ?? [];
          return (
            <Col xs={24} sm={12} xl={6} key={p.code}>
              <Card
                style={{
                  borderRadius: 16,
                  height: '100%',
                  display: 'flex',
                  flexDirection: 'column',
                  borderColor: recommended ? '#FF2442' : undefined,
                }}
                styles={{ body: { flex: 1, display: 'flex', flexDirection: 'column' } }}
              >
                <div style={{ minHeight: 24, marginBottom: 4 }}>
                  {isCurrent && <Tag color="#FF2442">当前套餐</Tag>}
                  {!isCurrent && recommended && <Tag color="rgba(255,125,3,0.12)">{HIGHLIGHT[p.code]}</Tag>}
                </div>
                <Typography.Title level={5} style={{ margin: 0, fontSize: 18 }}>
                  {p.name}
                </Typography.Title>
                <div style={{ margin: '12px 0 16px' }}>
                  <span style={{ fontSize: 30, fontWeight: 600, color: '#FF2442', fontVariantNumeric: 'tabular-nums' }}>
                    {p.price_monthly_cny === 0 ? '免费' : formatCny(p.price_monthly_cny)}
                  </span>
                  {p.price_monthly_cny > 0 && (
                    <span style={{ color: 'rgba(0,0,0,0.45)', fontSize: 13 }}> / 月</span>
                  )}
                </div>
                <ul style={{ listStyle: 'none', padding: 0, margin: 0, flex: 1 }}>
                  {features.map((f) => (
                    <li
                      key={f}
                      style={{
                        display: 'flex',
                        gap: 8,
                        alignItems: 'center',
                        color: 'rgba(0,0,0,0.62)',
                        fontSize: 14,
                        marginBottom: 10,
                      }}
                    >
                      <CheckOutlined style={{ color: '#02b940', fontSize: 12 }} />
                      {f}
                    </li>
                  ))}
                </ul>
                <Button
                  type={recommended ? 'primary' : 'default'}
                  block
                  size="large"
                  style={{ marginTop: 20 }}
                  disabled={isCurrent || subscribeQ.isPending}
                  loading={subscribeQ.isPending && subscribingCode === p.code}
                  onClick={() => subscribeQ.mutate(p.code)}
                >
                  {isCurrent ? '当前套餐' : p.price_monthly_cny === 0 ? '选择免费版' : `升级到${p.name}`}
                </Button>
              </Card>
            </Col>
          );
        })}
      </Row>
      <Card style={{ borderRadius: 16, marginTop: 16 }} styles={{ body: { display: 'flex', gap: 12, alignItems: 'center' } }}>
        <CrownFilled style={{ color: '#ff7d03', fontSize: 20 }} />
        <Typography.Text type="secondary" style={{ flex: 1 }}>
          全部套餐均支持随时切换；超额用量按套餐费率单独计费，无隐形费用。企业客户可联系商务获得专属方案。
        </Typography.Text>
      </Card>
    </div>
  );
}
