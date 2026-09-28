import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Card, Col, Input, Row, Select, Space, Typography } from 'antd';
import { Link } from 'react-router-dom';
import {
  CONFIG_KEYS,
  createMonitorPlan,
  getMonitorPlan,
  listMonitorPlans,
  listTemplates,
  patchMonitorPlan,
  previewMonitorPlan,
  type Candidate,
  type ConfigKey,
  type DraftConfig,
  type MonitorPlan,
  type Template,
} from '../api/monitorPlans';
import { ANALYSIS_TYPES, type AnalysisType } from '../lib/constants';

const inputFields: Record<string, string> = {
  brand_daily: 'brand_name', product_launch: 'product_name', quality_complaint: 'product_name',
  competitor_update: 'competitor_name', crisis: 'scenario', campaign_review: 'scenario',
};
const configLabels: Record<ConfigKey, string> = {
  keywords: '关键词', exclude_words: '排除词', sources: '数据源', monitoring_cycle: '监测周期',
  risk_tags: '风险标签', alert_rules: '预警规则', report_template: '报告模板',
};
const notConnected: ConfigKey[] = ['monitoring_cycle', 'risk_tags', 'alert_rules', 'report_template'];

function errorMessage(error: unknown): string {
  const response = error as { response?: { status?: number; data?: { message?: string } }; message?: string };
  return response.response?.data?.message ?? response.message ?? '请求失败';
}

function safeConfig(config: DraftConfig): DraftConfig {
  if (!config || !CONFIG_KEYS.every((key) => {
    const item = config[key];
    return item && (item.state === 'proposed' || item.state === 'unavailable') && typeof item.source === 'string' && item.source.length > 0;
  })) throw new Error('配置不完整或包含不支持的状态，请重新预览');
  const result = { ...config };
  for (const key of notConnected) {
    result[key] = { state: 'unavailable', source: config[key].source, reason: config[key].state === 'unavailable' ? config[key].reason : '执行器未接通，草稿不代表已启用' };
  }
  return result;
}

function words(value: string): string[] {
  return [...new Set(value.split(/[,，\n]/).map((item) => item.trim()).filter(Boolean))];
}

function candidateText(candidate: Candidate): string {
  return Array.isArray(candidate.value) ? candidate.value.join(', ') : '';
}

