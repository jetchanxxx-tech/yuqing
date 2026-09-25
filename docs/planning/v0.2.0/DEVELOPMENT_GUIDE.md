# v0.2.0 开发指南

**版本**: v0.2.0  
**作者**: 技术总监  
**日期**: 2026-09-25  
**依赖**: [TECHNICAL_DESIGN.md](./TECHNICAL_DESIGN.md)

---

## 目录

1. [开发顺序](#开发顺序)
2. [分支策略](#分支策略)
3. [需求1：营销方案建议与跟踪](#需求1营销方案建议与跟踪)
4. [需求2：竞品舆情对比](#需求2竞品舆情对比)
5. [需求3：指数热度geo](#需求3指数热度geo)
6. [测试策略](#测试策略)
7. [部署清单](#部署清单)
8. [验收标准](#验收标准)

---

## 开发顺序

### 总体策略

三个需求**独立开发、独立测试、独立上线**，避免相互阻塞。推荐并行开发顺序：

| 周次 | F23 营销活动 | F24 竞品对比 | F25 指数/Geo |
|------|-------------|-------------|-------------|
| W1 | 数据库 + Service 层 | 数据库 + Service 层 | LAC 集成 + 指数计算 |
| W2 | Strategy Engine + Pipeline | API + 监听器 | API + 前端组件 |
| W3 | API + 定时器 | 前端页面 | 集成测试 |
| W4 | 前端页面 + E2E | E2E 测试 | 生产验证 |

### 依赖关系

```
F25（指数/Geo）→ 无外部依赖，优先开发
F24（竞品对比）→ 依赖 analysis.Service，次优先
F23（营销活动）→ 依赖 analysis.Service + 新 strategy_engine，最复杂
```

**建议启动顺序**：F25 → F24 → F23（或 F24/F23 并行）

---

## 分支策略

### Git 工作流

```bash
# 主分支：main（当前 v0.1.2-beta 已部署生产）
git checkout main

# 为每个需求创建独立 feature 分支
git checkout -b feature/f23-campaign-tracking    # 营销活动
git checkout -b feature/f24-competitor-analysis  # 竞品对比
git checkout -b feature/f25-indices-geo          # 指数/Geo

# 开发完成后，合并回 main
git checkout main
git merge --no-ff feature/f25-indices-geo
git tag v0.2.0-f25
git push origin main --tags
```

### Feature Flag（可选）

如果三个需求需要在生产环境分阶段验证，可用环境变量 gate：

```go
// platform/internal/config/config.go
type FeatureFlags struct {
	CampaignTracking    bool `yaml:"campaign_tracking" env:"FEATURE_CAMPAIGN_TRACKING"`
	CompetitorAnalysis  bool `yaml:"competitor_analysis" env:"FEATURE_COMPETITOR_ANALYSIS"`
	IndicesGeo          bool `yaml:"indices_geo" env:"FEATURE_INDICES_GEO"`
}

// API 路由注册时检查
if cfg.Features.CampaignTracking {
	svc.RegisterCampaigns(v1)
}
```

---

## 需求1：营销方案建议与跟踪

### 工作量：8-10 人天

| 任务 | 工作量 | 文件路径 |
|------|--------|---------|
| 数据库设计 + 迁移脚本 | 0.5 天 | `platform/migrations/platform/0009_campaigns_and_competitors.sql` (Part 1) |
| Go Service 层 | 2 天 | `platform/internal/business/campaign/service.go`, `store_pg.go`, `types.go` |
| Strategy Engine (Python) | 2 天 | `engines/strategy_engine/main.py`, `prompts.py`, `llm_client.py` |
| Pipeline + 定时器 | 1.5 天 | `platform/internal/business/campaign/pipeline.go`, `platform/cmd/worker/checkpoint_scanner.go` |
| Engine 契约 + HTTP Transport | 1 天 | `platform/internal/engine/strategy.go`, `strategy_http.go` |
| API Handler | 1 天 | `platform/internal/api/v1/campaigns.go` |
| 前端页面 | 2 天 | `web/src/pages/CampaignListPage.tsx`, `CampaignDetailPage.tsx`, `CampaignNewPage.tsx` |
| 单元测试 + 集成测试 | 1 天 | `campaign/service_test.go`, `api/v1/campaigns_contract_test.go` |

### 详细步骤

#### Day 1-0.5: 数据库迁移

**文件**: `platform/migrations/platform/0009_campaigns_and_competitors.sql`

1. 复制技术设计文档中的 SQL（campaigns + campaign_checkpoints 两表）
2. 本地测试迁移：
   ```bash
   cd platform
   YUQING_CONFIG=../config/config.yaml go run ./cmd/cli migrate platform
   psql yuqing_platform -c "\d campaigns"
   ```
3. **验证**: 表创建成功 + 索引存在 + 约束生效

#### Day 1-2: Go Service 层

**文件**: 
- `platform/internal/business/campaign/types.go`
- `platform/internal/business/campaign/service.go`
- `platform/internal/business/campaign/store_pg.go`

**实现顺序**：

1. **定义类型**（`types.go`）：
   ```go
   package campaign
   
   import "time"
   
   const (
   	StatusPending    = "pending"
   	StatusGenerating = "generating"
   	StatusActive     = "active"
   	StatusCompleted  = "completed"
   	StatusFailed     = "failed"
   )
   
   const (
   	CheckpointPending   = "pending"
   	CheckpointFetching  = "fetching"
   	CheckpointAnalyzing = "analyzing"
   	CheckpointCompleted = "completed"
   	CheckpointFailed    = "failed"
   )
   
   type Campaign struct {
   	ID                 string
   	AnalysisID         string
   	Name               string
   	Description        string
   	StrategyContent    string
   	StrategySummary    string
   	StrategyDimensions []DimensionAdvice
   	Status             string
   	CreatedBy          string
   	CreatedAt          time.Time
   	UpdatedAt          time.Time
   }
   
   type DimensionAdvice struct {
   	Dimension      string `json:"dimension"`
   	Advice         string `json:"advice"`
   	ExpectedImpact string `json:"expected_impact"`
   }
   
   type Checkpoint struct {
   	ID              string
   	CampaignID      string
   	Name            string
   	Keywords        []string
   	CheckpointAt    time.Time
   	FetchAnalysisID string
   	Status          string
   	Metrics         map[string]interface{}
   	Insights        string
   	CreatedAt       time.Time
   	UpdatedAt       time.Time
   }
   
   type Store interface {
   	Create(ctx context.Context, tenantID string, c *Campaign) error
   	Get(ctx context.Context, tenantID, campaignID string) (*Campaign, error)
   	List(ctx context.Context, tenantID string) ([]Campaign, error)
   	UpdateStatus(ctx context.Context, tenantID, campaignID, status string) error
   	SetStrategy(ctx context.Context, tenantID, campaignID, content, summary string, dimensions []DimensionAdvice) error
   	
   	CreateCheckpoint(ctx context.Context, tenantID string, cp *Checkpoint) error
   	GetCheckpoint(ctx context.Context, tenantID, checkpointID string) (*Checkpoint, error)
   	ListCheckpoints(ctx context.Context, tenantID, campaignID string) ([]Checkpoint, error)
   	ListPendingCheckpoints(ctx context.Context) ([]Checkpoint, error)
   	BindCheckpointAnalysis(ctx context.Context, tenantID, checkpointID, analysisID string) error
   	UpdateCheckpointStatus(ctx context.Context, tenantID, checkpointID, status string) error
   	SetCheckpointResult(ctx context.Context, tenantID, checkpointID string, metrics map[string]interface{}, insights string) error
   }
   
   type AnalysisService interface {
   	Get(ctx context.Context, tenantID, analysisID string) (*analysis.AnalysisResult, error)
   	Create(ctx context.Context, tenantID string, req analysis.CreateRequest, createdBy string) (string, error)
   }
   
   type CreditReserver interface {
   	TryConsume(ctx context.Context, tenantID, analysisID string) error
   	RefundByAnalysis(ctx context.Context, tenantID, analysisID string) (bool, error)
   }
   ```

2. **实现 PG Store**（`store_pg.go`）：
   - 复用 pgxpool 模式（参考 `analysis/store_pg.go`）
   - 关键方法：`Create`, `Get`, `UpdateStatus`, `SetStrategy`, `CreateCheckpoint`, `ListPendingCheckpoints`
   - **注意**: `StrategyDimensions` JSONB 字段用 `json.Marshal/Unmarshal` 处理

3. **实现 Service**（`service.go`）：
   - 复制技术设计文档中的 `Service` 实现
   - 关键逻辑：`Create` 扣费 + 发布队列消息，`AddCheckpoint` 校验 campaign 状态

4. **单元测试**（`service_test.go`）：
   ```go
   func TestCampaignCreate(t *testing.T) {
   	store := newTestStore()
   	q := queue.NewMemory()
   	credits := &mockCreditReserver{}
   	analysisSvc := &mockAnalysisService{}
   	
   	svc := NewService(store, q, analysisSvc, credits)
   	
   	campaignID, err := svc.Create(context.Background(), "tenant1", "analysis1", "Campaign1", "", "user1")
   	assert.NoError(t, err)
   	assert.NotEmpty(t, campaignID)
   	assert.Equal(t, 1, credits.consumeCount) // 验证扣费调用
   }
   ```

#### Day 3-4: Strategy Engine (Python)

**文件**: 
- `engines/strategy_engine/main.py`
- `engines/strategy_engine/prompts.py`
- `engines/strategy_engine/llm_client.py`

**目录结构**：
```
engines/strategy_engine/
├── __init__.py
├── main.py          # FastAPI 应用
├── prompts.py       # Prompt 模板
├── llm_client.py    # 复用 common/llm_client.py
└── requirements.txt # 无新依赖（复用 openai）
```

**实现**：

1. **Prompt 模板**（`prompts.py`）：
   ```python
   STRATEGY_PROMPT = """你是资深品牌营销策略顾问。基于以下舆情分析结果，为客户制定可执行的营销方案建议。
   
   **舆情背景**：
   - 品牌/产品：{campaign_name}
   - 监测关键词：{keywords}
   - 情感分布：正面 {positive}% / 负面 {negative}% / 中性 {neutral}%
   - 核心话题：{topics}
   - 五维研判：
   {dimensions}
   
   **输出要求**：
   1. 核心策略（1-2 句话）
   2. 分维度营销建议（针对每个维度给出具体行动方案）
   3. 预期效果（量化指标）
   4. 执行预算与时间线
   
   **输出格式**（Markdown）：
   # 营销方案建议
   
   ## 一、核心策略
   ...
   
   ## 二、分维度行动方案
   ### 口碑塑造
   - 建议：...
   - 预期效果：...
   
   ### 产品力展示
   ...
   """
   
   COMPARE_PROMPT = """你是舆情分析专家。对比以下营销活动前后的舆情变化，生成跟踪洞察。
   
   **检查点**：{checkpoint_name}
   
   **基线数据**（营销活动前）：
   - 情感分布：正面 {baseline_pos}% / 负面 {baseline_neg}% / 中性 {baseline_neu}%
   - 声量：{baseline_volume} 条
   - 核心话题：{baseline_topics}
   
   **当前数据**（营销活动后）：
   - 情感分布：正面 {current_pos}% / 负面 {current_neg}% / 中性 {current_neu}%
   - 声量：{current_volume} 条
   - 核心话题：{current_topics}
   
   **输出要求**：
   1. 核心洞察（100-200字）：哪些指标改善？哪些恶化？营销策略是否见效？
   2. 趋势判断：improving（改善）/ stable（稳定）/ declining（下滑）
   3. 下一步建议（3-5 条）
   
   输出 JSON 格式：
   {
     "insights": "...",
     "trend": "improving",
     "next_steps": ["...", "..."]
   }
   """
   ```

2. **FastAPI 应用**（`main.py`）：
   ```python
   from fastapi import FastAPI, HTTPException
   from pydantic import BaseModel
   from typing import List, Dict, Any
   import os
   import json
   from engines.common.llm_client import LLMClient
   from .prompts import STRATEGY_PROMPT, COMPARE_PROMPT
   
   app = FastAPI(title="Strategy Engine", version="0.2.0")
   
   # 复用平台 LLM 配置
   llm_client = LLMClient(
       api_key=os.getenv("LLM_API_KEY", os.getenv("BOCHA_API_KEY")),
       base_url=os.getenv("LLM_BASE_URL"),
       model=os.getenv("LLM_MODEL", "glm-4-flash"),
   )
   
   class AnalysisSummary(BaseModel):
       keywords: List[str]
       sentiment: Dict[str, int]
       topics: List[Dict[str, Any]]
       dimensions: List[Dict[str, Any]]
   
   class StrategyRequest(BaseModel):
       analysis_id: str
       campaign_name: str
       analysis_summary: AnalysisSummary
   
   class DimensionAdvice(BaseModel):
       dimension: str
       advice: str
       expected_impact: str
   
   class StrategyResponse(BaseModel):
       strategy_content: str
       strategy_summary: str
       dimensions: List[DimensionAdvice]
       estimated_budget: str = ""
       execution_timeline: str = ""
   
   @app.post("/generate-strategy", response_model=StrategyResponse)
   async def generate_strategy(req: StrategyRequest):
       summary = req.analysis_summary
       total = sum(summary.sentiment.values())
       if total == 0:
           raise HTTPException(400, "Empty sentiment data")
       
       # 构造 Prompt
       pos_pct = summary.sentiment.get("positive", 0) / total * 100
       neg_pct = summary.sentiment.get("negative", 0) / total * 100
       neu_pct = summary.sentiment.get("neutral", 0) / total * 100
       
       topics_str = ", ".join([t["name"] for t in summary.topics[:5]])
       dimensions_str = "\n".join([
           f"- {d['dimension']}: {d['conclusion'][:100]}..." 
           for d in summary.dimensions
       ])
       
       prompt = STRATEGY_PROMPT.format(
           campaign_name=req.campaign_name,
           keywords=", ".join(summary.keywords),
           positive=f"{pos_pct:.1f}",
           negative=f"{neg_pct:.1f}",
           neutral=f"{neu_pct:.1f}",
           topics=topics_str,
           dimensions=dimensions_str,
       )
       
       # 调用 LLM
       try:
           content = llm_client.chat(prompt, temperature=0.3, max_tokens=4096)
       except Exception as e:
           # 降级：返回模板化建议
           content = f"# 营销方案建议\n\n## 核心策略\n针对当前舆情，建议加强正面内容传播，优化用户体验。\n\n## 分维度建议\n- 口碑塑造：发起 KOL 体验活动\n- 产品力展示：突出核心卖点"
       
       # 解析维度建议（简化版：从 content 提取）
       dimensions = _extract_dimensions(content, summary.dimensions)
       
       # 生成摘要（取第一段）
       summary_text = content.split("\n\n")[1] if "\n\n" in content else content[:100]
       
       return StrategyResponse(
           strategy_content=content,
           strategy_summary=summary_text[:200],
           dimensions=dimensions,
           estimated_budget="10-15万",
           execution_timeline="4-6周",
       )
   
   def _extract_dimensions(content: str, raw_dimensions: List[Dict]) -> List[DimensionAdvice]:
       """从生成内容提取维度建议（简化版）"""
       result = []
       for d in raw_dimensions[:5]:
           result.append(DimensionAdvice(
               dimension=d["dimension"],
               advice=f"针对'{d['dimension']}'维度，建议优化内容传播策略。",
               expected_impact="预期指标提升 10-15%",
           ))
       return result
   
   class Metrics(BaseModel):
       sentiment: Dict[str, int]
       volume: int
       topics: List[Dict[str, Any]]
   
   class CompareRequest(BaseModel):
       baseline: Metrics
       current: Metrics
       checkpoint_name: str
   
   class CompareResponse(BaseModel):
       insights: str
       trend: str
       next_steps: List[str]
   
   @app.post("/compare", response_model=CompareResponse)
   async def compare_checkpoint(req: CompareRequest):
       # 计算变化
       base_total = sum(req.baseline.sentiment.values())
       curr_total = sum(req.current.sentiment.values())
       
       if base_total == 0 or curr_total == 0:
           raise HTTPException(400, "Invalid sentiment data")
       
       base_pos = req.baseline.sentiment.get("positive", 0) / base_total * 100
       curr_pos = req.current.sentiment.get("positive", 0) / curr_total * 100
       
       # 构造 Prompt
       prompt = COMPARE_PROMPT.format(
           checkpoint_name=req.checkpoint_name,
           baseline_pos=f"{base_pos:.1f}",
           baseline_neg=f"{req.baseline.sentiment.get('negative', 0) / base_total * 100:.1f}",
           baseline_neu=f"{req.baseline.sentiment.get('neutral', 0) / base_total * 100:.1f}",
           baseline_volume=req.baseline.volume,
           baseline_topics=", ".join([t["name"] for t in req.baseline.topics[:5]]),
           current_pos=f"{curr_pos:.1f}",
           current_neg=f"{req.current.sentiment.get('negative', 0) / curr_total * 100:.1f}",
           current_neu=f"{req.current.sentiment.get('neutral', 0) / curr_total * 100:.1f}",
           current_volume=req.current.volume,
           current_topics=", ".join([t["name"] for t in req.current.topics[:5]]),
       )
       
       try:
           response_text = llm_client.chat(prompt, temperature=0.2, max_tokens=1024)
           # 尝试解析 JSON
           data = json.loads(response_text)
           return CompareResponse(**data)
       except Exception as e:
           # 降级：基于规则生成
           delta_pos = curr_pos - base_pos
           trend = "improving" if delta_pos > 5 else ("declining" if delta_pos < -5 else "stable")
           
           return CompareResponse(
               insights=f"情感正面占比变化 {delta_pos:+.1f} 个百分点，声量变化 {req.current.volume - req.baseline.volume:+d} 条。",
               trend=trend,
               next_steps=["持续监测舆情动态", "优化内容传播策略"],
           )
   
   @app.get("/health")
   async def health():
       return {"status": "ok", "engine": "strategy"}
   ```

3. **测试**：
   ```bash
   cd engines
   source venv/bin/activate
   uvicorn strategy_engine.main:app --port 8005 --reload
   
   # 测试 /generate-strategy
   curl -X POST http://127.0.0.1:8005/generate-strategy \
     -H "Content-Type: application/json" \
     -d '{"analysis_id":"test","campaign_name":"雅阁后排","analysis_summary":{"keywords":["雅阁"],"sentiment":{"positive":50,"negative":30,"neutral":20},"topics":[],"dimensions":[]}}'
   ```

#### Day 5: Pipeline + 定时器

**文件**:
- `platform/internal/business/campaign/pipeline.go`
- `platform/cmd/worker/checkpoint_scanner.go`

**实现**：复制技术设计文档中的代码，关键点：

1. **Pipeline.Handle**：
   - 调用 `strategy_engine.GenerateStrategy`
   - 超时 180s
   - 失败时 `markFailed` + 回补额度

2. **CheckpointScanner**：
   - 每小时扫描 `checkpoint_at <= NOW() AND status='pending'`
   - 创建新 analysis 任务（keywords 复用基线或用户指定）
   - 绑定 `fetch_analysis_id`，更新 status → fetching

3. **集成到 worker**（`cmd/worker/main.go`）：
   ```go
   func main() {
   	// ... 现有初始化 ...
   	
   	// 启动检查点扫描器
   	go campaign.CheckpointScanner(ctx, services.campaign, services.analysis)
   	
   	// ... 现有队列消费 ...
   }
   ```

#### Day 6: Engine 契约 + HTTP Transport

**文件**:
- `platform/internal/engine/strategy.go`
- `platform/internal/engine/strategy_http.go`

复制技术设计文档中的接口定义和 HTTP 实现。

#### Day 7: API Handler

**文件**: `platform/internal/api/v1/campaigns.go`

复制技术设计文档中的 Handler 实现，关键点：

1. **路由注册**（`router.go`）：
   ```go
   svc.RegisterCampaigns(v1)
   ```

2. **契约测试**（`campaigns_contract_test.go`）：
   ```go
   func TestCampaignsContract(t *testing.T) {
   	t.Run("POST /campaigns - 201", func(t *testing.T) {
   		// 创建基线分析
   		analysisID := createTestAnalysis(t, tenantID)
   		
   		// 创建营销活动
   		resp := httptest.NewRecorder()
   		req := httptest.NewRequest("POST", "/api/v1/campaigns", strings.NewReader(`{
   			"analysis_id": "`+analysisID+`",
   			"name": "Test Campaign"
   		}`))
   		req.Header.Set("Authorization", "Bearer "+jwt)
   		router.ServeHTTP(resp, req)
   		
   		assert.Equal(t, 202, resp.Code)
   		var body map[string]interface{}
   		json.Unmarshal(resp.Body.Bytes(), &body)
   		assert.Equal(t, "generating", body["status"])
   	})
   }
   ```

#### Day 8-9: 前端页面

**文件**:
- `web/src/pages/CampaignListPage.tsx`
- `web/src/pages/CampaignDetailPage.tsx`
- `web/src/pages/CampaignNewPage.tsx`
- `web/src/api/campaigns.ts`

**实现**：

1. **API 客户端**（`campaigns.ts`）：
   ```typescript
   import { apiClient } from './client';
   
   export interface Campaign {
     id: string;
     name: string;
     status: string;
     strategy?: {
       content: string;
       summary: string;
       dimensions: Array<{
         dimension: string;
         advice: string;
         expected_impact: string;
       }>;
     };
     checkpoints: Checkpoint[];
     created_at: string;
   }
   
   export interface Checkpoint {
     id: string;
     name: string;
     checkpoint_at: string;
     status: string;
     metrics?: any;
     insights?: string;
   }
   
   export const campaignsApi = {
     create: (data: { analysis_id: string; name: string; description?: string }) =>
       apiClient.post<{ campaign_id: string }>('/campaigns', data),
     
     get: (id: string) =>
       apiClient.get<Campaign>(`/campaigns/${id}`),
     
     list: () =>
       apiClient.get<{ campaigns: Campaign[] }>('/campaigns'),
     
     addCheckpoint: (campaignId: string, data: { name: string; checkpoint_at: string; keywords?: string[] }) =>
       apiClient.post(`/campaigns/${campaignId}/checkpoints`, data),
     
     getReport: (campaignId: string) =>
       apiClient.get(`/campaigns/${campaignId}/report`),
   };
   ```

2. **列表页**（`CampaignListPage.tsx`）：
   - Table 展示：名称/状态/创建时间/操作
   - 状态标签：pending（灰）/ generating（蓝）/ active（绿）/ failed（红）
   - 操作：查看详情 + 删除

3. **创建页**（`CampaignNewPage.tsx`）：
   - 选择基线分析（下拉框）
   - 输入活动名称 + 描述
   - 提交 → 跳转到详情页

4. **详情页**（`CampaignDetailPage.tsx`）：
   - 顶部：活动信息 + 状态
   - Tab 1：营销方案（Markdown 渲染 `strategy.content`）
   - Tab 2：检查点列表（Table + 添加按钮）
   - Tab 3：跟踪报告（图表 + 洞察）

#### Day 10: 集成测试

**文件**: `platform/test/integration/campaign_test.go`

```go
func TestCampaignFullFlow(t *testing.T) {
	// 1. 创建基线分析
	analysisID := createAnalysis(t, tenantID, []string{"雅阁", "后排空间"})
	waitForCompletion(t, analysisID)
	
	// 2. 创建营销活动
	campaignID := createCampaign(t, tenantID, analysisID, "Test Campaign")
	
	// 3. 等待策略生成
	time.Sleep(5 * time.Second)
	c, _ := campaignSvc.Get(context.Background(), tenantID, campaignID)
	assert.Equal(t, campaign.StatusActive, c.Status)
	assert.NotEmpty(t, c.StrategyContent)
	
	// 4. 添加检查点
	checkpointID, _ := campaignSvc.AddCheckpoint(context.Background(), tenantID, campaignID, "7天后", time.Now().Add(7*24*time.Hour), nil)
	assert.NotEmpty(t, checkpointID)
}
```

---

## 需求2：竞品舆情对比

### 工作量：6-8 人天

| 任务 | 工作量 | 文件路径 |
|------|--------|---------|
| 数据库设计 + 迁移脚本 | 0.5 天 | `platform/migrations/platform/0009_campaigns_and_competitors.sql` (Part 2) |
| Go Service 层 | 1.5 天 | `platform/internal/business/competitor/service.go`, `store_pg.go`, `types.go` |
| 监听器 | 1 天 | `platform/internal/business/competitor/listener.go` |
| API Handler | 1 天 | `platform/internal/api/v1/competitors.go` |
| 前端页面 | 2.5 天 | `web/src/pages/CompetitorListPage.tsx`, `ComparisonReportPage.tsx` |
| 单元测试 + 集成测试 | 1 天 | `competitor/service_test.go`, `api/v1/competitors_contract_test.go` |

### 详细步骤

#### Day 1-0.5: 数据库迁移

与需求1共用 `0009_campaigns_and_competitors.sql`（Part 2 包含 competitors/competitor_groups/competitor_analyses 三表）。

#### Day 1-2: Go Service 层

**实现要点**：

1. **CreateCompetitor**：校验 keywords 非空，tenant+name 唯一性由 PG UNIQUE 约束保证
2. **CreateGroup**：至少 2 个竞品，校验所有竞品存在
3. **CreateAnalysis**：扣费 N 次（循环调用 `credits.TryConsume`），并发创建 N 个 analysis 任务
4. **GenerateComparison**：聚合各竞品的分析结果，计算对比指标，调 LLM 生成洞察（可选）

#### Day 3: 监听器

**文件**: `platform/internal/business/competitor/listener.go`

**实现**：复制技术设计文档中的轮询逻辑，每 30 秒扫描一次 `status=running` 的竞品分析。

**优化建议**（可选）：改为事件驱动，在 `analysis.Pipeline` 完成时发布事件。

#### Day 4: API Handler

**文件**: `platform/internal/api/v1/competitors.go`

**端点**：
- `POST /competitors` - 创建竞品
- `GET /competitors` - 列出竞品
- `POST /competitor-groups` - 创建竞品组
- `GET /competitor-groups` - 列出竞品组
- `POST /competitor-analyses` - 创建竞品分析
- `GET /competitor-analyses/:id/comparison` - 获取对比报告

#### Day 5-6: 前端页面

**文件**:
- `web/src/pages/CompetitorListPage.tsx` - 竞品管理（CRUD）
- `web/src/pages/CompetitorGroupPage.tsx` - 竞品组管理
- `web/src/pages/ComparisonReportPage.tsx` - 对比报告

**关键组件**：

1. **情感对比柱状图**（ECharts）：
   ```tsx
   const option = {
     xAxis: { type: 'category', data: competitors },
     yAxis: { type: 'value' },
     series: [
       { name: '正面', type: 'bar', data: positiveData, color: '#02b940' },
       { name: '负面', type: 'bar', data: negativeData, color: '#FF2442' },
     ],
   };
   ```

2. **五维雷达图**（ECharts）：
   ```tsx
   const option = {
     radar: {
       indicator: [
         { name: '口碑塑造', max: 100 },
         { name: '产品力展示', max: 100 },
         { name: '用户体验', max: 100 },
         { name: '品牌影响力', max: 100 },
         { name: '市场趋势', max: 100 },
       ],
     },
     series: [{
       type: 'radar',
       data: competitors.map(c => ({ value: c.scores, name: c.name })),
     }],
   };
   ```

#### Day 7: 集成测试

**测试场景**：
1. 创建 3 个竞品
2. 创建竞品组
3. 创建竞品分析（mode=comparison）
4. 等待所有 analysis 完成
5. 验证对比报告生成

---

## 需求3：指数热度Geo

### 工作量：5-7 人天

| 任务 | 工作量 | 文件路径 |
|------|--------|---------|
| 数据库迁移 | 0.5 天 | `platform/migrations/platform/0010_indices_and_geo.sql` |
| LAC 集成 (Python) | 1 天 | `engines/common/ner.py`, `engines/insight_engine/main.py` |
| 指数计算 (Go) | 1 天 | `platform/internal/business/analysis/indices.go`, `pipeline.go` |
| API 扩展 | 0.5 天 | `platform/internal/api/v1/analyses.go` (扩展响应) |
| 前端组件 | 2 天 | `web/src/components/IndicesCards.tsx`, `GeoChart.tsx` |
| 测试 + 生产验证 | 1 天 | 单元测试 + E2E |

### 详细步骤

#### Day 1-0.5: 数据库迁移

**文件**: `platform/migrations/platform/0010_indices_and_geo.sql`

复制技术设计文档中的 `ALTER TABLE` 语句，添加 4 列。

#### Day 1: LAC 集成

**文件**:
- `engines/common/ner.py`
- `engines/insight_engine/main.py`（修改）

**实现**：

1. **NER 模块**（`ner.py`）：
   ```python
   from lac import LAC
   from typing import Dict, List
   
   class CityExtractor:
       def __init__(self):
           self.lac = LAC(mode='lac')
           self.city_whitelist = {
               "北京", "上海", "天津", "重庆", "广州", "深圳", "杭州", "成都",
               "西安", "武汉", "郑州", "南京", "苏州", "长沙", "东莞", "沈阳",
               # ... 完整城市列表见技术设计文档 ...
           }
       
       def extract_cities(self, texts: List[str]) -> Dict[str, int]:
           city_counts = {}
           for text in texts:
               if not text or len(text) < 10:
                   continue
               
               try:
                   lac_result = self.lac.run(text)
                   words, tags = lac_result[0], lac_result[1]
                   
                   for word, tag in zip(words, tags):
                       if tag == 'LOC' and word in self.city_whitelist:
                           city_counts[word] = city_counts.get(word, 0) + 1
               except Exception as e:
                   # LAC 失败不影响主流程
                   continue
           
           return city_counts
   ```

2. **集成到 Insight Engine**（`insight_engine/main.py`）：
   ```python
   from engines.common.ner import CityExtractor
   
   city_extractor = CityExtractor()
   
   @app.post("/analyze")
   async def analyze_insight(req: InsightRequest):
       # ... 现有五维分析逻辑 ...
       
       # 新增：提取城市分布
       texts = [doc.content for doc in req.documents if doc.content]
       geo_distribution = city_extractor.extract_cities(texts)
       
       return {
           "sentiment": sentiment,
           "topics": topics,
           "dimensions": dimensions,
           "summary": summary,
           "geo_distribution": geo_distribution,  # 新增字段
       }
   ```

3. **测试**：
   ```bash
   cd engines
   pip install lac
   python -c "from lac import LAC; lac = LAC(mode='lac'); print(lac.run('我在北京和上海都见过雅阁'))"
   # 预期输出：(['我', '在', '北京', '和', '上海', '都', '见', '过', '雅阁'], ['r', 'p', 'LOC', 'c', 'LOC', 'd', 'v', 'u', 'nz'])
   ```

#### Day 2: 指数计算

**文件**:
- `platform/internal/business/analysis/indices.go`
- `platform/internal/business/analysis/pipeline.go`（修改）

**实现**：

1. **指数计算函数**（`indices.go`）：
   - 复制技术设计文档中的 `CalculateIndices` 函数
   - 三个公式：情感指数（正负加权）/ 热度指数（log声量+话题+时间衰减）/ 风险指数（负面+敏感话题）

2. **集成到 Pipeline**（`pipeline.go:L120`）：
   ```go
   // analyzing 步骤完成后，计算指数
   if a.InsightAvailable {
   	sentiment, heat, risk := CalculateIndices(a)
   	if err := s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
   		a.SentimentIndex = sentiment
   		a.HeatIndex = heat
   		a.RiskIndex = risk
   		return nil
   	}); err != nil {
   		s.logger.Warn("failed to save indices", "analysis_id", analysisID, "err", err)
   	}
   }
   
   // Geo 分布来自 InsightResponse
   if insightResp.GeoDistribution != nil {
   	if err := s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
   		a.GeoDistribution = insightResp.GeoDistribution
   		return nil
   	}); err != nil {
   		s.logger.Warn("failed to save geo_distribution", "analysis_id", analysisID, "err", err)
   	}
   }
   ```

3. **Store 层扩展**：
   - `AnalysisResult` 结构体添加 4 字段
   - PG store 的 `put`/`get` 方法处理新列

#### Day 3: API 扩展

**文件**: `platform/internal/api/v1/analyses.go`

**修改 `getAnalysisResult` 响应**：
```go
type AnalysisResultResponse struct {
	// ... 现有字段 ...
	SentimentIndex  float64            `json:"sentiment_index"`
	HeatIndex       float64            `json:"heat_index"`
	RiskIndex       float64            `json:"risk_index"`
	GeoDistribution map[string]int     `json:"geo_distribution"`
}
```

#### Day 4-5: 前端组件

**文件**:
- `web/src/components/IndicesCards.tsx`
- `web/src/components/GeoChart.tsx`
- `web/src/pages/AnalysisDetailPage.tsx`（修改）

**实现**：复制技术设计文档中的组件代码。

**ECharts 地图注意事项**：
1. 确认 `node_modules/echarts/map/json/china.json` 存在
2. 引入：`import 'echarts/map/js/china';`
3. 数据格式：`[{ name: '北京', value: 45 }, ...]`

#### Day 6: 集成测试

**测试场景**：
1. 创建分析任务（关键词包含城市名称）
2. 等待完成
3. 验证三大指数非零
4. 验证 `geo_distribution` 包含预期城市

---

## 测试策略

### 1. 单元测试

**覆盖目标**: 80%+ 代码覆盖率

**关键包**：
- `business/campaign`：Service 层 + Store 层（用 testcontainers-go 或 mock）
- `business/competitor`：Service 层 + 对比逻辑
- `business/analysis`：指数计算函数（`CalculateIndices`）

**运行**：
```bash
cd platform
go test ./internal/business/campaign -count=1 -race
go test ./internal/business/competitor -count=1 -race
go test ./internal/business/analysis -count=1 -race -run TestCalculateIndices
```

### 2. 契约测试

**文件**: `platform/internal/api/v1/contract_test.go`

**新增测试用例**：
- `TestCampaignsContract`：POST/GET campaigns, POST checkpoints
- `TestCompetitorsContract`：POST competitors, POST groups, POST analyses
- `TestIndicesInAnalysisResult`：验证 GET /analyses/:id/result 返回三大指数

**Gap Registry 更新**：
```go
var knownGaps = []Gap{
	// ... 现有 gaps ...
	{ID: "F23-1", Description: "Campaign strategy 生成失败不回补额度", Severity: "P2"},
	{ID: "F24-1", Description: "竞品分析监听器轮询间隔固定 30s，未做事件驱动", Severity: "P3"},
	{ID: "F25-1", Description: "LAC 模型首次加载耗时 ~2s，未做预热", Severity: "P3"},
}
```

### 3. 集成测试

**文件**: `platform/test/integration/v0.2.0_test.go`

**测试场景**：
1. **营销活动全链路**：创建活动 → 等待策略生成 → 添加检查点 → 触发跟踪 → 生成报告
2. **竞品对比全链路**：创建 3 个竞品 → 创建竞品组 → 触发对比分析 → 验证对比报告
3. **指数计算**：创建分析任务 → 验证三大指数 + Geo 分布

### 4. Python 引擎测试

**文件**: `engines/tests/test_strategy_engine.py`

```python
import pytest
from fastapi.testclient import TestClient
from strategy_engine.main import app

client = TestClient(app)

def test_generate_strategy():
    response = client.post("/generate-strategy", json={
        "analysis_id": "test",
        "campaign_name": "Test Campaign",
        "analysis_summary": {
            "keywords": ["雅阁"],
            "sentiment": {"positive": 50, "negative": 30, "neutral": 20},
            "topics": [{"name": "后排空间", "count": 100}],
            "dimensions": [{"dimension": "口碑塑造", "conclusion": "负面较多"}],
        },
    })
    assert response.status_code == 200
    data = response.json()
    assert "strategy_content" in data
    assert len(data["dimensions"]) > 0

def test_compare_checkpoint():
    response = client.post("/compare", json={
        "baseline": {
            "sentiment": {"positive": 45, "negative": 32, "neutral": 23},
            "volume": 345,
            "topics": [{"name": "后排空间", "count": 128}],
        },
        "current": {
            "sentiment": {"positive": 57, "negative": 24, "neutral": 19},
            "volume": 501,
            "topics": [{"name": "后排空间", "count": 189}],
        },
        "checkpoint_name": "7天后",
    })
    assert response.status_code == 200
    data = response.json()
    assert data["trend"] in ["improving", "stable", "declining"]
```

**运行**：
```bash
cd engines
pytest tests/test_strategy_engine.py -v
pytest tests/test_ner.py -v  # 测试 LAC 集成
```

### 5. E2E 测试

**文件**: `web/e2e/v0.2.0.spec.ts`

```typescript
import { test, expect } from '@playwright/test';

test('营销活动完整流程', async ({ page }) => {
  await page.goto('http://localhost:5173');
  await page.fill('input[name="email"]', process.env.E2E_EMAIL);
  await page.fill('input[name="password"]', process.env.E2E_PASSWORD);
  await page.click('button[type="submit"]');
  
  // 创建基线分析
  await page.click('text=新建分析');
  await page.fill('input[name="keywords"]', '雅阁,后排空间');
  await page.click('button:has-text("开始分析")');
  await page.waitForSelector('text=已完成', { timeout: 120000 });
  
  // 创建营销活动
  await page.click('text=营销活动');
  await page.click('button:has-text("创建活动")');
  await page.selectOption('select[name="analysis_id"]', { index: 0 });
  await page.fill('input[name="name"]', 'E2E Test Campaign');
  await page.click('button:has-text("创建")');
  
  // 验证策略生成
  await page.waitForSelector('text=active', { timeout: 60000 });
  await expect(page.locator('text=营销方案建议')).toBeVisible();
});

test('竞品对比流程', async ({ page }) => {
  // 登录 + 创建竞品 + 创建竞品组 + 触发对比 + 验证报告
  // ...
});

test('指数面板展示', async ({ page }) => {
  // 登录 + 进入分析详情 + 验证指数卡片 + 验证地图
  // ...
});
```

**运行**：
```bash
cd web
npm run build
npx playwright test e2e/v0.2.0.spec.ts
```

---

## 部署清单

### 1. 本地构建

```bash
# Go 二进制交叉编译（本地 → Linux）
cd platform
make build  # 生成 bin/yuqing-server, bin/yuqing-worker, bin/yuqing-cli

# 前端构建
cd web
npm ci
npm run build  # 生成 dist/

# Python 依赖打包
cd engines
pip freeze > requirements.txt
```

### 2. 上传到服务器

```bash
# 上传二进制
scp -P 22352 bin/yuqing-* root@101.96.209.90:/opt/yuqing/bin/

# 上传前端
cd web/dist
tar czf dist.tar.gz *
scp -P 22352 dist.tar.gz root@101.96.209.90:/tmp/
ssh -p 22352 root@101.96.209.90 "cd /opt/yuqing/web && tar xzf /tmp/dist.tar.gz"

# 上传 Python 引擎
cd engines
tar czf strategy_engine.tar.gz strategy_engine/
scp -P 22352 strategy_engine.tar.gz root@101.96.209.90:/opt/pangu-source/engines/
```

### 3. 数据库迁移

```bash
# SSH 到服务器
ssh -p 22352 root@101.96.209.90

# 事务试跑（验证语法）
psql yuqing_platform -c "BEGIN; \i /opt/yuqing/migrations/platform/0009_campaigns_and_competitors.sql; ROLLBACK;"
psql yuqing_platform -c "BEGIN; \i /opt/yuqing/migrations/platform/0010_indices_and_geo.sql; ROLLBACK;"

# 执行迁移
cd /opt/yuqing
YUQING_CONFIG=/opt/yuqing/config/config.yaml ./bin/yuqing-cli migrate platform

# 验证
psql yuqing_platform -c "\d campaigns"
psql yuqing_platform -c "\d+ analyses" | grep sentiment_index
```

### 4. Python 依赖安装

```bash
# 激活 venv
cd /opt/yuqing/engines
source venv/bin/activate

# 安装新依赖
pip install lac==2.2.0

# 预热 LAC 模型（避免首次调用超时）
python -c "from lac import LAC; LAC(mode='lac')"
```

### 5. systemd 服务

**新增**: `yuqing-strategy.service`

```bash
# 拷贝 service 文件
cp scripts/systemd/yuqing-strategy.service /etc/systemd/system/

# 重载 + 启动
systemctl daemon-reload
systemctl enable yuqing-strategy
systemctl start yuqing-strategy

# 验证
systemctl status yuqing-strategy
curl http://127.0.0.1:8005/health
```

### 6. 配置更新

**文件**: `/opt/yuqing/config/config.yaml`

```yaml
engines:
  query:
    url: "http://127.0.0.1:8000"
  insight:
    url: "http://127.0.0.1:8002"
  report:
    url: "http://127.0.0.1:8003"
  strategy:
    url: "http://127.0.0.1:8005"  # 新增
```

### 7. 重启服务

```bash
# 重启 server（加载新配置）
systemctl restart yuqing-server

# 重启 worker（启动检查点扫描器 + 竞品监听器）
systemctl restart yuqing-worker

# 重启 insight（加载 LAC）
systemctl restart yuqing-insight
```

### 8. nginx 配置（可选）

如需前端直接访问引擎（不推荐，建议走 Go 层代理）：

```nginx
# /etc/nginx/sites-available/yuqing.conf
location /api/v1/campaigns {
    proxy_pass http://127.0.0.1:8080;
}

location /api/v1/competitors {
    proxy_pass http://127.0.0.1:8080;
}
```

### 9. 健康检查

```bash
# 各引擎健康检查
curl http://127.0.0.1:8000/health  # query
curl http://127.0.0.1:8002/health  # insight
curl http://127.0.0.1:8003/health  # report
curl http://127.0.0.1:8005/health  # strategy

# Go API 健康检查
curl http://127.0.0.1:8080/health

# 前端访问
curl -I https://yuqing2.pangu-cloud.com/
```

---

## 验收标准

### 需求1：营销活动

**功能验收**：
- [ ] 可创建营销活动（基于已完成的舆情分析）
- [ ] AI 生成营销方案建议（Markdown 格式，包含分维度建议）
- [ ] 可添加检查点（时间 + 关键词）
- [ ] 检查点自动触发舆情采集（定时器每小时扫描）
- [ ] 生成跟踪报告（对比基线，展示指标变化 + AI 洞察）

**性能验收**：
- [ ] 策略生成耗时 < 60s（中等复杂度舆情）
- [ ] 检查点扫描器延迟 < 1 小时（定时器间隔）

**数据验收**：
- [ ] 营销活动存入 `campaigns` 表，关联正确的 `analysis_id`
- [ ] 检查点存入 `campaign_checkpoints` 表，`checkpoint_at` 准确
- [ ] 跟踪报告 `metrics` 字段包含情感/声量/话题对比

### 需求2：竞品对比

**功能验收**：
- [ ] 可创建竞品（名称 + 关键词）
- [ ] 可创建竞品组（至少 2 个竞品）
- [ ] 可触发竞品分析（三种模式：analysis/reference/comparison）
- [ ] 生成对比报告（情感/声量/话题/五维雷达）

**性能验收**：
- [ ] 3 个竞品并发采集耗时 < 10 分钟（full 模式）
- [ ] 对比报告生成耗时 < 5s（监听器检测到全部完成后）

**数据验收**：
- [ ] 竞品存入 `competitors` 表，tenant+name 唯一
- [ ] 竞品分析存入 `competitor_analyses` 表，`analysis_ids` 数组长度 = 竞品数量
- [ ] 对比报告 `comparison_report` 包含四个维度数据

### 需求3：指数/Geo

**功能验收**：
- [ ] 分析完成后自动计算三大指数（情感/热度/风险）
- [ ] 指数范围 0-100，计算公式正确
- [ ] 提取地理分布（城市级别），白名单过滤
- [ ] 前端展示指数卡片（三色标签 + 描述）
- [ ] 前端展示中国地图（热力图）

**性能验收**：
- [ ] 指数计算耗时 < 100ms（在 analyzing 步骤内）
- [ ] LAC NER 耗时 < 2s（100 条文档）

**数据验收**：
- [ ] `analyses` 表三大指数列非零（完成的分析）
- [ ] `geo_distribution` 包含至少 1 个城市（如果文档提及）
- [ ] 城市名称在白名单内

### E2E 验收

**E2E 测试套件通过率 ≥ 90%**：
- [ ] `web/e2e/v0.2.0.spec.ts` 全部通过
- [ ] 生产环境冒烟测试通过（手工验证关键路径）

---

## 附录：关键实现细节

### 1. 指数计算公式详解

**情感指数**（Sentiment Index）：
```
SI_raw = (positive% × 100) - (negative% × 50)
SI = max(0, min(100, (SI_raw + 50) / 1.5))

示例：
- 正面 60%, 负面 20%, 中性 20%
  SI_raw = 60 - 10 = 50
  SI = (50 + 50) / 1.5 = 66.7
```

**热度指数**（Heat Index）：
```
HI = log10(doc_count + 1) × 20 + topic_count × 5 + time_decay
time_decay = 10 (24h内) | 5 (7天内) | 2 (30天内) | 0 (更早)

示例：
- 文档 500 条, 话题 12 个, 2天前创建
  HI = log10(501) × 20 + 12 × 5 + 5 = 54 + 60 + 5 = 119 → cap 到 100
```

**风险指数**（Risk Index）：
```
RI = negative% × 100 + sensitive_topic_count × 10
sensitive_topics = ["投诉", "召回", "事故", "质量问题", "安全隐患", "维权"]

示例：
- 负面 35%, 话题包含"投诉"和"质量问题"
  RI = 35 + 2 × 10 = 55
```

### 2. LAC 城市白名单（完整版）

```python
CITY_WHITELIST = {
    # 直辖市
    "北京", "上海", "天津", "重庆",
    # 省会城市
    "广州", "成都", "西安", "武汉", "郑州", "南京", "杭州", "长沙",
    "福州", "南昌", "长春", "石家庄", "哈尔滨", "昆明", "兰州",
    "乌鲁木齐", "贵阳", "南宁", "银川", "呼和浩特", "西宁", "海口",
    "太原", "拉萨", "沈阳", "济南", "合肥",
    # 计划单列市
    "深圳", "大连", "青岛", "宁波", "厦门",
    # 其他重要城市（地级市）
    "苏州", "东莞", "佛山", "无锡", "温州", "珠海", "中山", "惠州",
    "泉州", "嘉兴", "烟台", "徐州", "常州", "南通", "扬州", "绍兴",
    "金华", "台州", "湖州", "保定", "唐山", "秦皇岛", "包头", "呼伦贝尔",
    "赤峰", "通辽", "鞍山", "抚顺", "本溪", "丹东", "锦州", "营口",
    "阜新", "辽阳", "盘锦", "铁岭", "朝阳", "葫芦岛", "吉林", "四平",
    "辽源", "通化", "白山", "松原", "白城", "延边", "齐齐哈尔", "鸡西",
    "鹤岗", "双鸭山", "大庆", "伊春", "佳木斯", "七台河", "牡丹江",
    "黑河", "绥化", "大兴安岭", "淮安", "盐城", "镇江", "泰州", "宿迁",
    "芜湖", "蚌埠", "淮南", "马鞍山", "淮北", "铜陵", "安庆", "黄山",
    "滁州", "阜阳", "宿州", "六安", "亳州", "池州", "宣城", "莆田",
    "三明", "漳州", "南平", "龙岩", "宁德", "景德镇", "萍乡", "九江",
    "新余", "鹰潭", "赣州", "吉安", "宜春", "抚州", "上饶", "淄博",
    "枣庄", "东营", "潍坊", "济宁", "泰安", "威海", "日照", "临沂",
    "德州", "聊城", "滨州", "菏泽", "开封", "洛阳", "平顶山", "安阳",
    "鹤壁", "新乡", "焦作", "濮阳", "许昌", "漯河", "三门峡", "南阳",
    "商丘", "信阳", "周口", "驻马店", "黄石", "十堰", "宜昌", "襄阳",
    "鄂州", "荆门", "孝感", "荆州", "黄冈", "咸宁", "随州", "恩施",
    "株洲", "湘潭", "衡阳", "邵阳", "岳阳", "常德", "张家界", "益阳",
    "郴州", "永州", "怀化", "娄底", "湘西", "韶关", "汕头", "江门",
    "湛江", "茂名", "肇庆", "梅州", "汕尾", "河源", "阳江", "清远",
    "潮州", "揭阳", "云浮", "柳州", "桂林", "梧州", "北海", "防城港",
    "钦州", "贵港", "玉林", "百色", "贺州", "河池", "来宾", "崇左",
    "三亚", "自贡", "攀枝花", "泸州", "德阳", "绵阳", "广元", "遂宁",
    "内江", "乐山", "南充", "眉山", "宜宾", "广安", "达州", "雅安",
    "巴中", "资阳", "阿坝", "甘孜", "凉山", "六盘水", "遵义", "安顺",
    "毕节", "铜仁", "黔西南", "黔东南", "黔南", "曲靖", "玉溪", "保山",
    "昭通", "丽江", "普洱", "临沧", "楚雄", "红河", "文山", "西双版纳",
    "大理", "德宏", "怒江", "迪庆", "金昌", "白银", "天水", "武威",
    "张掖", "平凉", "酒泉", "庆阳", "定西", "陇南", "临夏", "甘南",
    "克拉玛依", "吐鲁番", "哈密", "昌吉", "博尔塔拉", "巴音郭楞",
    "阿克苏", "克孜勒苏", "喀什", "和田", "伊犁", "塔城", "阿勒泰",
}
```

### 3. 营销策略 Prompt 优化建议

**可选增强**：
1. **Few-Shot 示例**：在 Prompt 中加入 1-2 个高质量案例
2. **角色强化**：增加"曾服务过 500 强品牌"等人设细节
3. **格式约束**：用 JSON Schema 约束输出结构（提高解析成功率）
4. **思考链**：要求 LLM 先分析再输出（`Let's think step by step...`）

**降级策略**：
- LLM 超时/失败时，返回基于规则生成的模板化建议
- 维度建议从五维研判结论提取关键词，套用模板

---

**文档版本**: v1.0  
**最后更新**: 2026-09-25  
**审核状态**: 待评审
