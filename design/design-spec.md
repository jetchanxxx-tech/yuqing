# 微舆 · SaaS 舆情监测平台 — 设计规范 (Design Spec)

> 交付对象：构建代码的智能体 / 前端开发者。本文件是"样式契约"，配合各页面 HTML 一起使用。
> 技术栈参考：React 18 + TypeScript + Ant Design 5 + ECharts 5（真实工程实现时把下方 SVG/CSS 形态映射为 AntD 组件与 ECharts 配置即可）。
> 页面文件：`login.html` `dashboard.html` `analysis-new.html` `analysis-list.html` `analysis-detail.html` `reports.html` `plans.html` `usage.html` `invoices.html` `settings.html` `admin.html`，入口 `index.html`。

---

## 1. 设计定位

- **一句话**：一个让舆情数据像照片一样"好看、好读"的监测工作台——内容（数据、报告）是画面的主角，界面是透明的相框。
- 视觉基调：**daily-ness（日常感）+ 轻 B2B**。白底画布 `#F5F5F5`、白色卡片、12–16px 圆角、近乎零阴影、全药丸按钮、单一品牌红。
- 情感口径：系统情绪 = 第二人称、平实、不喊口号。样例文案均为中文。

## 2. 设计原则

1. **单一强调色**：品牌红 `#FF2442` 每屏至多出现 2 处（典型组合：1 个主 CTA + 1 处激活态/关键数字）。数据图表使用语义色（绿=正面、灰=中性、红=负面）与中性灰。
2. **半透明中性色**：不用离散灰色阶，用 `rgba(48,48,52,0.05/.10/.20)` 表达 hover/禁用/按下；分隔线用 `rgba(0,0,0,0.08)`。
3. **圆角即深度**：卡片 12px（大卡 16px），按钮全药丸（9999px）。默认无阴影；仅 PC 卡片 hover 用 `0 4px 12px rgba(0,0,0,0.08)`，模态用 `0 8px 32px rgba(0,0,0,0.12)`。
4. **内容优先**：图表、报告、文档列表是视觉主角，UI 组件保持安静。
5. **软黑文字**：标题 `rgba(0,0,0,0.80)`，正文 `rgba(0,0,0,0.62)`，辅助 `rgba(0,0,0,0.45)`；纯黑仅用于图表描边以外的极少数场景。
6. **禁忌**：不用紫色/蓝色渐变做品牌主色；品牌红不做渐变；不用 Inter/Roboto 做中文展示字体；卡片不加左侧彩色竖条；不用重阴影（alpha ≤ 0.15、spread ≤ 16px）；不用 3D/抽象网络插画。

## 3. 色彩 Token

> 所有页面在首个 `<style>` 中必须原样粘贴下述 `:root` 令牌块（已绑定设计系统「小红书」）。

```css
:root {
  --bg:           #f5f5f5;
  --surface:      #ffffff;
  --surface-warm: #fafafa;
  --fg:    rgba(0, 0, 0, 0.8);
  --fg-2:  rgba(0, 0, 0, 0.62);
  --muted: rgba(0, 0, 0, 0.45);
  --meta:  rgba(0, 0, 0, 0.27);
  --border:      rgba(0, 0, 0, 0.08);
  --border-soft: rgba(0, 0, 0, 0.05);
  --accent:        #ff2442;
  --accent-on:     #ffffff;
  --accent-hover:  #ff2e4d;
  --accent-active: #e6203a;
  --success: #02b940;
  --warn:    #ff7d03;
  --danger:  #ff2442;
  --font-display: "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans SC", -apple-system, "Helvetica Neue", Helvetica, Arial, sans-serif;
  --font-body:    "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans SC", -apple-system, "Helvetica Neue", Helvetica, Arial, sans-serif;
  --font-mono:    ui-monospace, "SF Mono", "JetBrains Mono", Menlo, Monaco, Consolas, monospace;
  --text-xs:   12px;
  --text-sm:   14px;
  --text-base: 16px;
  --text-lg:   18px;
  --text-xl:   20px;
  --text-2xl:  24px;
  --text-3xl:  28px;
  --text-4xl:  32px;
  --leading-body:     1.5;
  --leading-tight:    1.25;
  --tracking-display: 0;
  --space-1:  4px;
  --space-2:  8px;
  --space-3:  12px;
  --space-4:  16px;
  --space-5:  20px;
  --space-6:  24px;
  --space-8:  32px;
  --space-12: 48px;
  --section-y-desktop: 64px;
  --section-y-tablet:  48px;
  --section-y-phone:   32px;
  --radius-sm:   8px;
  --radius-md:   12px;
  --radius-lg:   16px;
  --radius-pill: 9999px;
  --elev-flat:   none;
  --elev-ring:   0 0 0 1px var(--border);
  --elev-raised: 0 4px 12px rgba(0, 0, 0, 0.08);
  --focus-ring: 0 0 0 3px color-mix(in oklab, var(--accent), transparent 70%);
  --motion-fast:   150ms;
  --motion-base:   200ms;
  --ease-standard: cubic-bezier(0.2, 0, 0, 1);
  --container-max:            1200px;
  --container-gutter-desktop: 24px;
  --container-gutter-tablet:  16px;
  --container-gutter-phone:   12px;
}
```

