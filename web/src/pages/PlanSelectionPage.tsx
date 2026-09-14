import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert, App, Button, Card, Col, Empty, Modal, Row, Space, Spin, Table, Tag, Typography,
} from 'antd';
import { AlipayCircleFilled, CheckOutlined, ReloadOutlined, WechatFilled } from '@ant-design/icons';
import { QRCodeSVG } from 'qrcode.react';
import {
  createOrder, getCredits, getCatalog, getOrder, getCreditTransactions,
  type CreditTransaction, type Order, type Plan, type SKU,
} from '../api/billing';
import { formatCents, formatDateTime } from '../lib/format';
import { ErrorBlock, LoadingBlock } from '../components/PageState';

/** 套餐权益文案（按 code 静态补充，与后端目录语义一致） */
const PLAN_FEATURES: Record<string, string[]> = {
  free: ['注册赠 1 次体验', '3 维速览报告', 'HTML 报告', '30 天数据保留'],
  lite: ['4 次报告 / 月', '3 维速览（约 2 分钟）', 'HTML + Markdown 报告', '90 天数据保留'],
  pro: ['10 次报告 / 月', '5 维深度研判（BettaFish 内核）', 'PDF 报告下载', 'API 读取接口', '180 天数据保留'],
  enterprise: ['50 次报告 / 月', '5 维深度研判 + 优先队列', '全部报告格式', 'API 读写接口', '365 天数据保留'],
};

const HIGHLIGHT: Record<string, string> = {
  pro: '最受欢迎',
  enterprise: '旗舰之选',
};

const MODE_LABEL: Record<string, string> = {
  quick: '3 维速览',
  full: '5 维深度研判',
};

const CHANNEL_LABEL: Record<string, string> = {
  alipay: '支付宝',
  wechat: '微信支付',
  unionpay: '银联',
};

/** 轮询间隔：订单状态（后端每次轮询顺带查单对账） */
const POLL_MS = 3000;

