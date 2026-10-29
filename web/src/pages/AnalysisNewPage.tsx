import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  Alert,
  App,
  Button,
  Card,
  Checkbox,
  Col,
  Descriptions,
  DatePicker,
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
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import dayjs, { type Dayjs } from 'dayjs';
import 'dayjs/locale/zh-cn';
import datePickerLocale from 'antd/es/date-picker/locale/zh_CN';
import { createAnalysis, getCollectionCapabilities } from '../api/analyses';
import { getCredits, getUsage } from '../api/billing';
import {
  ANALYSIS_TYPES,
  ANALYSIS_TEMPLATES,
  SOURCES,
  SELECTABLE_SOURCES,
  SOURCE_LABELS,
  type AnalysisType,
  type SourceKey,
} from '../lib/constants';

/** 成本估算：每个「关键词 × 数据源」约消耗 1,200 token（采集摘要+研判），另加固定报告 token */
const TOKEN_PER_KEYWORD_SOURCE = 1200;
const BASE_TOKENS = 3000;

const TYPE_ICONS = [SafetyOutlined, RocketOutlined, TeamOutlined, CompassOutlined];

const STEP_LABELS = ['选择场景或视角', '关键词与时间范围', '数据源选择', '确认提交'];

const DATE_RANGE_STYLES = `
  .analysis-date-field-label { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
  @media (max-width: 600px) {
    .analysis-date-range-popup { position: fixed !important; top: 12px !important; left: 12px !important; right: auto !important; bottom: auto !important; transform: none !important; max-width: calc(100vw - 24px); }
    .analysis-date-range-popup .ant-picker-panel-container { width: min(340px, calc(100vw - 24px)); max-height: 72vh; overflow-y: auto; }
    .analysis-date-range-popup .ant-picker-panel-layout { flex-direction: column !important; }
    .analysis-date-range-popup .ant-picker-panels { flex-direction: column !important; }
    .analysis-date-range-popup .ant-picker-presets { width: auto; min-width: 0; max-width: none; border-inline-end: 0; }
    .analysis-date-range-popup .ant-picker-presets ul { display: flex; flex-wrap: wrap; gap: 4px; padding: 8px; }
    .analysis-date-range-popup .ant-picker-presets li { margin: 0; }
  }
`;

