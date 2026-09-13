import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Card, Col, Progress, Row, Space, Tag, Typography } from 'antd';
import { Link } from 'react-router-dom';
import { getPlans, getUsage } from '../api/billing';
import { useAuth } from '../stores/auth';
import { formatNum, formatTokenQuota, formatTokens } from '../lib/format';
import { ErrorBlock, LoadingBlock } from '../components/PageState';

function clampPercent(used: number, quota: number): number {
  if (!quota) return 0;
  return Math.max(0, Math.min(100, (used / quota) * 100));
}

export default function UsagePage() {
  const { principal } = useAuth();
  const usageQ = useQuery({ queryKey: ['billing', 'usage'], queryFn: getUsage });
  const plansQ = useQuery({ queryKey: ['billing', 'plans'], queryFn: getPlans, staleTime: 60_000 });

  if (usageQ.isLoading || plansQ.isLoading) {
    return (
      <div>
        <Typography.Title level={4}>用量概览</Typography.Title>
        <Card style={{ borderRadius: 16 }}>
          <LoadingBlock rows={6} />
        </Card>
      </div>
    );
  }

  if (usageQ.isError || !usageQ.data) {
    return (
      <div>
        <Typography.Title level={4}>用量概览</Typography.Title>
        <Card style={{ borderRadius: 16 }}>
          <ErrorBlock description="用量数据加载失败" onRetry={() => void usageQ.refetch()} />
        </Card>
      </div>
    );
  }

  const usage = usageQ.data;
  const plan = plansQ.data?.find((p) => p.code === principal?.plan_code);
  const tokenPct = clampPercent(usage.tokens_used, usage.tokens_quota);
  const analysisPct = clampPercent(usage.analyses_used, usage.analyses_quota);
  const overToken = usage.tokens_quota > 0 && usage.tokens_used >= usage.tokens_quota;

  return (
    <div>
      <Typography.Title level={4}>用量概览</Typography.Title>
      {overToken && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 16 }}
          message="Token 额度即将耗尽或已用尽"
          description={
            <span>
              超额部分将按套餐费率计费，也可以{' '}
              <Link to="/plans">升级套餐</Link> 获取更高额度。
            </span>
          }
        />
      )}
      <Row gutter={[16, 16]}>
        <Col xs={24} md={8}>
          <Card style={{ borderRadius: 16, textAlign: 'center' }}>
            <Typography.Text type="secondary">Token 用量（本月）</Typography.Text>
            <div style={{ margin: '12px 0 4px', fontSize: 32, fontWeight: 600, color: tokenPct > 80 ? '#ff7d03' : 'rgba(0,0,0,0.8)' }}>
              {formatTokens(usage.tokens_used)}
              <span style={{ fontSize: 15, color: 'rgba(0,0,0,0.45)', fontWeight: 400 }}>
                {' / '}
                {formatTokens(usage.tokens_quota)}
              </span>
            </div>
            <Progress type="dashboard" percent={Math.round(tokenPct)} strokeColor={tokenPct > 80 ? '#ff7d03' : '#FF2442'} />
          </Card>
        </Col>
        <Col xs={24} md={8}>
          <Card style={{ borderRadius: 16, textAlign: 'center' }}>
            <Typography.Text type="secondary">分析次数（本月）</Typography.Text>
            <div style={{ margin: '12px 0 4px', fontSize: 32, fontWeight: 600 }}>
              {formatNum(usage.analyses_used)}
              <span style={{ fontSize: 15, color: 'rgba(0,0,0,0.45)', fontWeight: 400 }}>
                {' / '}
                {formatNum(usage.analyses_quota)}
              </span>
            </div>
            <Progress
              type="dashboard"
              percent={Math.round(analysisPct)}
              strokeColor={analysisPct > 80 ? '#ff7d03' : '#FF2442'}
            />
          </Card>
        </Col>
        <Col xs={24} md={8}>
          <Card style={{ borderRadius: 16, textAlign: 'center' }}>
            <Typography.Text type="secondary">当前套餐</Typography.Text>
            <div style={{ margin: '12px 0' }}>
              <Typography.Title level={3} style={{ margin: 0, fontSize: 28 }}>
                {plan?.name ?? principal?.plan_code ?? '-'}
              </Typography.Title>
            </div>
            {principal?.plan_code !== 'enterprise' && (
              <Space direction="vertical" size={8} style={{ width: '100%' }}>
                <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                  {plan ? `${formatTokenQuota(plan.token_quota_m)} / 月` : ''}
                </Typography.Text>
                <Link to="/plans">
                  <Button type="primary" block>
                    升级套餐
                  </Button>
                </Link>
              </Space>
            )}
            {principal?.plan_code === 'enterprise' && (
              <Tag color="#FF2442" style={{ borderRadius: 9999, padding: '4px 16px' }}>
                企业专属额度
              </Tag>
            )}
          </Card>
        </Col>
      </Row>
      <Typography.Paragraph type="secondary" style={{ marginTop: 16, fontSize: 13 }}>
        Token 用量按每次大模型调用计量，覆盖采集摘要、智能研判与报告生成；分析次数按任务创建计次。
      </Typography.Paragraph>
    </div>
  );
}