export default function PlanSelectionPage() {
  const { message } = App.useApp();
  const queryClient = useQueryClient();

  const catalogQ = useQuery({ queryKey: ['billing', 'catalog'], queryFn: getCatalog });
  const creditsQ = useQuery({ queryKey: ['billing', 'credits'], queryFn: getCredits });

  const [channel, setChannel] = useState<string>('');
  const [order, setOrder] = useState<Order | null>(null);
  const [payError, setPayError] = useState<string>('');
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // 渠道默认取第一个可用渠道
  useEffect(() => {
    if (!channel && catalogQ.data?.channels.length) {
      setChannel(catalogQ.data.channels[0]);
    }
  }, [catalogQ.data, channel]);

  // 订单轮询：pending 时每 POLL_MS 查一次（后端触发查单对账）
  useEffect(() => {
    if (pollRef.current) clearInterval(pollRef.current);
    if (!order || order.state !== 'pending') return undefined;
    pollRef.current = setInterval(async () => {
      try {
        const latest = await getOrder(order.id);
        setOrder(latest);
        if (latest.state === 'paid') {
          message.success('支付成功，额度已到账');
          void queryClient.invalidateQueries({ queryKey: ['billing', 'credits'] });
          void queryClient.invalidateQueries({ queryKey: ['billing', 'transactions'] });
          void queryClient.invalidateQueries({ queryKey: ['billing', 'orders'] });
        } else if (latest.state === 'closed' || latest.state === 'refund_needed') {
          setPayError('订单已关闭或需要人工处理，如有疑问请联系客服');
        }
      } catch {
        // 网络抖动不终止轮询
      }
    }, POLL_MS);
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, [order, message, queryClient]);

  async function buy(sku: { code: string; name: string }) {
    setPayError('');
    if (!channel) {
      message.warning('请先选择支付方式');
      return;
    }
    try {
      const o = await createOrder(sku.code, channel);
      setOrder(o);
      void queryClient.invalidateQueries({ queryKey: ['billing', 'orders'] });
    } catch (e) {
      const msg = (e as { response?: { data?: { message?: string } } })?.response?.data?.message;
      setPayError(msg || `发起${CHANNEL_LABEL[channel] ?? '支付'}失败，请稍后再试`);
    }
  }

  const { data: catalog, isLoading, isError, refetch } = catalogQ;
  const purchasable = (catalog?.plans ?? []).filter((p) => p.credits_per_cycle > 0);

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <div>
          <Typography.Title level={4} style={{ margin: 0 }}>
            套餐与额度
          </Typography.Title>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            按次计费 · 每次分析 = 1 次报告额度 · 公测期额度永久有效
          </Typography.Text>
        </div>
        <Space>
          {creditsQ.data && (
            <Tag color="#FF2442" style={{ fontSize: 14, padding: '4px 12px' }}>
              剩余额度：{creditsQ.data.balance} 次
            </Tag>
          )}
          <Button icon={<ReloadOutlined />} onClick={() => { void refetch(); void creditsQ.refetch(); }}>
            刷新
          </Button>
        </Space>
      </div>

      {isError && (
        <Card style={{ borderRadius: 16, marginBottom: 16 }}>
          <ErrorBlock description="套餐列表加载失败" onRetry={() => void refetch()} />
        </Card>
      )}
      {isLoading && (
        <Card style={{ borderRadius: 16, marginBottom: 16 }}>
          <LoadingBlock rows={6} />
        </Card>
      )}

      {catalog && catalog.channels.length === 0 && (
        <Alert
          type="info"
          showIcon
          style={{ borderRadius: 12, marginBottom: 16 }}
          message="在线支付渠道尚未配置"
          description="管理员在「管理后台 → 数据源配置」填写支付宝/微信/银联商户参数并启用后，这里即可在线购买。"
        />
      )}

      <Row gutter={[16, 16]}>
        {purchasable.map((p: Plan) => {
          const recommended = Boolean(HIGHLIGHT[p.code]);
          const isCurrent = p.code === creditsQ.data?.plan_code;
          return (
            <Col xs={24} sm={12} xl={8} key={p.code}>
              <Card
                style={{
                  borderRadius: 16, height: '100%', display: 'flex', flexDirection: 'column',
                  borderColor: recommended ? '#FF2442' : undefined,
                }}
                styles={{ body: { flex: 1, display: 'flex', flexDirection: 'column' } }}
              >
                <div style={{ minHeight: 24, marginBottom: 4 }}>
                  {isCurrent && <Tag color="#FF2442">当前套餐</Tag>}
                  {recommended && <Tag color="rgba(255,125,3,0.12)">{HIGHLIGHT[p.code]}</Tag>}
                </div>
                <Typography.Title level={5} style={{ margin: 0, fontSize: 18 }}>{p.name}</Typography.Title>
                <div style={{ margin: '12px 0 4px' }}>
                  <span style={{ fontSize: 30, fontWeight: 600, color: '#FF2442', fontVariantNumeric: 'tabular-nums' }}>
                    {formatCents(p.price_monthly_cny, 0)}
                  </span>
                  <span style={{ color: 'rgba(0,0,0,0.45)', fontSize: 13 }}> / 月</span>
                </div>
                <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                  含 {p.credits_per_cycle} 次报告 · {MODE_LABEL[p.analysis_mode] ?? p.analysis_mode}
                </Typography.Text>
                <ul style={{ listStyle: 'none', padding: 0, margin: '12px 0 0', flex: 1 }}>
                  {(PLAN_FEATURES[p.code] ?? []).map((f) => (
                    <li key={f} style={{ display: 'flex', gap: 8, alignItems: 'center', color: 'rgba(0,0,0,0.62)', fontSize: 14, marginBottom: 8 }}>
                      <CheckOutlined style={{ color: '#02b940', fontSize: 12 }} />
                      {f}
                    </li>
                  ))}
                </ul>
                <Button
                  type={recommended ? 'primary' : 'default'}
                  block
                  size="large"
                  style={{ marginTop: 16 }}
                  onClick={() => buy({ code: p.code, name: p.name })}
                >
                  立即购买
                </Button>
              </Card>
            </Col>
          );
        })}
      </Row>

      {catalog && catalog.addons.length > 0 && (
        <Card style={{ borderRadius: 16, marginTop: 16 }} title="额度加购">
          <Space wrap>
            {catalog.addons.map((sku: SKU) => (
              <Button key={sku.code} size="large" onClick={() => buy(sku)}>
                {sku.name} · {sku.credits} 次 {formatCents(sku.price_cents, 0)}
              </Button>
            ))}
            <Typography.Text type="secondary" style={{ fontSize: 13 }}>
              适合临时超出套餐额度；买套餐单价更划算。
            </Typography.Text>
          </Space>
        </Card>
      )}

      {/* 支付弹窗 */}
      <Modal
        open={Boolean(order)}
        onCancel={() => setOrder(null)}
        footer={null}
        width={420}
        centered
        title={`订单支付 · ${order ? formatCents(order.amount_cents) : ''}`}
      >
        {order && order.state === 'pending' && (
          <div style={{ textAlign: 'center', padding: '8px 0' }}>
            <Space direction="vertical" size={12} style={{ width: '100%' }}>
              {order.channel === 'unionpay' ? (
                <Alert
                  type="info"
                  message="银联收银台"
                  description={
                    <Space direction="vertical">
                      <span>点击下方按钮打开银联收银台完成支付（支持扫码/云闪付）。</span>
                      <Button
                        type="primary"
                        onClick={() => window.open(
                          `/api/v1/billing/orders/${order.id}/paypage?token=${localStorage.getItem('access_token')}`,
                          '_blank',
                        )}
                      >
                        打开银联收银台
                      </Button>
                    </Space>
                  }
                />
              ) : (
                <>
                  <Spin />
                  <QRCodeSVG value={order.qr_code_url ?? ''} size={220} />
                  <Space>
                    {order.channel === 'alipay' && <AlipayCircleFilled style={{ color: '#1677ff', fontSize: 20 }} />}
                    {order.channel === 'wechat' && <WechatFilled style={{ color: '#02b940', fontSize: 20 }} />}
                    <span>请用{CHANNEL_LABEL[order.channel] ?? '手机'}扫码支付</span>
                  </Space>
                </>
              )}
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                订单号 {order.id} · 15 分钟内有效 · 支付后自动到账
              </Typography.Text>
            </Space>
          </div>
        )}
        {order && order.state === 'paid' && (
          <div style={{ textAlign: 'center', padding: '24px 0' }}>
            <CheckOutlined style={{ color: '#02b940', fontSize: 48 }} />
            <Typography.Title level={4} style={{ marginTop: 12 }}>支付成功</Typography.Title>
            <Typography.Text type="secondary">
              {order.credits} 次报告额度已到账
            </Typography.Text>
            <div style={{ marginTop: 16 }}>
              <Button type="primary" onClick={() => setOrder(null)}>完成</Button>
            </div>
          </div>
        )}
        {payError && <Alert type="error" message={payError} style={{ marginTop: 12 }} showIcon />}
      </Modal>

      {!catalog && !isLoading && <Empty description="暂无可购套餐" />}

      {/* 消费面板：额度流水 */}
      <Card style={{ borderRadius: 16, marginTop: 16 }} title="额度流水">
        <TransactionsTable />
      </Card>
    </div>
  );
}

