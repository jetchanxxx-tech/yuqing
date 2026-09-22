# 热榜聚合（F21）

实时聚合微博、B站、知乎、新浪科技、36氪等平台热榜，为用户提供舆情监测起点与分析预填功能。

## 🎯 功能定位

**不是**：舆情监测主功能（那是分析任务）  
**是**：快速发现热点 → 一键预填关键词 → 创建分析任务

**核心价值**：
- ✅ 零操作成本获取全网热点
- ✅ 从热榜标题直接跳转创建分析
- ✅ 5 分钟自动刷新，实时性强
- ✅ 纯快照零存储，无计费消耗

## 📊 支持平台（V2：5 平台）

| 平台 | 数据源 | 条数 | 准入标准 |
|------|--------|------|----------|
| **微博** | RSSHub /weibo/search/hot | 20 | ✅ 免 Cookie，实测稳定 |
| **B站** | RSSHub /bilibili/hot-search | 10 | ✅ 免 Cookie，实测稳定 |
| **知乎** | RSSHub /zhihu/hot | 20 | ✅ 免 Cookie，实测稳定 |
| **新浪科技** | RSSHub /sina/rollnews | 20 | ✅ 免 Cookie，2026-09-23 实测 3 连发 |
| **36氪** | RSSHub /36kr/newsflashes | 20 | ✅ 免 Cookie，实测 3 连发全 200 |
| 抖音 | RSSHub /douyin/hot | - | ❌ 需反爬，暂缓 |
| 小红书 | RSSHub /xiaohongshu/user/... | - | ❌ 需 Cookie+反爬，暂缓 |
| IT之家/虎扑 | RSSHub /ithome/rank 等 | - | ❌ 实测 503：路由在但上游失败 |

**准入线**：
1. 免 Cookie / ≥20 条
2. 响应时间 <15s
3. **生产 RSSHub 实测连续 3 次可用**（2026-09-23 增补）

**跨平台搜索**：前端 `/trends` 页提供关键字搜索框（纯客户端过滤），输入后聚合展示所有平台命中条目（带来源平台标记与命中数统计）。

## 🏗️ 架构设计

```
┌──────────────────────────────────────────────────┐
│  前端 /trends 页面                                │
│  ├─ Tab 切换（微博/B站/知乎）                     │
│  ├─ 实时数据（三态标记）                          │
│  └─ 点击标题 → 预填关键词 → 跳转 /analyses/new   │
└─────────────────┬────────────────────────────────┘
                  │ GET /api/v1/trends
┌─────────────────▼────────────────────────────────┐
│  Go server 内存缓存（CachedTrendsProvider）       │
│  ├─ 5 分钟 ticker 全平台刷新                      │
│  ├─ 失败保留 last-good（三态：ok/stale/error）   │
│  ├─ 登录即可看（无租户维度）                      │
│  └─ 零落库（快照过了就过了）                      │
└─────────────────┬────────────────────────────────┘
                  │ HTTP
┌─────────────────▼────────────────────────────────┐
│  RSSHub (:1200)                                   │
│  ├─ systemd unit（只绑 127.0.0.1）               │
│  ├─ Playwright Chromium（npmmirror CDN）         │
│  ├─ /weibo/hot/search                            │
│  ├─ /bilibili/hot-search                         │
│  └─ /zhihu/hot                                    │
└──────────────────────────────────────────────────┘
```

### 零存储设计

**不落库原因**：
1. 热榜快照时效性强（5 分钟即过期）
2. 历史数据无分析价值（用户只关心"现在"）
3. 避免存储成本与清理任务

**内存缓存策略**：
```go
type CachedTrendsProvider struct {
    mu    sync.RWMutex
    cache map[string]*PlatformSnapshot  // platform → snapshot
}

type PlatformSnapshot struct {
    Status    string       // "ok" / "stale" / "error"
    Items     []TrendItem
    UpdatedAt time.Time
    Error     string       // status=error 时说明原因
}
```