### 语义色使用边界

| 用途 | Token | 说明 |
|---|---|---|
| 主 CTA / 激活态 / 红点告警 | `--accent` | 每屏 ≤ 2 处 |
| 正面情感 / 成功 / 已完成 | `--success`（#02b940，背景 #EAF8EF） | 仅图表与状态徽标 |
| 负面情感 / 失败 | `--danger`（= `--accent` 红） | 与主红同色，需要区分时用"描边+文案"或前置图标 |
| 中性情感 / 排队中 | `rgba(48,48,52,.10)` 填充 + `--fg-2` 文字 | — |
| 警告 / 预算预警 | `--warn`（#FF7D03，背景 #FFF2E6） | 仅用量、预警徽标 |
| 关注/收藏（品牌黄） | #FDBC5F | 仅收藏类图标（本系统默认不用） |

## 4. 字体与排版

- 全部中文/数字使用 `--font-body`（PingFang SC 栈）。无独立展示字体；拉丁回退链 `-apple-system, "Helvetica Neue", Arial`。
- **数字对齐**：所有计数、金额、Token 用量使用 `font-variant-numeric: tabular-nums;`（对应生产环境可引入等宽数字字体或 `RED Number` 栈）。
- 字重仅 400 / 500 / 600。追踪 0（拉丁小号标签可用 +0.02em，全大写禁用——中文界面不做全大写英文标题）。
- 行高：正文 1.5，标题 1.25–1.35（中文标题**必须** ≥ 1.3，防止行碰撞）。
- 字号天花板 32px（页面级标题）。层级：

| 层 | 字号 | 字重 | 行高 |
|---|---|---|---|
| 页面标题 | 24–32px | 600 | 1.25–1.3 |
| 卡片标题 | 18–20px | 600 | 1.3–1.4 |
| 次级标签 | 16px | 500 | 1.5 |
| 正文 | 14–16px | 400 | 1.5–1.6 |
| 辅助/说明 | 13–14px | 400 | 1.5 |
| 小号/徽标 | 12px | 400–500 | 1.5 |
| 图表标签/角标 | 10–12px | 400 | 1.4 |

## 5. 布局系统

### 5.1 应用外壳（Dashboard 及之后所有工作台页面）

```
┌──────────────────────────────────────────────────┐
│ Sidebar (224px, 固定, 白底)          │ 主区 main     │
│ 品牌块                                      │ margin-left:224px │
│ 新建分析 (主 CTA 药丸, 顶部)                 │ padding: 24px 32px │
│ ── 导航分组 ──                                 │                  │
│   数据面板 / 分析任务 / 报告中心              │ Header: 页面标题   │
│   用量中心 / 账单与订阅 / 团队设置 / 管理后台    │  + 右侧 套餐徽标/铃铛/头像 │
│ 底部 用户卡                                     │                  │
└──────────────────────────────────────────────────┘
```

- **Sidebar**：`width:224px; background:var(--surface); border-right:1px solid var(--border);` 固定全高，独立滚动。
- **Nav item**：`padding:9px 12px; border-radius:10px;` 图标 18px stroke 1.6 `currentColor`。默认文字 `--fg-2`；hover `rgba(48,48,52,.05)`；激活态 `background:rgba(255,36,66,.08); color:var(--accent); font-weight:600;`（激活态即红色之一，注意红色配额）。
- **主区**：`margin-left:224px; min-height:100vh; padding:24px 32px 56px;` 内容容器 `max-width:1200px;`
- **Header**：标题（h1, 24/600）+ 副文案（`--muted`, 14px）+ 右侧工具区（当前套餐徽标 / 通知铃铛带 8px 红点 / 32px 圆形头像）。