const REASON_LABEL: Record<string, string> = {
  trial: '注册赠送',
  grant: '运营赠送',
  purchase: '购买入账',
  consume: '分析消费',
  refund: '失败回补',
};

function TransactionsTable() {
  const txQ = useQuery({ queryKey: ['billing', 'transactions'], queryFn: getCreditTransactions });

  if (txQ.isLoading) return <LoadingBlock rows={3} />;
  if (txQ.isError) return <ErrorBlock description="流水加载失败" onRetry={() => void txQ.refetch()} />;

  return (
    <Table<CreditTransaction>
      size="small"
      rowKey="id"
      dataSource={txQ.data ?? []}
      pagination={{ pageSize: 8, hideOnSinglePage: true }}
      columns={[
        {
          title: '时间',
          dataIndex: 'created_at',
          width: 170,
          render: (v: string) => formatDateTime(v),
        },
        {
          title: '类型',
          dataIndex: 'reason',
          width: 110,
          render: (r: string) => REASON_LABEL[r] ?? r,
        },
        {
          title: '变动',
          dataIndex: 'delta',
          width: 90,
          align: 'right',
          render: (d: number) => (
            <span style={{ color: d > 0 ? '#02b940' : '#FF2442', fontVariantNumeric: 'tabular-nums' }}>
              {d > 0 ? `+${d}` : d}
            </span>
          ),
        },
        { title: '结余', dataIndex: 'balance_after', width: 80, align: 'right' },
        { title: '关联分析', dataIndex: 'analysis_id', ellipsis: true },
      ]}
    />
  );
}