### 三态数据模型

| 状态 | 含义 | 前端显示 |
|------|------|----------|
| `ok` | 本次刷新成功 | 实时数据 + 刷新时间 |
| `stale` | 本次失败，显示上次成功快照 | 数据 + ⚠️ 更新时间（可能过时） |
| `error` | 从未成功过 / last-good 已丢失 | ❌ 该平台暂不可用 |

**RSSHub 不可达时仍返回 200**：
- 数据源状态 ≠ 服务器故障
- 前端正常渲染其他可用平台
- 仅当 RSSHub 未配置（`rsshub_base` 为空）才返回 503

## 🔄 刷新机制

### 定时刷新

```go
func (p *CachedTrendsProvider) Start() {
    ticker := time.NewTicker(5 * time.Minute)
    go func() {
        p.refreshAll()  // 启动时立即刷新一次
        for range ticker.C {
            p.refreshAll()
        }
    }()
}

func (p *CachedTrendsProvider) refreshAll() {
    platforms := []string{"微博", "B站", "知乎"}
    for _, name := range platforms {
        items, err := p.fetcher.Fetch(name)
        p.mu.Lock()
        if err != nil {
            // 失败：保留 last-good，标记 stale
            if old := p.cache[name]; old != nil && old.Status == "ok" {
                old.Status = "stale"
            } else {
                p.cache[name] = &PlatformSnapshot{Status: "error", Error: err.Error()}
            }
        } else {
            // 成功：覆盖为 ok
            p.cache[name] = &PlatformSnapshot{
                Status: "ok", Items: items, UpdatedAt: time.Now(),
            }
        }
        p.mu.Unlock()
    }
}
```

### 并发安全

- `sync.RWMutex` 保护缓存读写
- Ticker 串行刷新（避免重叠）
- GET 请求并发读（RLock）

## 🎨 前端实现

### Tab 切换

```tsx
// web/src/pages/TrendsPage.tsx
const tabs = [
  { key: 'weibo', label: '微博热搜', icon: <WeiboOutlined /> },
  { key: 'bilibili', label: 'B站热搜', icon: <YoutubeOutlined /> },
  { key: 'zhihu', label: '知乎热榜', icon: <QuestionCircleOutlined /> },
];

<Tabs activeKey={activeTab} onChange={setActiveTab}>
  {tabs.map(tab => (
    <TabPane tab={tab.label} key={tab.key}>
      <TrendList platform={tab.key} data={trendsData[tab.key]} />
    </TabPane>
  ))}
</Tabs>
```

### 三态渲染

```tsx
function TrendList({ platform, data }) {
  if (data.status === 'error') {
    return <Empty description={`❌ ${data.error || '该平台暂不可用'}`} />;
  }
  
  return (
    <>
      {data.status === 'stale' && (
        <Alert
          type="warning"
          message="数据可能过时"
          description={`上次更新：${dayjs(data.updated_at).fromNow()}`}
        />
      )}
      <List
        dataSource={data.items}
        renderItem={(item, index) => (
          <List.Item
            actions={[
              <Button 
                type="link"
                onClick={() => createAnalysisFromTrend(item.title)}
              >
                立即分析
              </Button>
            ]}
          >
            <List.Item.Meta
              avatar={<Badge count={item.rank} />}
              title={<a href={item.url} target="_blank">{item.title}</a>}
              description={item.hot}
            />
          </List.Item>
        )}
      />
    </>
  );
}
```

### 预填关键词跳转

```tsx
function createAnalysisFromTrend(trendTitle: string) {
  // 清洗标题：去除 emoji、#话题#、多余空格
  const keywords = trendTitle
    .replace(/#/g, '')
    .replace(/[\u{1F600}-\u{1F64F}]/gu, '')
    .trim();
  
  // 跳转到新建分析页，预填关键词
  navigate('/analyses/new', {
    state: { keywords, source: 'trends' }
  });
}
```

## 🚀 部署配置

### RSSHub 安装（CentOS Stream 9）