export default function AnalysisNewPage() {
  const { message } = App.useApp();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const [current, setCurrent] = useState(0);
  const [name, setName] = useState('');
  const [entry, setEntry] = useState<'template' | 'manual' | null>(null);
  const [templateId, setTemplateId] = useState<string | null>(null);
  const [type, setType] = useState<AnalysisType | null>(null);
  // ?keywords= 支持「热榜 → 分析」预填（热榜页「分析」按钮带参数跳入）
  const [keywords, setKeywords] = useState<string[]>(() => {
    const kw = searchParams.get('keywords');
    return kw ? [kw.trim()].filter(Boolean) : [];
  });
  const [sources, setSources] = useState<SourceKey[]>([]);
  const [researchQuestion, setResearchQuestion] = useState('');
  const [seedInput, setSeedInput] = useState('');
  const [deliveryPolicy, setDeliveryPolicy] = useState<'best_available' | 'full_only'>('best_available');
  const seedUrls = seedInput.split(/\r?\n/).map((value) => value.trim()).filter(Boolean);
  const seedsValid = seedUrls.length <= 10 && seedUrls.every((value) => {
    try { const url = new URL(value); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password; }
    catch { return false; }
  });
  const capabilitiesQ = useQuery({ queryKey: ['collection', 'capabilities'], queryFn: getCollectionCapabilities, staleTime: 60_000 });
  const [dateRange, setDateRange] = useState<[Dayjs | null, Dayjs | null] | null>(() => {
    const today = dayjs().startOf('day');
    return [today.subtract(6, 'day'), today];
  });
  const validDateRange = !!dateRange?.[0]?.isValid() && !!dateRange?.[1]?.isValid()
    && !dateRange[0].isAfter(dateRange[1], 'day') && !dateRange[1].isAfter(dayjs(), 'day');
  const selectedPeriod = validDateRange
    ? `${dateRange![0]!.format('YYYY-MM-DD')} 至 ${dateRange![1]!.format('YYYY-MM-DD')}` : '';

  const usageQ = useQuery({ queryKey: ['billing', 'usage'], queryFn: getUsage, staleTime: 30_000 });
  const creditsQ = useQuery({ queryKey: ['billing', 'credits'], queryFn: getCredits, staleTime: 30_000 });

  const estTokens = BASE_TOKENS + Math.max(keywords.length, 0) * Math.max(sources.length, 0) * TOKEN_PER_KEYWORD_SOURCE;
  const remainingTokens = usageQ.data && !usageQ.data.billing_exempt && usageQ.data.budget_mode === 'hard_cap'
    ? usageQ.data.token_quota - usageQ.data.quota_tokens_used : null;
  const overBudget = remainingTokens !== null && estTokens > remainingTokens;
  /** 报告额度（方案 B：每次分析 = 1 次） */
  const noCredits = creditsQ.data?.billing_exempt !== true && (creditsQ.data?.balance ?? 1) <= 0;
  const selectedTemplate = ANALYSIS_TEMPLATES.find((template) => template.id === templateId);
  const defaultView = ANALYSIS_TYPES.find((item) => item.value === selectedTemplate?.defaultType)?.label;

  const canNext =
    current === 0
      ? name.trim().length >= 2 && entry !== null && (entry === 'manual' || templateId !== null)
      : current === 1
        ? keywords.length >= 1 && validDateRange
        : current === 2
          ? sources.length >= 1 && seedsValid
          : true;

  const createQ = useMutation({
    mutationFn: () => {
      if (!validDateRange || !dateRange?.[0] || !dateRange?.[1]) {
        throw new Error('请选择完整且有效的分析时间范围');
      }
      return createAnalysis({
        name: name.trim(),
        analysis_type: type as AnalysisType,
        ...(entry === 'template' && selectedTemplate ? { scenario_template_id: selectedTemplate.id } : {}),
        keywords: [...keywords],
        seed_urls: seedUrls,
        delivery_policy: deliveryPolicy,
        ...(researchQuestion.trim() ? { research_question: researchQuestion.trim() } : {}),
        sources: sources.filter((source) => SELECTABLE_SOURCES.includes(source)),
        date_from: dateRange[0].format('YYYY-MM-DD'),
        date_to: dateRange[1].format('YYYY-MM-DD'),
      });
    },
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
      if (current === 0) message.warning(name.trim().length < 2 ? '请填写分析名称（至少 2 个字）' : '请选择模板或手动创建');
      else if (current === 1) message.warning(keywords.length === 0 ? '请至少输入 1 个关键词' : '请选择完整且有效的分析时间范围');
      else message.warning('请至少选择 1 个数据源');
      return;
    }
    setCurrent((c) => Math.min(c + 1, 3));
  };

  const toggleSource = (s: SourceKey, checked: boolean) => {
    if (!SELECTABLE_SOURCES.includes(s)) return;
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
      <style>{DATE_RANGE_STYLES}</style>
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
            <Typography.Title level={5}>创建方式</Typography.Title>
            <Space wrap style={{ marginBottom: 16 }}>
              <Button type={entry === 'template' ? 'primary' : 'default'} onClick={() => { setEntry('template'); setTemplateId(null); setType(null); }}>按模板</Button>
              <Button type={entry === 'manual' ? 'primary' : 'default'} onClick={() => { setEntry('manual'); setTemplateId(null); setType(null); }}>手动选择视角</Button>
            </Space>
            {entry === 'template' && (
              <>
                <Alert type="info" showIcon style={{ marginBottom: 16 }} message="按场景专项任务与分析视角运行一次分析"
                  description="周期监测尚未上线；这里不会保存监测方案，也不会自动启用排除词、风险标签、预警或专属报告模板。" />
                <Row gutter={[16, 16]}>
                  {ANALYSIS_TEMPLATES.map((template) => (
                    <Col xs={24} sm={12} lg={8} key={template.id}>
                      <Card hoverable onClick={() => { setTemplateId(template.id); setType(template.defaultType); }}
                        style={{ height: '100%', borderColor: templateId === template.id ? '#FF2442' : undefined }}>
                        <Typography.Text strong>{template.label}</Typography.Text>
                        <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>{template.desc}</Typography.Paragraph>
                      </Card>
                    </Col>
                  ))}
                </Row>
                {selectedTemplate && (
                  <div style={{ marginTop: 20 }}>
                    <Typography.Paragraph>默认分析视角：{defaultView}</Typography.Paragraph>
                    <Typography.Text>分析视角（可修改，与场景专项任务共同生效）</Typography.Text>
                    <Select aria-label="分析视角" value={type} onChange={setType} style={{ display: 'block', maxWidth: 320, marginTop: 8 }}
                      options={ANALYSIS_TYPES.map((item) => ({ value: item.value, label: item.label }))} />
                    <Typography.Paragraph type="secondary" style={{ marginTop: 16 }}>
                      关键词：单次分析时手动输入；数据源：单次分析时手动选择，是否可采集以实际返回为准。<br />
                      排除词：未启用；监测周期：未启用；风险标签：未启用；预警规则：未启用；报告模板：未启用。
                    </Typography.Paragraph>
                  </div>
                )}
              </>
            )}
            {entry === 'manual' && (
              <>
                <Typography.Paragraph type="secondary">四种视角可选；不选择时使用原有通用分析模式。</Typography.Paragraph>
                <Row gutter={[16, 16]}>{ANALYSIS_TYPES.map((t, i) => typeCard(t, i))}</Row>
              </>
            )}
          </div>
        )}
        {/* ② 关键词 */}
        {current === 1 && (
          <div>
            <Typography.Title level={5} style={{ marginTop: 0 }}>
              输入监测关键词
            </Typography.Title>
            <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
              输入后按回车确认，最多 8 个关键词。
            </Typography.Paragraph>
            <Typography.Paragraph type="secondary">多个关键词共同描述同一研究主题，材料无需逐词命中。</Typography.Paragraph>
            <Select
              mode="tags"
              size="large"
              value={keywords}
              onChange={(v: string[]) => setKeywords(Array.from(new Set(v.map((k) => k.trim()).filter(Boolean))).slice(0, 8))}
              placeholder="输入关键词后按回车，如：雅阁 后排舒适性"
              open={false}
              suffixIcon={null}
              style={{ width: '100%' }}
            />
            <Typography.Title level={5} style={{ marginTop: 24 }}>
              <label htmlFor="analysis-research-question">关注问题（选填）</label>
            </Typography.Title>
            <Input.TextArea id="analysis-research-question" value={researchQuestion} onChange={(event) => setResearchQuestion(event.target.value)}
              maxLength={1000} showCount autoSize={{ minRows: 2, maxRows: 5 }}
              placeholder="例如：了解 iPhone18 系列新品的续航与价格反馈；或雅阁后排的乘坐舒适性" />
            <Typography.Paragraph type="secondary" style={{ marginTop: 8 }}>
              可补充型号关系、比较对象与关注点；系统会结合全部关键词理解本轮主题，并在任务详情展示。
            </Typography.Paragraph>
            <Typography.Title level={5} style={{ marginTop: 24 }}>分析时间范围</Typography.Title>
            <label className="analysis-date-field-label" htmlFor="analysis-date-from">开始日期</label>
            <label className="analysis-date-field-label" htmlFor="analysis-date-to">结束日期</label>
            <DatePicker.RangePicker
              className="analysis-date-range-picker"
              classNames={{ popup: { root: 'analysis-date-range-popup' } }}
              id={{ start: 'analysis-date-from', end: 'analysis-date-to' }}
              locale={datePickerLocale}
              format="YYYY-MM-DD"
              placeholder={['开始日期', '结束日期']}
              value={dateRange}
              onChange={setDateRange}
              onCalendarChange={setDateRange}
              disabledDate={(date) => date.isAfter(dayjs(), 'day')}
              presets={[
                { label: '今天', value: [dayjs().startOf('day'), dayjs().startOf('day')] },
                { label: '最近 7 天', value: [dayjs().startOf('day').subtract(6, 'day'), dayjs().startOf('day')] },
                { label: '最近 30 天', value: [dayjs().startOf('day').subtract(29, 'day'), dayjs().startOf('day')] },
              ]}
              style={{ width: '100%', maxWidth: 490 }}
              size="large"
            />
            <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
              按材料来源所示的发布日期筛选，包含开始和结束两天；无法识别日期的材料会排除。
            </Typography.Paragraph>
          </div>
        )}
        {/* ③ 数据源 */}
        {current === 2 && (
          <div>
            <Typography.Title level={5} style={{ marginTop: 0 }}>
              选择数据源
            </Typography.Title>
            <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
              选择希望尝试采集的数据源（至少 1 个）；是否可用、是否有数据以实际执行结果为准。
            </Typography.Paragraph>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
              {SOURCES.map((s) => {
                const enabled = SELECTABLE_SOURCES.includes(s);
                return (
                  <div key={s} style={{ padding: '8px 12px', border: '1px solid #ddd', borderRadius: 8, opacity: enabled ? 1 : 0.45 }}>
                    <Checkbox checked={sources.includes(s)} disabled={!enabled} onChange={(e) => toggleSource(s, e.target.checked)}>
                      {SOURCE_LABELS[s]}
                    </Checkbox>
                    <Tag color={enabled ? 'blue' : 'default'} style={{ marginLeft: 8 }}>
                      {enabled ? '可检索公开网页' : '暂未接入'}
                    </Tag>
                  </div>
                );
              })}
            </div>
            <Typography.Paragraph type="secondary" style={{ marginTop: 12 }}>
              逐平台检索公开网页，并尝试补抓可访问的正文；视频描述、搜索摘要与正文分别标注，不代表完整视频或评论。
            </Typography.Paragraph>
            {sources.map((source) => {
              const capability = capabilitiesQ.data?.sources?.find((row) => row.source === source);
              return <Alert key={source} type="info" showIcon style={{ marginTop: 8 }}
                message={`${SOURCE_LABELS[source]}：${capability?.discovery ? '已配置公开发现入口，正文可用性以本次实测为准' : '自主入口暂不可用，可尝试供应商搜索或提供公开链接'}`}
                description={capability?.limitations?.join('；')} />;
            })}
            <label htmlFor="analysis-seed-links" style={{ display: 'block', marginTop: 20, marginBottom: 8 }}>补充公开链接（可选，每行一条，最多 10 条）</label>
            <Input.TextArea id="analysis-seed-links" value={seedInput} onChange={(event) => setSeedInput(event.target.value)}
              autoSize={{ minRows: 2, maxRows: 5 }} placeholder="文章、公开账号或列表页链接；来源和日期仍需核验" />
            {!seedsValid && <Typography.Text type="danger">请填写最多 10 个不含账号密码的 HTTP/HTTPS 链接。</Typography.Text>}
            <label htmlFor="analysis-delivery-policy" style={{ display: 'block', marginTop: 20, marginBottom: 8 }}>交付方式</label>
            <Select id="analysis-delivery-policy" aria-label="交付方式" value={deliveryPolicy} onChange={setDeliveryPolicy}
              style={{ width: '100%' }} options={[
                { value: 'best_available', label: '自动恢复采集，按实际完成度交付（推荐）' },
                { value: 'full_only', label: '必须完整研判，否则保留资料并标明未完成' },
              ]} />
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
                ...(selectedTemplate ? [{ key: 'template', label: '选择的场景', children: `${selectedTemplate.label}（随本次任务保存并用于专项研判）` }] : []),
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
                { key: 'research-question', label: '关注问题', children: researchQuestion.trim() || '未填写，将结合全部关键词理解同一研究主题' },
                { key: 'delivery-policy', label: '交付方式', children: deliveryPolicy === 'best_available' ? '按实际完成度交付' : '必须完整研判' },
                { key: 'seed-urls', label: '补充链接', children: seedUrls.length ? seedUrls.join('；') : '未填写，将使用系统可用入口发现资料' },
                {
                  key: 'period',
                  label: '分析时间范围',
                  children: selectedPeriod,
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
                  依次执行检索与正文补抓、语义核验、智能研判及报告生成，最长 <b>46 分钟</b>（时间上限，通常会提前结束）；详情页展示实际进度与已用时。
                  <br />
                  本次分析{creditsQ.data?.billing_exempt ? <b>免扣报告额度</b> : <>先扣 <b>1 次报告额度</b>；仅完整研判保留消费，受限研判、资料简报及空结果回执配对回补</>}
                  {creditsQ.data && (
                    <>，{creditsQ.data.billing_exempt ? '当前账号免次数限制，实际余额' : '当前剩余'} <b style={{ color: noCredits ? '#FF2442' : undefined }}>{creditsQ.data.balance}</b> 次</>
                  )}
                  {noCredits && (
                    <div style={{ marginTop: 8 }}>
                      额度不足，<Link to="/plans">去购买套餐 →</Link>
                    </div>
                  )}
                </>
              }
            />
            {entry === 'template' && <Alert type="warning" showIcon style={{ marginTop: 12 }} message="仅创建单次分析"
              description="本次保存场景选择并执行专项研判；周期调度与预警尚未接通。" />}
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
              disabled={!canNext || !validDateRange || keywords.length === 0 || sources.length === 0}
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
