import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Descriptions,
  Input,
  Row,
  Select,
  Space,
  Steps,
  Tag,
  Typography,
} from 'antd';
import {
  CheckCircleFilled,
  CompassOutlined,
  RocketOutlined,
  SafetyOutlined,
  TeamOutlined,
} from '@ant-design/icons';
import { Link, useNavigate } from 'react-router-dom';
import { createAnalysis } from '../api/analyses';
import { getCredits, getUsage } from '../api/billing';
import {
  ANALYSIS_TYPES,
  SOURCES,
  SOURCE_LABELS,
  type AnalysisType,
  type SourceKey,
} from '../lib/constants';

/** 成本估算：每个「关键词 × 数据源」约消耗 1,200 token（采集摘要+研判），另加固定报告 token */
const TOKEN_PER_KEYWORD_SOURCE = 1200;
const BASE_TOKENS = 3000;

const TYPE_ICONS = [SafetyOutlined, RocketOutlined, TeamOutlined, CompassOutlined];

const STEP_LABELS = ['分析类型', '关键词设置', '数据源选择', '确认提交'];

export default function AnalysisNewPage() {
  const { message } = App.useApp();
  const navigate = useNavigate();

  const [current, setCurrent] = useState(0);
  const [name, setName] = useState('');
  const [type, setType] = useState<AnalysisType | null>(null);
  const [keywords, setKeywords] = useState<string[]>([]);
  const [sources, setSources] = useState<SourceKey[]>([]);

  const usageQ = useQuery({ queryKey: ['billing', 'usage'], queryFn: getUsage, staleTime: 30_000 });
  const creditsQ = useQuery({ queryKey: ['billing', 'credits'], queryFn: getCredits, staleTime: 30_000 });

  const estTokens = BASE_TOKENS + Math.max(keywords.length, 0) * Math.max(sources.length, 0) * TOKEN_PER_KEYWORD_SOURCE;
  const remainingTokens = usageQ.data ? usageQ.data.tokens_quota - usageQ.data.tokens_used : null;
  const overBudget = remainingTokens !== null && estTokens > remainingTokens;
  /** 报告额度（方案 B：每次分析 = 1 次） */
  const noCredits = (creditsQ.data?.balance ?? 1) <= 0;

  const canNext =
    current === 0
      ? name.trim().length >= 2 && type !== null
      : current === 1
        ? keywords.length >= 1
        : current === 2
          ? sources.length >= 1
          : true;

  const createQ = useMutation({
    mutationFn: () =>
      createAnalysis({
        name: name.trim(),
        analysis_type: type as AnalysisType,
        keywords: [...keywords],
        sources,
      }),
    onSuccess: (res) => {
      message.success('分析任务已创建，正在排队执行');
      void creditsQ.refetch();
      navigate(`/analyses/${res.id}`);
    },
    onError: (error) => {
      // 额度不足（402 NO_CREDITS）：明确引导购买，其余错误信封已由全局处理
      const code = (error as { response?: { data?: { code?: string } } })
        ?.response?.data?.code;
      if (code === 'NO_CREDITS') {
        message.warning('报告额度不足，请先购买套餐');
        navigate('/plans');
      }
    },
  });

  const next = () => {
    if (!canNext) {
      if (current === 0) message.warning(name.trim().length < 2 ? '请填写分析名称（至少 2 个字）' : '请选择一种分析类型');
      else if (current === 1) message.warning('请至少输入 1 个关键词');
      else message.warning('请至少选择 1 个数据源');
      return;
    }
    setCurrent((c) => Math.min(c + 1, 3));
  };

  const toggleSource = (s: SourceKey, checked: boolean) => {
    setSources((prev) => (checked ? [...prev, s] : prev.filter((x) => x !== s)));
  };

  const typeCard = (t: (typeof ANALYSIS_TYPES)[number], index: number) => {
    const selected = type === t.value;
    const Icon = TYPE_ICONS[index % TYPE_ICONS.length];
    return (
      <Col xs={24} sm={12} lg={6} key={t.value}>
        <Card
          hoverable
          onClick={() => setType(t.value)}
          style={{
            height: '100%',
            borderRadius: 16,
            borderColor: selected ? '#FF2442' : 'rgba(0,0,0,0.08)',
            boxShadow: selected ? '0 4px 12px rgba(255,36,66,0.12)' : 'none',
          }}
          styles={{ body: { height: '100%', display: 'flex', flexDirection: 'column' } }}
        >
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <Icon style={{ fontSize: 24, color: selected ? '#FF2442' : 'rgba(0,0,0,0.45)' }} />
            {selected ? (
              <CheckCircleFilled style={{ color: '#FF2442', fontSize: 18 }} />
            ) : (
              <span style={{ width: 18, height: 18, borderRadius: 9, border: '2px solid rgba(0,0,0,0.15)' }} />
            )}
          </div>
          <Typography.Text strong style={{ margin: '12px 0 4px', fontSize: 15 }}>
            {t.label}
          </Typography.Text>
          <Typography.Paragraph type="secondary" style={{ fontSize: 13, margin: 0 }}>
            {t.desc}
          </Typography.Paragraph>
        </Card>
      </Col>
    );
  };

  return (
    <div style={{ maxWidth: 920, margin: '0 auto' }}>
      <Typography.Title level={4}>新建分析</Typography.Title>
      <Card style={{ borderRadius: 16, marginBottom: 16 }}>
        <Steps current={current} items={STEP_LABELS.map((title) => ({ title }))} style={{ marginBottom: 32 }} />
        {/* ① 分析类型 */}
        {current === 0 && (
          <div>
            <Typography.Title level={5} style={{ marginTop: 0 }}>
              给这次分析起个名字
            </Typography.Title>
            <Input
              size="large"
              showCount
              maxLength={50}
              placeholder="例如：新品发布会舆情监测"
              value={name}
              onChange={(e) => setName(e.target.value)}
              style={{ maxWidth: 480, marginBottom: 24 }}
            />
            <Typography.Title level={5}>选择分析类型</Typography.Title>
            <Row gutter={[16, 16]}>
              {ANALYSIS_TYPES.map((t, i) => typeCard(t, i))}
            </Row>
          </div>
        )}
        {/* ② 关键词 */}
        {current === 1 && (
          <div>
            <Typography.Title level={5} style={{ marginTop: 0 }}>
              输入监测关键词
            </Typography.Title>
            <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
              输入后按回车确认，可添加多个关键词（建议 2–5 个，避免过于宽泛）
            </Typography.Paragraph>
            <Select
              mode="tags"
              size="large"
              value={keywords}
              onChange={(v: string[]) => setKeywords(Array.from(new Set(v.map((k) => k.trim()).filter(Boolean))).slice(0, 20))}
              placeholder="输入关键词后按回车，如：雅阁 后排舒适性"
              open={false}
              suffixIcon={null}
              style={{ width: '100%' }}
            />
            <div style={{ marginTop: 16 }}>
              <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                常用补充：
              </Typography.Text>
              <Space size={8} style={{ marginTop: 8, display: 'flex', flexWrap: 'wrap' }}>
                {['舆情', '口碑', '投诉', '召回', '涨价', '新品'].map((k) => (
                  <Tag.CheckableTag
                    key={k}
                    checked={keywords.includes(k)}
                    onChange={(checked) =>
                      setKeywords((prev) => {
                        if (checked) return prev.length < 20 ? [...prev, k] : prev;
                        return prev.filter((x) => x !== k);
                      })
                    }
                    style={{ fontSize: 13, padding: '4px 12px', borderRadius: 9999 }}
                  >
                    {k}
                  </Tag.CheckableTag>
                ))}
              </Space>
            </div>
          </div>
        )}
        {/* ③ 数据源 */}
        {current === 2 && (
          <div>
            <Typography.Title level={5} style={{ marginTop: 0 }}>
              选择数据源
            </Typography.Title>
            <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
              支持全平台抓取，至少选择 1 个
            </Typography.Paragraph>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
              {SOURCES.map((s) => {
                const checked = sources.includes(s);
                return (
                  <Tag.CheckableTag
                    key={s}
                    checked={checked}
                    onChange={(c) => toggleSource(s, c)}
                    style={{
                      fontSize: 14,
                      padding: '8px 22px',
                      borderRadius: 9999,
                      border: `1px solid ${checked ? '#FF2442' : 'rgba(0,0,0,0.12)'}`,
                      background: checked ? 'rgba(255,36,66,0.06)' : '#fff',
                      color: checked ? '#FF2442' : 'rgba(0,0,0,0.8)',
                      fontWeight: 500,
                    }}
                  >
                    {SOURCE_LABELS[s]}
                  </Tag.CheckableTag>
                );
              })}
            </div>
          </div>
        )}
        {/* ④ 确认 */}
        {current === 3 && (
          <div>
            <Descriptions
              bordered
              size="small"
              column={1}
              labelStyle={{ width: 110, background: '#fafafa' }}
              items={[
                { key: 'name', label: '分析名称', children: name.trim() },
                {
                  key: 'type',
                  label: '分析类型',
                  children: ANALYSIS_TYPES.find((t) => t.value === type)?.label ?? '-',
                },
                {
                  key: 'keywords',
                  label: '监测关键词',
                  children: keywords.length ? keywords.join(' / ') : '-',
                },
                {
                  key: 'sources',
                  label: '数据源',
                  children: sources.length ? sources.map((s) => SOURCE_LABELS[s]).join('、') : '-',
                },
              ]}
            />
            <Alert
              type={overBudget || noCredits ? 'warning' : 'info'}
              showIcon
              style={{ marginTop: 16 }}
              message="成本预估"
              description={
                <>
                  预计耗时 <b>5-10 分钟</b>（采集 → 五维智能分析 → 报告生成）；
                  本次分析消耗 <b>1 次报告额度</b>
                  {creditsQ.data && (
                    <>，当前剩余 <b style={{ color: noCredits ? '#FF2442' : undefined }}>{creditsQ.data.balance}</b> 次</>
                  )}
                  {noCredits && (
                    <div style={{ marginTop: 8 }}>
                      额度不足，<Link to="/plans">去购买套餐 →</Link>
                    </div>
                  )}
                </>
              }
            />
          </div>
        )}
        {/* 底部操作 */}
        <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 28 }}>
          <Button size="large" disabled={current === 0} onClick={() => setCurrent((c) => Math.max(0, c - 1))}>
            上一步
          </Button>
          {current < 3 ? (
            <Button type="primary" size="large" onClick={next} disabled={!canNext}>
              下一步
            </Button>
          ) : (
            <Button
              type="primary"
              size="large"
              loading={createQ.isPending}
              disabled={!canNext || keywords.length === 0 || sources.length === 0}
              onClick={() => createQ.mutate()}
            >
              提交分析
            </Button>
          )}
        </div>
      </Card>
    </div>
  );
}
