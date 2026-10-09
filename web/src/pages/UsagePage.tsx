import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Col, Progress, Row, Space, Tag, Typography } from 'antd';
import { Link } from 'react-router-dom';
import { getPlans, getUsage } from '../api/billing';
import { formatNum, formatTokens } from '../lib/format';
import { ErrorBlock, LoadingBlock } from '../components/PageState';

export default function UsagePage() {
  const usageQ = useQuery({ queryKey: ['billing', 'usage'], queryFn: getUsage });
  const plansQ = useQuery({ queryKey: ['billing', 'plans'], queryFn: getPlans, staleTime: 60_000 });
  if (usageQ.isLoading || plansQ.isLoading) return <LoadingBlock rows={6} />;
  if (usageQ.isError || !usageQ.data) return <ErrorBlock description="用量数据加载失败" onRetry={() => void usageQ.refetch()} />;

  const usage = usageQ.data;
  const plan = plansQ.data?.find((item) => item.code === usage.plan_code);
  const limited = !usage.billing_exempt && usage.budget_mode === 'hard_cap' && usage.token_quota > 0;
  const percent = limited ? Math.min(100, Math.max(0, usage.quota_tokens_used / usage.token_quota * 100)) : 0;
  return (
    <div>
      <Typography.Title level={4}>用量概览</Typography.Title>
      <Typography.Paragraph type="secondary">
        {usage.cycle_status === 'configured' && usage.period_start && usage.period_end
          ? `统计周期：${usage.period_start.slice(0, 10)} 至 ${usage.period_end.slice(0, 10)}`
          : '统计周期未配置，当前按累计用量统计'}
      </Typography.Paragraph>
      {limited && percent >= 100 && <Alert type="warning" showIcon message="Token 配额已用尽，新的模型调用将暂停" description="可购买更高配额的套餐；已发生的用量仍会完整记录。" style={{ marginBottom: 16 }} />}
      {usage.billing_exempt && <Alert type="info" showIcon message="当前固定账号免次数与消费限制" description="实际用量照常记录，且不占用团队其他成员的 Token 配额。" style={{ marginBottom: 16 }} />}
      {!usage.provider_cost_complete && <Alert type="info" showIcon message="供应商成本仍有待核对记录" description={`${usage.pending_cost_events} 条事件和 ${usage.unsettled_calls} 次调用尚未完成核对。`} style={{ marginBottom: 16 }} />}
      <Row gutter={[16, 16]}>
        <Col xs={24} md={8}>
          <Card style={{ borderRadius: 16 }}>
            <Typography.Text type="secondary">实际 Token 用量</Typography.Text>
            <Typography.Title level={2}>{formatTokens(usage.actual_tokens)}</Typography.Title>
            <Typography.Paragraph>计入团队配额：{formatTokens(usage.quota_tokens_used)}</Typography.Paragraph>
            <Typography.Text type="secondary">{usage.budget_mode === 'none' ? '当前套餐不限制 Token 配额' : `套餐 Token 配额：${formatTokens(usage.token_quota)}`}</Typography.Text>
            {limited && <Progress percent={Math.round(percent)} />}
          </Card>
        </Col>
        <Col xs={24} md={8}>
          <Card style={{ borderRadius: 16 }}>
            <Typography.Text type="secondary">报告余额</Typography.Text>
            <Typography.Title level={2}>{formatNum(usage.report_balance)} 次</Typography.Title>
            <Typography.Paragraph>{usage.billing_exempt ? '当前账号不扣报告次数' : '每次创建或重跑扣 1 次，失败或取消按当前轮退回'}</Typography.Paragraph>
            <Link to="/plans"><Button>购买报告额度</Button></Link>
          </Card>
        </Col>
        <Col xs={24} md={8}>
          <Card style={{ borderRadius: 16 }}>
            <Typography.Text type="secondary">当前套餐</Typography.Text>
            <Typography.Title level={3}>{plan?.name ?? usage.plan_code}</Typography.Title>
            <Space direction="vertical">
              <Tag>按报告次数收费</Tag>
              <Typography.Text type="secondary">不另收 Token 现金费用</Typography.Text>
              <Link to="/plans"><Button type="primary">查看套餐</Button></Link>
            </Space>
          </Card>
        </Col>
      </Row>
    </div>
  );
}