```bash
# 1. 下载预构建包（本地 npm run build 后上传）
scp rsshub-dist.tar.gz root@yuqing2:/opt/
ssh root@yuqing2
cd /opt
tar xzf rsshub-dist.tar.gz -C /opt/rsshub

# 2. 安装 Playwright Chromium
cd /opt/rsshub
export PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=0
export PLAYWRIGHT_CHROMIUM_DOWNLOAD_HOST=https://registry.npmmirror.com/-/binary/playwright
npx playwright install chromium

# ⚠️ 手动安装系统依赖（--with-deps 在 CentOS 9 会卡死）
sudo dnf install -y \
  atk cups-libs gtk3 libXcomposite libXdamage libXrandr \
  pango alsa-lib nss libdrm mesa-libgbm

# 3. 创建日志目录
sudo mkdir -p /opt/rsshub/logs

# 4. 创建 systemd unit
sudo tee /etc/systemd/system/yuqing-rsshub.service <<'EOF'
[Unit]
Description=RSSHub for Yuqing Trends
After=network.target

[Service]
Type=simple
User=yuqing
WorkingDirectory=/opt/rsshub
Environment="NODE_ENV=production"
Environment="LISTEN_INADDR_ANY=0"
Environment="PORT=1200"
ExecStart=/usr/local/node/bin/node lib/index.js
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# 5. 启动
sudo systemctl daemon-reload
sudo systemctl enable yuqing-rsshub
sudo systemctl start yuqing-rsshub

# 6. 验证
curl http://127.0.0.1:1200/weibo/hot/search
curl http://127.0.0.1:1200/bilibili/hot-search
curl http://127.0.0.1:1200/zhihu/hot
```

### Go server 配置

```yaml
# platform/config.yaml
trends:
  rsshub_base: "http://127.0.0.1:1200"  # RSSHub 端点
  refresh_interval: "5m"                 # 刷新间隔
```

**环境变量方式**：
```bash
export YUQING_RSSHUB_BASE=http://127.0.0.1:1200
export YUQING_TRENDS_REFRESH_INTERVAL=5m
```

### 前端路由

```tsx
// web/src/App.tsx
<Route path="/trends" element={<TrendsPage />} />
```

导航栏新增入口：
```tsx
<Menu.Item key="trends" icon={<FireOutlined />}>
  <Link to="/trends">热榜</Link>
</Menu.Item>
```

## 📊 API 参考

### GET /api/v1/trends

**认证**：需要登录（JWT 或 ApiKey）  
**权限**：任意角色（无租户隔离）

**请求示例**：
```bash
GET /api/v1/trends
Authorization: Bearer <token>
```

**响应示例**：
```json
{
  "platforms": [
    {
      "name": "微博",
      "status": "ok",
      "updated_at": "2026-09-22T08:00:00Z",
      "items": [
        {
          "rank": 1,
          "title": "中秋国庆假期安排出炉",
          "url": "https://s.weibo.com/weibo?q=%23中秋国庆假期%23",
          "hot": "486.2万"
        },
        {
          "rank": 2,
          "title": "男子称买到注胶大闸蟹",
          "url": "https://s.weibo.com/weibo?q=%23注胶大闸蟹%23",
          "hot": "321.5万"
        }
      ]
    },
    {
      "name": "B站",
      "status": "stale",
      "updated_at": "2026-09-22T07:45:00Z",
      "items": [
        {
          "rank": 1,
          "title": "【官方】某游戏新版本前瞻",
          "url": "https://www.bilibili.com/video/BV1xx411c7mD",
          "hot": "1234567"
        }
      ],
      "error": ""
    },
    {
      "name": "知乎",
      "status": "error",
      "items": [],
      "error": "该平台暂不可用"
    }
  ]
}
```

**状态码**：
- `200` - 成功（即使部分平台 error）
- `503 TRENDS_UNAVAILABLE` - RSSHub 未配置（`rsshub_base` 为空）

