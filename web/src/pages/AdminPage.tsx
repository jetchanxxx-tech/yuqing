import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert, App, Button, Card, Descriptions, Input, Popconfirm,
  Result, Space, Table, Tabs, Tag, Typography,
} from 'antd';
import { ReloadOutlined, StopOutlined, SaveOutlined, ApiOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import {
  listTenants, suspendTenant,
  getAdminSettings, updateAdminSettings,
  type Tenant,
} from '../api/admin';
import { getPlans, type Plan } from '../api/billing';
import { useAuth } from '../stores/auth';
import { formatDateTime } from '../lib/format';
import { ErrorBlock, EmptyBlock, LoadingBlock } from '../components/PageState';

const ADMIN_ROLES = ['platform_admin', 'admin'];

export default function AdminPage() {
  const { principal } = useAuth();

  const isAdmin = !!principal && principal.roles.some((r) => ADMIN_ROLES.includes(r));

  if (!principal) return null;
  if (!isAdmin) {
    return (
      <Card style={{ borderRadius: 16 }}>
        <Result status="403" title="无访问权限" subTitle="管理后台仅对平台管理员开放" />
      </Card>
    );
  }

  return (
    <div>
      <Typography.Title level={4} style={{ marginTop: 0 }}>
        管理后台
      </Typography.Title>
      <Tabs
        defaultActiveKey="tenants"
        items={[
          { key: 'tenants', label: '租户管理', children: <TenantsTab /> },
          { key: 'datasource', label: '数据源配置', children: <DataSourceTab /> },
        ]}
      />
    </div>
  );
}

// ════════════════════════════════════════════════════════════
// 租户管理
// ════════════════════════════════════════════════════════════

function TenantsTab() {
  const { message } = App.useApp();
  const queryClient = useQueryClient();

  const tenantsQ = useQuery({ queryKey: ['admin', 'tenants'], queryFn: listTenants });
  const plansQ = useQuery({ queryKey: ['billing', 'plans'], queryFn: getPlans });

  const suspendQ = useMutation({
    mutationFn: (id: string) => suspendTenant(id),
    onSuccess: () => {
      message.success('操作已提交');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'tenants'] });
    },
  });

  const planName = (code: string, plans: Plan[]) => plans.find((p) => p.code === code)?.name ?? code;

  const columns: ColumnsType<Tenant> = [
    {
      title: '租户',
      dataIndex: 'name',
      key: 'name',
      render: (name: string) => <Typography.Text strong>{name}</Typography.Text>,
    },
    {
      title: '租户 ID', dataIndex: 'id', key: 'id', width: 220,
      render: (v: string) => <Typography.Text code style={{ fontSize: 12 }}>{v}</Typography.Text>,
    },
    {
      title: '套餐', dataIndex: 'plan_code', key: 'plan_code', width: 130,
      render: (code: string) => (plansQ.data ? <Tag>{planName(code, plansQ.data)}</Tag> : code),
    },
    {
      title: '状态', dataIndex: 'status', key: 'status', width: 110,
      render: (s: string) => (
        <Tag color={s === 'active' ? 'success' : s === 'suspended' ? 'warning' : 'default'}>
          {s === 'active' ? '正常' : s === 'suspended' ? '已挂起' : s}
        </Tag>
      ),
    },
    {
      title: '创建时间', dataIndex: 'created_at', key: 'created_at', width: 180,
      render: (v?: string) => formatDateTime(v),
    },
    {
      title: '操作', key: 'action', width: 120,
      render: (_, r) =>
        r.status === 'active' ? (
          <Popconfirm
            title="挂起该租户？"
            description="挂起后租户将无法登录与使用服务"
            okText="挂起"
            okButtonProps={{ danger: true }}
            cancelText="取消"
            onConfirm={() => suspendQ.mutate(r.id)}
          >
            <Button size="small" danger icon={<StopOutlined />}
              loading={suspendQ.isPending && suspendQ.variables === r.id}>
              挂起
            </Button>
          </Popconfirm>
        ) : (
          <Typography.Text type="secondary">—</Typography.Text>
        ),
    },
  ];

  const rows = tenantsQ.data ?? [];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 12 }}>
        <Button icon={<ReloadOutlined />} onClick={() => void tenantsQ.refetch()}
          loading={tenantsQ.isFetching}>
          刷新
        </Button>
      </div>
      {suspendQ.isError && (
        <Alert type="error" showIcon message="挂起操作失败" style={{ marginBottom: 16 }} />
      )}
      <Card style={{ borderRadius: 16 }}>
        {tenantsQ.isLoading ? (
          <LoadingBlock rows={6} />
        ) : tenantsQ.isError ? (
          <ErrorBlock description="租户列表加载失败" onRetry={() => void tenantsQ.refetch()} />
        ) : rows.length === 0 ? (
          <EmptyBlock description="暂无租户" />
        ) : (
          <Table<Tenant>
            rowKey="id"
            columns={columns}
            dataSource={rows}
            pagination={{ pageSize: 10, showTotal: (t) => `共 ${t} 条`, showSizeChanger: false }}
          />
        )}
      </Card>
    </div>
  );
}