export default function MonitorPlansPage() {
  const [templates, setTemplates] = useState<Template[]>([]);
  const [plans, setPlans] = useState<MonitorPlan[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [screen, setScreen] = useState<'list' | 'edit'>('list');
  const [template, setTemplate] = useState<Template | null>(null);
  const [draft, setDraft] = useState<MonitorPlan | null>(null);
  const [name, setName] = useState('');
  const [subject, setSubject] = useState('');
  const [analysisType, setAnalysisType] = useState<AnalysisType>('brand');
  const [config, setConfig] = useState<DraftConfig | null>(null);
  const [wordInputs, setWordInputs] = useState({ keywords: '', exclude_words: '' });
  const [sourceOptions, setSourceOptions] = useState<string[]>([]);
  const [warning, setWarning] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const edits = useRef<DraftConfig | null>(null);

  const refresh = async () => {
    try {
      const result = await listMonitorPlans();
      if (!Array.isArray(result.plans)) throw new Error('草稿列表无效');
      setPlans(result.plans);
      setHasMore(result.plans.length === 20);
    } catch (cause) {
      setError(`读取草稿失败：${errorMessage(cause)}`);
    }
  };

  const loadMore = async () => {
    setBusy(true); setError('');
    try {
      const result = await listMonitorPlans(plans.length);
      if (!Array.isArray(result.plans)) throw new Error('草稿列表无效');
      setPlans((previous) => [...previous, ...result.plans.filter((item) => !previous.some((seen) => seen.plan_id === item.plan_id))]);
      setHasMore(result.plans.length === 20);
    } catch (cause) { setError(`加载草稿失败：${errorMessage(cause)}`); }
    finally { setBusy(false); }
  };

  useEffect(() => {
    void listTemplates().then((items) => {
      if (!Array.isArray(items)) throw new Error('模板目录无效');
      setTemplates(items);
    }).catch((cause: unknown) => setError(`读取模板失败：${errorMessage(cause)}`));
    void refresh();
  }, []);

  const invalidate = () => {
    if (config) edits.current = config;
    setConfig(null);
    setWarning('输入或视角已变更，请重新预览后保存；未接通配置不会自动启用。');
  };

  const newDraft = () => {
    setScreen('edit'); setDraft(null); setTemplate(null); setName(''); setSubject('');
    setConfig(null); setWordInputs({ keywords: '', exclude_words: '' }); setSourceOptions([]); setError(''); setWarning(''); edits.current = null;
  };

  const selectTemplate = (selected: Template) => {
    if (!inputFields[selected.template_id] || !ANALYSIS_TYPES.some((item) => item.value === selected.default_analysis_type)) {
      setError('模板不受当前页面支持，不能创建草稿');
      return;
    }
    setTemplate(selected); setName(`${selected.name}草稿`); setSubject('');
    setAnalysisType(selected.default_analysis_type); setConfig(null); setWordInputs({ keywords: '', exclude_words: '' }); setSourceOptions([]); setError(''); setWarning(''); edits.current = null;
  };

  const openDraft = async (id: string) => {
    setBusy(true); setError('');
    try {
      const result = await getMonitorPlan(id);
      if (result.state !== 'draft' || !inputFields[result.template_id]) throw new Error('此方案不支持草稿编辑');
      const loaded = safeConfig(result.config);
      setDraft(result);
      setTemplate({ template_id: result.template_id, template_version: result.template_version, name: templates.find((item) => item.template_id === result.template_id)?.name ?? result.name, default_analysis_type: result.analysis_type });
      setName(result.name); setSubject(result.inputs[inputFields[result.template_id]] ?? '');
      setAnalysisType(result.analysis_type); setConfig(loaded); edits.current = null;
      setWordInputs({ keywords: candidateText(loaded.keywords), exclude_words: candidateText(loaded.exclude_words) });
      setSourceOptions(Array.isArray(loaded.sources.value) ? loaded.sources.value as string[] : []);
      setWarning('已重开草稿；仍未启用运行、调度、通知或报告执行。');
      setScreen('edit');
    } catch (cause) { setError(`打开草稿失败：${errorMessage(cause)}`); }
    finally { setBusy(false); }
  };

  const preview = async () => {
    if (!template || !subject.trim() || subject.trim().length > 100) { setError('请先选择模板并输入 1–100 字的关注主体'); return; }
    setConfig(null); setBusy(true); setError('');
    try {
      const result = await previewMonitorPlan({ template_id: template.template_id, template_version: template.template_version, analysis_type: analysisType, inputs: { [inputFields[template.template_id]]: subject.trim() } });
      const verified = safeConfig(result.config);
      const prior = edits.current;
      if (prior) {
        for (const key of CONFIG_KEYS) {
          if (verified[key].state === 'proposed' && prior[key].state === 'proposed' && prior[key].source === 'user_edit' && key !== 'sources') {
            verified[key] = prior[key];
          }
        }
      }
      setConfig(verified); setAnalysisType(result.analysis_type_resolution.analysis_type);
      setWordInputs({ keywords: candidateText(verified.keywords), exclude_words: candidateText(verified.exclude_words) });
      setSourceOptions(Array.isArray(verified.sources.value) ? verified.sources.value as string[] : []);
      setWarning(result.warnings?.join('；') ?? ''); edits.current = null;
    } catch (cause) { setError(`预览失败：${errorMessage(cause)}`); }
    finally { setBusy(false); }
  };

  const editWords = (key: 'keywords' | 'exclude_words', value: string) => {
    setWordInputs((previous) => ({ ...previous, [key]: value }));
    setConfig((previous) => previous ? { ...previous, [key]: { ...previous[key], value: words(value), source: 'user_edit' } } : previous);
  };

  const save = async () => {
    if (!template || !config || name.trim().length < 2 || !subject.trim()) return;
    if (!Array.isArray(config.keywords.value) || config.keywords.value.length === 0) { setError('请至少保留一个关键词'); return; }
    setBusy(true); setError('');
    try {
      const body = { name: name.trim(), analysis_type: analysisType, inputs: { [inputFields[template.template_id]]: subject.trim() }, config: safeConfig(config), state: 'draft' as const };
      const result = draft
        ? await patchMonitorPlan(draft.plan_id, draft.revision, body)
        : await createMonitorPlan({ ...body, template_id: template.template_id, template_version: template.template_version });
      setDraft(result); setConfig(safeConfig(result.config));
      setWarning('草稿已保存；保存不等于已启用，也不会创建分析任务或扣除报告额度。');
      await refresh();
    } catch (cause) {
      const status = (cause as { response?: { status?: number } }).response?.status;
      setError(status === 409 ? '版本冲突：草稿已被修改，请返回列表重新打开后检查差异。' : `保存失败：${errorMessage(cause)}`);
    } finally { setBusy(false); }
  };

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <Typography.Title level={4}>监测方案草稿</Typography.Title>
      <Alert type="warning" showIcon style={{ marginBottom: 16 }} message="仅预览及保存草稿，暂不能运行或发送"
        description={<>来源覆盖以服务端授权和套餐检查为准；周期执行、风险标签、预警与报告模板尚未接通。单次分析请使用 <Link to="/analyses/new">新建分析</Link>。</>} />
      {error && <Alert type="error" showIcon style={{ marginBottom: 16 }} message={error} />}
      {warning && <Alert type="info" showIcon style={{ marginBottom: 16 }} message={warning} />}
      {screen === 'list' ? (
        <Card title="已保存草稿" extra={<Button type="primary" onClick={newDraft}>新建草稿</Button>}>
          {plans.length === 0 && <Typography.Text type="secondary">暂无草稿。保存前不会创建方案。</Typography.Text>}
          {plans.map((plan) => (
            <Card size="small" key={plan.plan_id} style={{ marginBottom: 12 }}>
              <Space wrap>
                <Typography.Text strong>{plan.name}</Typography.Text>
                <Typography.Text type="secondary">草稿 · 修订 {plan.revision}</Typography.Text>
                <Button onClick={() => void openDraft(plan.plan_id)} loading={busy}>打开草稿</Button>
              </Space>
            </Card>
          ))}
          {hasMore && <Button loading={busy} onClick={() => void loadMore()}>加载更多</Button>}
        </Card>
      ) : (
        <Card title={draft ? `编辑草稿 · 修订 ${draft.revision}` : '新建草稿'} extra={<Button onClick={() => { setScreen('list'); setError(''); setWarning(''); void refresh(); }}>返回草稿列表</Button>}>
          {!draft && <><Typography.Title level={5}>选择场景模板</Typography.Title><Row gutter={[12, 12]} style={{ marginBottom: 20 }}>
            {templates.map((item) => <Col key={item.template_id} xs={24} sm={12} md={8}>
              <Button block type={template?.template_id === item.template_id ? 'primary' : 'default'} onClick={() => selectTemplate(item)}>{item.name}</Button>
            </Col>)}
          </Row></>}
          {template && <>
            <Space direction="vertical" style={{ width: '100%' }} size="middle">
              <label>草稿名称<Input aria-label="草稿名称" maxLength={50} value={name} onChange={(event) => setName(event.target.value)} /></label>
              <label>关注主体<Input aria-label="关注主体" maxLength={100} value={subject} onChange={(event) => { setSubject(event.target.value); invalidate(); }} placeholder="输入品牌或场景" /></label>
              <label>分析视角（可修改）<Select aria-label="分析视角" style={{ width: '100%' }} value={analysisType} options={ANALYSIS_TYPES.map((item) => ({ label: item.label, value: item.value }))} onChange={(value: AnalysisType) => { setAnalysisType(value); invalidate(); }} /></label>
              <Button onClick={() => void preview()} loading={busy}>预览候选配置</Button>
            </Space>
            {config && <div style={{ marginTop: 20 }}>
              <Typography.Title level={5}>七项配置 · 仅供草稿编辑</Typography.Title>
              {CONFIG_KEYS.map((key) => {
                const candidate = config[key];
                const editable = candidate.state === 'proposed';
                return <Card key={key} size="small" style={{ marginBottom: 10 }}>
                  <Typography.Text strong>{configLabels[key]}：{editable ? '候选（未启用）' : '不可用'}</Typography.Text>
                  {candidate.reason && <div><Typography.Text type="danger">{candidate.reason}</Typography.Text></div>}
                  {editable && (key === 'keywords' || key === 'exclude_words') && <Input aria-label={configLabels[key]} value={wordInputs[key]} onChange={(event) => editWords(key, event.target.value)} placeholder="用逗号分隔，修改后需保存" style={{ marginTop: 8 }} />}
                  {editable && key === 'sources' && <Select aria-label="数据源候选" mode="multiple" style={{ width: '100%', marginTop: 8 }} value={Array.isArray(candidate.value) ? candidate.value as string[] : []} options={sourceOptions.map((value) => ({ label: value, value }))} onChange={(values: string[]) => setConfig((previous) => previous ? { ...previous, sources: { ...previous.sources, value: values, source: 'user_edit' } } : previous)} />}
                </Card>;
              })}
              <Button type="primary" onClick={() => void save()} loading={busy} disabled={!config || name.trim().length < 2 || !subject.trim()}>{draft ? '保存修改' : '保存草稿'}</Button>
            </div>}
            {!config && <Button style={{ marginTop: 20 }} disabled>保存草稿</Button>}
          </>}
        </Card>
      )}
    </div>
  );
}