## 🧪 测试

### 单元测试

```bash
cd platform
go test ./internal/business/trends/ -v
```

覆盖：
- 三态切换逻辑
- 并发读写安全
- last-good 保留机制

### E2E 测试

```bash
cd web
npx playwright test e2e/trends.spec.ts
```

场景：
- Tab 切换显示不同平台
- ok 状态正常渲染列表
- stale 状态显示警告提示
- error 状态显示空状态
- 点击"立即分析"预填关键词

### 生产验收

```bash
# 1. 检查 RSSHub 进程
systemctl status yuqing-rsshub
journalctl -u yuqing-rsshub -n 50

# 2. 测试各平台端点
curl http://127.0.0.1:1200/weibo/hot/search | jq '.item | length'
curl http://127.0.0.1:1200/bilibili/hot-search | jq '.item | length'
curl http://127.0.0.1:1200/zhihu/hot | jq '.item | length'

# 3. 测试 Go API
curl https://yuqing2.pangu-cloud.com/api/v1/trends \
  -H "Authorization: Bearer <token>" | jq '.platforms[].name'

# 4. 前端验收
# 访问 https://yuqing2.pangu-cloud.com/trends
# 确认三个平台数据加载成功
# 确认点击"立即分析"跳转并预填关键词
```

**实测数据**（2026-09-22）：
- ✅ 微博：20 条
- ✅ B站：10 条
- ✅ 知乎：20 条
- 响应时间：<3s

## ⚠️ 已知限制

### 1. 平台覆盖不全

**原因**：抖音/小红书需反爬突破（Chromium + Cookie + 请求签名）

**规划**：
- 抖音：研究 RSSHub 反爬方案 + 压测稳定性
- 小红书：研究登录态维护 + Cookie 池

### 2. 无历史数据

**原因**：零存储设计，快照过期即丢失

**替代方案**：
- 用户关注热点 → 立即创建分析任务 → 采集结果永久保存
- 历史热榜需求不强（用户只关心"现在热什么"）

### 3. 5 分钟延迟

**原因**：平衡实时性与 RSSHub 负载

**可调整**：
```yaml
trends:
  refresh_interval: "3m"  # 缩短到 3 分钟
```

**不建议 <3 分钟**：RSSHub 抓取本身需 2-5s，过高频率可能触发平台限流。

### 4. 单机瓶颈

**当前架构**：内存缓存 + 单进程刷新

**多实例部署时**：
- 方案 A：改用 Redis 缓存（跨实例共享）
- 方案 B：仅一台实例负责刷新（Cron Leader Election）
- 方案 C：客户端直连 RSSHub（绕过 Go 层）

## 🎓 最佳实践

### 1. 监控 RSSHub 健康

```bash
# 日志监控（每天 cron）
journalctl -u yuqing-rsshub --since "1 day ago" | grep -i error

# 进程存活检查
systemctl is-active yuqing-rsshub || systemctl restart yuqing-rsshub
```

### 2. Playwright 浏览器更新

```bash
# 每月更新（避免版本过旧导致反爬失效）
cd /opt/rsshub
npx playwright install chromium
```

### 3. 日志轮转

```bash
# /etc/logrotate.d/rsshub
/opt/rsshub/logs/*.log {
    daily
    rotate 7
    compress
    missingok
    notifempty
}
```

### 4. 前端缓存策略

```tsx
// TanStack Query 配置
const { data } = useQuery({
  queryKey: ['trends'],
  queryFn: fetchTrends,
  staleTime: 4 * 60 * 1000,  // 4 分钟（略短于后端 5 分钟）
  refetchInterval: 5 * 60 * 1000,  // 自动刷新
});
```

## 📖 相关文档

- [[快速开始|Quick-Start]] - 体验热榜功能
- [[架构设计|Architecture]] - 热榜缓存架构
- [[API 参考|API-Reference]] - `/trends` 端点详解
- [[生产部署|Production-Deployment]] - RSSHub 部署清单