### 5.2 栅格

- 8pt 网格；卡片间隙 12–16px；区块大间距 48px。
- Dashboard 指标卡行：`grid-template-columns:repeat(4,1fr)`，`<1120px` 降为 2 列，`<720px` 1 列。
- 双栏布局：主栏 `1fr` + 侧栏 `320px`；`<980px` 堆叠。

## 6. 组件规范

### 6.1 按钮（全部药丸）

| 变体 | 背景 | 文字 | Hover |
|---|---|---|---|
| primary | `--accent` | 白 500 | `--accent-hover` |
| secondary | `rgba(48,48,52,.10)` | `--fg` | `rgba(48,48,52,.16)` |
| ghost | 透明 | `--fg-2` | `rgba(48,48,52,.05)` |
| outline | 白 | `--accent` | 边框+文字换 `--accent-hover` |
| danger | 透明 | `--danger` | `rgba(255,36,66,.08)`（描边型，用于删除/挂起，避免与主 CTA 撞色） |

- 尺寸：`height:36px; padding:0 20px;` 大号 `height:44px; padding:0 28px;` 小号 `height:32px; padding:0 14px; font-size:13px;`
- 交互：按下 `--accent-active`；`transition: background var(--motion-fast) var(--ease-standard);` 永不把前景变浅/变灰。
- 焦点：`button:focus-visible{ outline: var(--focus-ring); outline-offset:2px; }`（在全局 CSS 统一声明）。
- **主 CTA 唯一**：同一视口只允许一个实心主按钮，其余入口用 ghost/outline/文字链接。新建分析是全站唯一实心主 CTA。

### 6.2 卡片

- `background:var(--surface); border-radius:var(--radius-md);` 无阴影。
- PC hover（可点击卡片）：`transform:translateY(-2px); box-shadow:var(--elev-raised);`
- 内部间距：`padding:20px 24px;` 头部：标题（16/600）+ 右侧操作（`--muted` 文字链接）。
- **无左侧彩色竖条**；卡片间靠画布色差分离。

### 6.3 分段控件 / Tabs

- 文字 + 2px 下划线（`--accent`），宽度随文字；激活文字 `--fg` weight 600，未激活 `--muted`。
- 标签间距 32px；`padding:6px 2px;`
- 移动端可横向滚动（`overflow-x:auto;` 隐藏滚动条）。

### 6.4 输入框