// ════════════════════════════════════════════════════════════
// 数据源配置（搜索 / 爬虫供应商 API Key）
// ════════════════════════════════════════════════════════════

/** 掩码显示已配置的 Key，避免完整凭据出现在界面上 */
function maskKey(key?: string): string {
  if (!key) return '未配置';
  if (key.length <= 10) return '••••••';
  return `${key.slice(0, 6)}••••••${key.slice(-4)}`;
}

function DataSourceTab() {
  const { message } = App.useApp();
  const queryClient = useQueryClient();
  const [bochaInput, setBochaInput] = useState('');
  const [llmKeyInput, setLlmKeyInput] = useState('');
  const [llmUrlInput, setLlmUrlInput] = useState('');
  const [llmModelInput, setLlmModelInput] = useState('');

  const settingsQ = useQuery({ queryKey: ['admin', 'settings'], queryFn: getAdminSettings });

  const saveQ = useMutation({
    mutationFn: (patch: Record<string, string>) => updateAdminSettings(patch),
    onSuccess: () => {
      message.success('配置已保存，下一个分析任务即刻生效（无需重启）');
      setBochaInput('');
      setLlmKeyInput('');
      setLlmUrlInput('');
      setLlmModelInput('');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] });
    },
    onError: () => message.error('保存失败，请检查权限或稍后重试'),
  });

  const currentKey = settingsQ.data?.bocha_api_key;
  const configured = !!currentKey;
  const currentLlmKey = settingsQ.data?.llm_api_key;
  const llmConfigured = !!currentLlmKey;
  const llmBaseUrl = settingsQ.data?.llm_base_url ?? '';
  const llmModel = settingsQ.data?.llm_model ?? '';

  const onSave = () => {
    const value = bochaInput.trim();
    if (!value) {
      message.warning('请填写 Bocha API Key');
      return;
    }
    saveQ.mutate({ bocha_api_key: value });
  };

  const onSaveLlm = () => {
    const value = llmKeyInput.trim();
    if (!value) {
      message.warning('请填写 LLM API Key');
      return;
    }
    const patch: Record<string, string> = { llm_api_key: value };
    if (llmUrlInput.trim()) patch.llm_base_url = llmUrlInput.trim();
    if (llmModelInput.trim()) patch.llm_model = llmModelInput.trim();
    saveQ.mutate(patch);
  };

  return (
    <div style={{ maxWidth: 760 }}>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="搜索引擎（Bocha）"
        description={
          <>
            平台的舆情采集依赖 Bocha AI 搜索发现内容，再由 Scrapling 抓取正文。
            未配置 Key 时搜索链路不可用，分析任务的文档数将为 0。
            <br />
            注册免费获取：<Typography.Link href="https://open.bochaai.com" target="_blank">
              open.bochaai.com
            </Typography.Link>
          </>
        }
      />

      <Card style={{ borderRadius: 16 }} title={<Space><ApiOutlined />Bocha AI 搜索</Space>}>
        {settingsQ.isLoading ? (
          <LoadingBlock rows={3} />
        ) : settingsQ.isError ? (
          <ErrorBlock description="配置读取失败" onRetry={() => void settingsQ.refetch()} />
        ) : (
          <>
            <Descriptions column={1} size="small" style={{ marginBottom: 20 }}>
              <Descriptions.Item label="当前状态">
                <Tag color={configured ? 'success' : 'warning'}>
                  {configured ? '已配置' : '未配置'}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="API Key">
                <Typography.Text code>{maskKey(currentKey)}</Typography.Text>
              </Descriptions.Item>
            </Descriptions>

            <Space direction="vertical" style={{ width: '100%' }} size="middle">
              <div>
                <Typography.Text strong>更新 API Key</Typography.Text>
                <Typography.Paragraph type="secondary" style={{ fontSize: 13, margin: '4px 0 8px' }}>
                  填入新的 Key 后保存，即刻生效，无需重启服务。
                </Typography.Paragraph>
                <Input.Password
                  size="large"
                  placeholder="sk-..."
                  value={bochaInput}
                  onChange={(e) => setBochaInput(e.target.value)}
                  onPressEnter={onSave}
                  style={{ maxWidth: 520 }}
                />
              </div>
              <Space>
                <Button
                  type="primary"
                  icon={<SaveOutlined />}
                  loading={saveQ.isPending}
                  onClick={onSave}
                >
                  保存配置
                </Button>
                <Button onClick={() => void settingsQ.refetch()} icon={<ReloadOutlined />}>
                  重新读取
                </Button>
              </Space>
            </Space>
          </>
        )}
      </Card>

      <Card
        style={{ borderRadius: 16, marginTop: 16 }}
        title={<Space><ApiOutlined />LLM 分析（可配置供应商）</Space>}
      >
        {settingsQ.isLoading ? (
          <LoadingBlock rows={3} />
        ) : (
          <>
            <Descriptions column={1} size="small" style={{ marginBottom: 20 }}>
              <Descriptions.Item label="当前状态">
                <Tag color={llmConfigured ? 'success' : 'warning'}>
                  {llmConfigured ? '已配置' : '未配置'}
                </Tag>
              </Descriptions.Item>
              <Descriptions.Item label="API Key">
                <Typography.Text code>{maskKey(currentLlmKey)}</Typography.Text>
              </Descriptions.Item>
              <Descriptions.Item label="端点 / 模型">
                <Typography.Text code>
                  {llmBaseUrl || '（平台默认：智谱 GLM）'} · {llmModel || '（平台默认模型）'}
                </Typography.Text>
              </Descriptions.Item>
            </Descriptions>

            <Space direction="vertical" style={{ width: '100%' }} size="middle">
              <div>
                <Typography.Text strong>更新 API Key</Typography.Text>
                <Typography.Paragraph type="secondary" style={{ fontSize: 13, margin: '4px 0 8px' }}>
                  LLM 驱动情感分析、话题聚类与 AI 研判报告。支持任意 OpenAI 兼容供应商
                  （智谱 / DeepSeek / Kimi 等）。未配置时任务仍完成采集，但分析与报告将降级并标注原因。
                </Typography.Paragraph>
                <Input.Password
                  size="large"
                  placeholder="API Key（如 sk-... 或智谱 key 格式）"
                  value={llmKeyInput}
                  onChange={(e) => setLlmKeyInput(e.target.value)}
                  onPressEnter={onSaveLlm}
                  style={{ maxWidth: 520 }}
                />
              </div>
              <div>
                <Typography.Text strong>端点与模型（可选，留空用平台默认）</Typography.Text>
                <Space direction="vertical" style={{ width: '100%', marginTop: 8 }} size={8}>
                  <Input
                    placeholder="Base URL，如 https://open.bigmodel.cn/api/paas/v4"
                    value={llmUrlInput}
                    onChange={(e) => setLlmUrlInput(e.target.value)}
                    style={{ maxWidth: 520 }}
                  />
                  <Input
                    placeholder="模型名，如 glm-5.3-flash / deepseek-chat"
                    value={llmModelInput}
                    onChange={(e) => setLlmModelInput(e.target.value)}
                    style={{ maxWidth: 520 }}
                  />
                </Space>
              </div>
              <Button
                type="primary"
                icon={<SaveOutlined />}
                loading={saveQ.isPending}
                onClick={onSaveLlm}
              >
                保存配置
              </Button>
            </Space>
          </>
        )}
      </Card>

      <Card style={{ borderRadius: 16, marginTop: 16 }} title="采集链路说明">
        <Typography.Paragraph type="secondary" style={{ fontSize: 13, marginBottom: 6 }}>
          · <b>Bocha API</b>：负责按关键词搜索，返回候选网页列表（标题 / 链接 / 摘要）
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ fontSize: 13, marginBottom: 6 }}>
          · <b>Scrapling</b>：负责抓取候选页面的正文，带自适应选择器与反爬绕过
        </Typography.Paragraph>
        <Typography.Paragraph type="secondary" style={{ fontSize: 13, marginBottom: 0 }}>
          · 若目标站反爬导致正文抓取失败，自动退回用 Bocha 摘要兜底，保证结果不丢
        </Typography.Paragraph>
      </Card>
    </div>
  );
}