- 背景 `--bg`(#F5F5F5)，`border-radius:8px`（搜索类药丸 9999px），`height:40px; padding:0 14px;` 无边框，focus 时 `box-shadow:0 0 0 1px var(--accent);` 或 `--focus-ring`。
- 占位符 `--meta`。标签 13px/500 `--fg-2`。
- 关键词芯片输入：白色胶囊 chip + 右上 × 移除；`rgba(48,48,52,.10)` 底 + `--fg-2` 文字。

### 6.5 状态徽标（Status Pill）

| 状态 | 样式 |
|---|---|
| 已完成 / 成功 | 底 `#EAF8EF`，字 `--success`（#02B940），600 |
| 进行中 | 底 `rgba(255,36,66,.08)`，字 `--accent`，带 8px 脉冲红点 |
| 排队中 / 中性 | 底 `rgba(48,48,52,.10)`，字 `--fg-2` |
| 失败 / 已取消 | 底 `rgba(255,36,66,.08)`，字 `--danger`（描边+圆点区分） |
| 预警 | 底 `#FFF2E6`，字 `--warn` |

通用：`padding:3px 10px; border-radius:9999px; font-size:12px; font-weight:500;` 徽标内可带 6–8px 圆点。

### 6.6 标签 / 话题 Tag

- 胶囊，`padding:4px 12px; font-size:12px;` 默认 `rgba(48,48,52,.10)` 底 + `--fg-2` 字；热点（trending）用 `--accent` 底 + 白字（每屏至多 1–2 个）。

### 6.7 表格

- 白卡内，无外边框；行间 `1px solid var(--border)`；列头 12px/500 `--muted`，上边框 `var(--border)`。
- 行高 52px；hover `rgba(48,48,52,.05)`。首列（如文档标题）可 `--fg` 500，数值列 `tabular-nums`。
- 分页：`上一页 页码 下一页` 药丸小按钮，`--muted`，当前页 `--fg` 600。

### 6.8 进度条

- 轨道 `rgba(48,48,52,.10)` 高 6px 圆角；填充 `--accent`（分析进度）或 `--success`（配额/已完成）。内嵌文本 12px `--muted` 置于右侧。

### 6.9 向导 Stepper（新建分析）

- 水平 4 步，`数字圆点(24px, 灰/红) + 标签 + 连接线`；当前步红色圆点白字 + 标签 600 `--fg`；已完成步红色对勾；未到步灰色圆点 + `--muted`。
- 底部操作条：`返回`(ghost) + `下一步`/`提交`(primary)。Step 3 提交为实心主按钮（全屏唯一主 CTA）。

### 6.10 状态时间线（分析详情，SSE）

- 垂直步骤，每步：左侧节点（12px 圆点）+ 右侧 `状态名 + 时间 + 说明`。
- 已完成绿点，进行中红点脉冲（`@keyframes pulse` 透明度闪烁），未到 `rgba(48,48,52,.10)` 灰点。
- 进行中状态右侧显示 spinner（16px 圆环动画，stroke `--accent`）。

### 6.11 模态 / 抽屉

- PC 居中白卡 `border-radius:12px; box-shadow:0 8px 32px rgba(0,0,0,0.12);` 底 `rgba(0,0,0,0.5)` 遮罩。标题 18/600 + 右上 ×(ghost icon)。底部操作靠右。
- 移动端次级操作一律底部抽屉 `border-radius:16px 16px 0 0` + 顶部 4×36 拖拽条。

### 6.12 图表（ECharts 映射说明）

| 场景 | ECharts 配置要点 |
|---|---|
| 声量趋势（堆叠面积） | `stack` 面积，正面 #EAF8EF→#02B940 系、中性 #E8E8E8、负面红；圆角 smooth 曲线；隐藏网格线 |
| 来源分布（环形） | `pie` + `borderRadius:6`，中心放总数，图例灰 12px |
| 情感占比（环形） | 正面 #02B940 / 中性 `rgba(48,48,52,.18)` / 负面 `--accent` |
| 横向条形（话题/模型用量） | `bar`，`itemStyle.borderRadius:4`，单色 `--accent` 或中性灰 |
| 关键数字 | 大号 `tabular-nums` 24–32px/600，单位 13px `--muted` |

- 静态产物中图表以手写 SVG 呈现（数据编码为填充图形，非空心描边），构建方替换为 ECharts 实例。

### 6.13 头像

- 圆形 `border-radius:50%`，32px（导航）/ 40px（列表）/ 48px（详情）。用首字占位（浅灰底 + `--fg-2` 字）或渐变占位图，**不用真实人脸占位**。

## 7. 响应式

| 断点 | 行为 |
|---|---|
| ≥1120px | 桌面布局：4 列指标卡、双栏 |
| 980–1119px | 双栏堆叠为单栏；指标卡 2 列 |
| 720–979px | 指标卡 2 列；表格横向滚动容器 |
| <720px | Sidebar 折叠为抽屉/顶部条；指标卡 1 列；`padding` 收窄到 12px |

- 触控目标 ≥44px；正文最小 14px；禁止横向滚动。

## 8. 页面清单与规格

> 数据均为**示例数据**，页面右上角或卡片带"示例数据"说明；构建方接入真实 API。

| 文件 | 页面 | 关键内容 |
|---|---|---|
| `login.html` | 登录 / 注册 | 双 tab 切换；左侧品牌价值陈述；注册即创建租户；示例：登录态 CTA 进入 dashboard |
| `dashboard.html` | 数据面板 | 指标卡×4（今日新增/总声量/正面占比/预警数）＋ 声量趋势堆叠面积 + 来源环形 + 热门话题 + 实时告警 + "新建分析"主 CTA |
| `analysis-new.html` | 新建分析（4 步向导） | 1 类型 → 2 关键词/来源/时间/情感 → 3 成本预估（模型价格×Token、配额、预算校验）→ 4 提交 |
| `analysis-list.html` | 分析任务 | 搜索 + 状态筛选 + 表格（名称/类型/关键词/状态/进度/创建时间/操作）+ 分页 |
| `analysis-detail.html` | 分析详情 | SSE 实时时间线（状态机 7 步）+ 情感环形 + 话题 + 关键发现（LLM 摘要）+ 原始文档列表（情感徽标+来源标签） |
| `reports.html` | 报告中心 | 报告列表 + 格式徽标（HTML/MD/PDF/DOCX）+ 下载（按套餐限制）+ 模板说明 |
| `plans.html` | 套餐选择 | 四档对比卡（Free/Pro/Business/Enterprise）+ 当前套餐标识 + 升级确认抽屉 |
| `usage.html` | 用量中心 | 本月 Token 用量/配额、预算进度（hard cap/overage 语义）、分模型用量条形、成本汇总 |
| `invoices.html` | 账单 | 账单列表（期间/金额/状态/下载 PDF）+ 当前余额 + 支付方式 |
| `settings.html` | 团队设置 | Tabs：个人资料 / 成员 / API 密钥(Enterprise) / 安全；成员角色（Owner/Admin/Editor/Viewer） |
| `admin.html` | 管理后台 | 平台概览（租户/用量/收入）+ 租户管理表 + 挂起/恢复操作（描边红） |

## 9. 数据与文案约定

- 所有示例业务文案使用简体中文；数字用 `tabular-nums`。
- 分析状态机文案（用于时间线/徽标）：`草稿 → 已排队 → 预算校验 → 数据抓取 → 智能分析 → 生成报告 → 已完成`（英文 DTO 映射见 `docs/planning/architecture-plan.md` §7）。
- 预算语义文案：Free/Pro = "硬上限，超限拒绝新任务"；Business = "超额按单价计费"；Enterprise = "公平使用，无硬限制"。
- 情感：正面/中性/负面；数据源：微博 / 新闻 / 微信 / 抖音 / 小红书 / 知乎 / 论坛。
- 模型示例：`deepseek-chat` / `kimi-moondream-v2` / `moonshot-v1-32k`（均为占位）。
- 错误信封示例：`{ code, message, details, request_id }`；页面以行内红色提示（`rgba(255,36,66,.08)` 底 + `--danger` 字）呈现。

## 10. 交互状态检查表（每屏必查）

- [ ] primary hover → `--accent-hover`，按下 → `--accent-active`；前景永不变浅
- [ ] 所有可聚焦元素有 `:focus-visible` 焦点环（`--focus-ring`）
- [ ] 禁用态唯一允许降对比度（`--meta` + `rgba(48,48,52,.10)`）
- [ ] 每屏红色使用 ≤ 2 处
- [ ] 正文对比 ≥ 4.5:1（`--fg`/`--fg-2` 在白底上达标）；图标/大字号 ≥ 3:1
- [ ] 无元素重叠、无文本裁切、无横向溢出；中文标题行高 ≥ 1.3
- [ ] 卡片 hover 只用于可点击卡片；`translateY(-2px)` + `--elev-raised`

## 11. 构建方映射指南

> 技术栈：React 18 + TypeScript + Ant Design 5 + ECharts 5

### 11.1 设计文件清单

| 文件 | 性质 | 用途 |
|---|---|---|
| `shared.css` | CSS 契约 | 全局 Token + 重置 + 外壳 + 通用组件，构建方直接复制到项目 |
| `design-spec.md` | 设计规范 | 本文档，构建方阅读理解设计意图与约束 |
| `index.html` | 设计系统文档中心 | 色彩/字体/组件/图表色板/AntD 映射/ConfigProvider 模板 |
| `dashboard.html` 等 11 页 | 交互原型 | 每页自包含 token + 样式 + 完整业务组件结构 |

### 11.2 Ant Design ConfigProvider 模板

```ts
// theme.ts — 直接复制到 React 项目入口
import type { ThemeConfig } from 'antd';

export const weiyuTheme: ThemeConfig = {
  token: {
    colorPrimary: '#ff2442',
    colorPrimaryHover: '#ff2e4d',
    colorPrimaryActive: '#e6203a',
    colorSuccess: '#02b940',
    colorWarning: '#ff7d03',
    colorError: '#ff2442',
    colorBgContainer: '#ffffff',
    colorBgLayout: '#f5f5f5',
    fontFamily: `"PingFang SC","Hiragino Sans GB","Microsoft YaHei","Noto Sans SC",-apple-system,"Helvetica Neue",Helvetica,Arial,sans-serif`,
    borderRadius: 8,
    borderRadiusLG: 12,
    motionDurationSlow: '200ms',
    motionDurationMid: '150ms',
    boxShadow: 'none',
    boxShadowSecondary: '0 4px 12px rgba(0,0,0,0.08)',
  },
  components: {
    Button: {
      borderRadius: 9999,
      borderRadiusLG: 9999,
      borderRadiusSM: 9999,
      contentFontSize: 14,
      fontWeight: 500,
    },
    Card: {
      borderRadiusLG: 12,
      boxShadow: 'none',
    },
    Table: {
      headerColor: 'rgba(0,0,0,0.45)',
      headerBg: '#fafafa',
      rowHoverBg: 'rgba(48,48,52,0.04)',
    },
    Tabs: {
      inkBarColor: '#ff2e4d',
      itemActiveColor: 'rgba(0,0,0,0.80)',
      itemColor: 'rgba(0,0,0,0.45)',
    },
    Tag: {
      borderRadiusSM: 9999,
    },
  },
};
```

### 11.3 组件映射速查

| CSS 类名 / 设计意图 | Ant Design 组件 | 注意事项 |
|---|---|---|
| `.btn-primary` | `<Button type="primary" shape="round" />` | AntD `round` 自带药丸；hover 色需覆写为 `#ff2e4d` |
| `.btn-secondary` | `<Button shape="round" />` | 默认灰色需覆写为 `rgba(48,48,52,0.10)` |
| `.btn-outline` | `<Button shape="round" />` + style | border + color 设为 `--accent` |
| `.btn-ghost` | `<Button type="text" />` | AntD text 类型 = 无背景 |
| `.btn-danger-ghost` | `<Button danger type="default" shape="round" />` | 描边型危险操作，非实心 |
| `.card` | `<Card />` 或 `<div>` | AntD Card 默认含阴影，需覆写为 `none`；`borderRadius: 12` |
| `.chip` (状态徽标) | `<Tag />` | 语义色按 status 映射；圆角 9999px |
| `.seg` / `.topnav-tabs` | `<Tabs />` + inkBar | 2px 下划线指示器；颜色用 `--accent-hover` |
| `.progress` | `<Progress percent={n} />` | `strokeColor: --accent`, `trailColor: rgba(48,48,52,.10)` |
| `.field` | `<Input />` + `variant="filled"` | 背景 `var(--bg)`，无边框，focus 红环 |
| `.search-pill` | `<Input.Search />` | 圆角 9999px，背景 `var(--bg)` |
| `.sidebar` | `<Layout.Sider width={224} />` | 固定 224px，白底，右分隔线，独立滚动 |
| 表格 | `<Table />` | 表头 12/500 `--muted`；行 hover `rgba(48,48,52,.04)`；无外边框 |

### 11.4 ECharts 图表配置速查

| 场景 | 关键配置 | 对应页面 |
|---|---|---|
| 声量趋势（堆叠面积） | `series.type: 'line'` + `areaStyle` + `stack` + `smooth`；三色填充；隐藏 `splitLine`；`borderRadius: 4` | dashboard |
| 情感占比（环形） | `series.type: 'pie'`；`radius: ['55%','75%']`；`itemStyle.borderRadius: 6`；中心放总数 | dashboard, analysis-detail |
| 来源分布（环形） | 同上，`color` 用 6 级冷灰阶 `rgba(48,48,52, .07–.37)` | dashboard |
| 话题/模型用量（横向条形） | `series.type: 'bar'`；`xAxis + yAxis` 互换；`itemStyle.borderRadius: 4`；单色 `--accent` 或中性灰 | dashboard, usage |
| 关键数字（大号） | 纯 CSS：`font-size: 24–32px` / `600` / `tabular-nums` / `color: --fg` | 所有指标卡 |

### 11.5 情感三色（ECharts color 数组）

```js
// 正面 / 中性 / 负面 — 直接用于 ECharts series.color
const SENTIMENT_COLORS = {
  positive: { fill: 'rgba(2,185,64,0.16)', stroke: '#02b940' },
  neutral:  { fill: 'rgba(48,48,52,0.10)', stroke: 'rgba(48,48,52,0.35)' },
  negative: { fill: 'rgba(255,36,66,0.12)', stroke: '#ff2442' },
};
```

## 12. 变更记录

| 日期 | 版本 | 变更 |
|---|---|---|
| 2026-08-11 | v1.0 | 初始版本：色彩 token、字体层级、布局系统、13 类组件规范、11 页规格、数据约定、交互检查表 |
| 2026-08-11 | v1.1 | 新增 §11 构建方映射指南（Ant Design ConfigProvider 模板、组件映射表、ECharts 配置速查）；新增 `shared.css` 全局样式契约文件 |
| 2026-08-11 | v1.2 | 新增 `assets/` 品牌素材目录：`brand-hero.svg`（登录页品牌区插图）、`empty-data.svg` / `empty-reports.svg` / `empty-search.svg`（空态占位图）；更新 `login.html` 左侧品牌区新增 SVG 视觉素材 |
