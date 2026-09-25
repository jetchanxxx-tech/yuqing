# v0.2.0 技术设计文档

**版本**: v0.2.0  
**作者**: 技术总监  
**日期**: 2026-09-25  
**状态**: 待评审

---

## 目录

1. [概述](#概述)
2. [需求1：营销方案建议与跟踪分析](#需求1营销方案建议与跟踪分析)
3. [需求2：竞品舆情对比](#需求2竞品舆情对比)
4. [需求3：指数/热度可视化 + Geo](#需求3指数热度可视化--geo)
5. [架构集成](#架构集成)
6. [性能考量](#性能考量)
7. [安全考量](#安全考量)
8. [依赖分析](#依赖分析)
9. [迁移策略](#迁移策略)

---

## 概述

本文档描述 v0.2.0 三个新需求的技术实现方案，总工作量 **19-25 人天**。

### 设计原则

1. **延续现有架构**：三层分离（platform/business/engine）保持不变
2. **PostgreSQL 优先**：所有新表直接 PG 实现，不做内存版（MVP 已转 PG）
3. **租户隔离**：所有业务表带 `tenant_id` 列 + 复合索引
4. **额度契约复用**：营销活动/竞品分析复用 `credit.Service` 扣费
5. **渐进式上线**：每个需求独立分支，feature flag gate 到生产

### 工作量分解

| 需求 | 工作量 | 风险等级 |
|------|--------|---------|
| F23: 营销方案建议与跟踪 | 8-10 人天 | 中（新引擎） |
| F24: 竞品舆情对比 | 6-8 人天 | 低（复用现有） |
| F25: 指数/热度/Geo | 5-7 人天 | 中（NER 集成） |

---

## 需求1：营销方案建议与跟踪分析

### 业务流程

```
用户基于舆情分析结果 → 创建营销活动 → AI 生成营销方案建议
   ↓
客户自行执行营销活动
   ↓
用户添加检查点（时间节点 + 关键词） → 系统采集新舆情 → 对比基线 → 生成跟踪报告
```

### 数据库设计

#### 表1: `campaigns`（营销活动）

```sql
-- 迁移: 0009_campaigns_and_competitors.sql (Part 1)
CREATE TABLE campaigns (
    id TEXT PRIMARY KEY,                    -- ULID
    tenant_id TEXT NOT NULL,
    analysis_id TEXT NOT NULL,              -- 关联的基线舆情分析
    name TEXT NOT NULL,                     -- 活动名称（用户输入）
    description TEXT DEFAULT '',            -- 活动描述
    
    -- AI 生成的营销方案建议
    strategy_content TEXT NOT NULL,         -- 方案正文（Markdown）
    strategy_summary TEXT DEFAULT '',       -- 摘要（100字内）
    strategy_dimensions JSONB DEFAULT '[]', -- 维度建议: [{dimension, advice, expected_impact}]
    
    -- 状态机
    status TEXT NOT NULL DEFAULT 'pending', -- pending | generating | active | completed | failed
    
    -- 元数据
    created_by TEXT NOT NULL,               -- 创建用户 UUID
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_campaigns_tenant ON campaigns(tenant_id, created_at DESC);
CREATE INDEX idx_campaigns_analysis ON campaigns(tenant_id, analysis_id);
CREATE INDEX idx_campaigns_status ON campaigns(tenant_id, status);
```

**字段说明**：
- `analysis_id`：基线舆情分析，用于对比前后变化
- `strategy_content`：AI 生成的方案正文（Markdown 格式，前端渲染）
- `strategy_dimensions`：分维度建议，结构：
  ```json
  [
    {
      "dimension": "口碑塑造",
      "advice": "针对'后排空间'话题，建议发起 KOL 深度体验活动...",
      "expected_impact": "预期正面提及率提升 15-20%"
    }
  ]
  ```
- `status`：`pending` → `generating`（调 strategy_engine）→ `active`（可添加检查点）/ `failed`

#### 表2: `campaign_checkpoints`（跟踪检查点）

```sql
CREATE TABLE campaign_checkpoints (
    id TEXT PRIMARY KEY,                    -- ULID
    tenant_id TEXT NOT NULL,
    campaign_id TEXT NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    
    -- 检查点配置
    name TEXT NOT NULL,                     -- 检查点名称（如"上线后7天"）
    keywords TEXT[] NOT NULL,               -- 监测关键词（可选，为空则复用基线关键词）
    checkpoint_at TIMESTAMPTZ NOT NULL,     -- 检查时间点
    
    -- 采集与分析结果
    fetch_analysis_id TEXT,                 -- 自动触发的舆情分析 ID
    status TEXT NOT NULL DEFAULT 'pending', -- pending | fetching | analyzing | completed | failed
    
    -- 对比指标（analyzing → completed 时填充）
    metrics JSONB DEFAULT '{}',             -- 对比指标: {sentiment_delta, volume_delta, topic_changes, ...}
    insights TEXT DEFAULT '',               -- AI 生成的跟踪洞察（100-200字）
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_checkpoints_campaign ON campaign_checkpoints(tenant_id, campaign_id, checkpoint_at);
CREATE INDEX idx_checkpoints_status ON campaign_checkpoints(tenant_id, status);
CREATE INDEX idx_checkpoints_time ON campaign_checkpoints(checkpoint_at) WHERE status = 'pending';
```

**字段说明**：
- `keywords`：可选，为空时复用基线分析的关键词
- `fetch_analysis_id`：自动创建的舆情分析任务（走现有 analysis 管线）
- `metrics`：对比基线的变化指标，结构：
  ```json
  {
    "sentiment_delta": {"positive": +12, "negative": -8, "neutral": -4},
    "volume_delta": {"count": +156, "pct": +45.2},
    "topic_changes": [
      {"topic": "后排空间", "mentions_before": 128, "mentions_after": 189, "delta_pct": +47.7}
    ],
    "heat_index_delta": +23.5
  }
  ```
- `insights`：AI 根据指标变化生成的跟踪洞察（如"'后排空间'话题热度显著提升，正面提及率增加12%，营销策略见效"）

### API 设计

#### 1. 创建营销活动

```http
POST /api/v1/campaigns
Authorization: Bearer <jwt>
Content-Type: application/json

{
  "analysis_id": "01JCABCD1234567890ABCDEFGH",
  "name": "雅阁后排空间口碑优化",
  "description": "针对后排空间负面舆情的营销方案"
}
```

**响应**（202 Accepted）：
```json
{
  "campaign_id": "01JCXYZT9876543210ZYXWVUTS",
  "status": "generating",
  "message": "AI 正在生成营销方案建议，预计 30-60 秒"
}
```

**后端流程**：
1. 校验 `analysis_id` 存在且属于该租户
2. 扣 1 次额度（复用 `credit.Service.TryConsume`）
3. 创建 campaign 记录（status=pending）
4. 发布队列消息到 `campaign.strategy` 主题
5. Pipeline 调用 `strategy_engine` 生成方案
6. 更新 status → active，填充 `strategy_content`

#### 2. 获取营销活动详情

```http
GET /api/v1/campaigns/:id
Authorization: Bearer <jwt>
```

**响应**（200 OK）：
```json
{
  "id": "01JCXYZT9876543210ZYXWVUTS",
  "analysis_id": "01JCABCD1234567890ABCDEFGH",
  "name": "雅阁后排空间口碑优化",
  "status": "active",
  "strategy": {
    "content": "# 营销方案建议\n\n## 一、核心策略...",
    "summary": "聚焦后排空间优势，通过 KOL 体验+用户 UGC 双轮驱动...",
    "dimensions": [
      {
        "dimension": "口碑塑造",
        "advice": "发起 KOL 深度体验活动...",
        "expected_impact": "预期正面提及率提升 15-20%"
      }
    ]
  },
  "checkpoints": [
    {
      "id": "01JCZZZ...",
      "name": "上线后7天",
      "checkpoint_at": "2026-10-02T10:00:00Z",
      "status": "completed",
      "metrics": { "sentiment_delta": {...}, "volume_delta": {...} },
      "insights": "..."
    }
  ],
  "created_at": "2026-09-25T10:00:00Z"
}
```

#### 3. 添加检查点

```http
POST /api/v1/campaigns/:id/checkpoints
Authorization: Bearer <jwt>
Content-Type: application/json

{
  "name": "上线后7天",
  "checkpoint_at": "2026-10-02T10:00:00Z",
  "keywords": ["雅阁", "后排空间", "舒适"]  // 可选
}
```

**响应**（201 Created）：
```json
{
  "checkpoint_id": "01JCZZZ123...",
  "status": "pending",
  "message": "检查点已创建，将在指定时间自动触发舆情采集"
}
```

**后端流程**：
1. 校验 campaign 存在且 status=active
2. 创建 checkpoint 记录（status=pending）
3. 定时器 cron（每小时扫描 `checkpoints` 表，`checkpoint_at <= NOW() AND status='pending'`）
4. 触发时：创建新 analysis（keywords 复用或用户指定）→ 管线运行 → completed 后调用对比逻辑
5. 对比逻辑：读取基线 analysis + 新 analysis → 计算 delta → 调 strategy_engine `/compare` 端点生成 insights
6. 更新 checkpoint: status=completed, metrics, insights

#### 4. 获取跟踪报告

```http
GET /api/v1/campaigns/:id/report
Authorization: Bearer <jwt>
```

**响应**（200 OK）：
```json
{
  "campaign_id": "01JCXYZT...",
  "baseline": {
    "analysis_id": "01JCABCD...",
    "sentiment": {"positive": 45, "negative": 32, "neutral": 23},
    "volume": 345,
    "heat_index": 67.8
  },
  "checkpoints": [
    {
      "name": "上线后7天",
      "checkpoint_at": "2026-10-02T10:00:00Z",
      "sentiment": {"positive": 57, "negative": 24, "neutral": 19},
      "sentiment_delta": {"positive": +12, "negative": -8, "neutral": -4},
      "volume": 501,
      "volume_delta": +156,
      "heat_index": 91.3,
      "heat_delta": +23.5,
      "insights": "..."
    }
  ],
  "overall_summary": "营销活动上线7天后，舆情热度提升34.6%，正面情感占比增加12个百分点..."
}
```

### Engine 设计：`strategy_engine`

**端口**: 8005  
**技术栈**: Python 3.11 + FastAPI + LLM（复用 GLM/DeepSeek 配置）

#### 端点1: 生成营销方案

```http
POST /generate-strategy
Content-Type: application/json

{
  "analysis_id": "01JCABCD...",
  "campaign_name": "雅阁后排空间口碑优化",
  "analysis_summary": {
    "keywords": ["雅阁", "后排空间"],
    "sentiment": {"positive": 45, "negative": 32, "neutral": 23},
    "topics": ["后排空间", "座椅舒适度", ...],
    "dimensions": [
      {"dimension": "口碑塑造", "conclusion": "...", "sentiment": "negative"}
    ]
  }
}
```

**响应**（200 OK）：
```json
{
  "strategy_content": "# 营销方案建议\n\n## 一、核心策略...",
  "strategy_summary": "聚焦后排空间优势...",
  "dimensions": [
    {
      "dimension": "口碑塑造",
      "advice": "发起 KOL 深度体验活动...",
      "expected_impact": "预期正面提及率提升 15-20%"
    }
  ],
  "estimated_budget": "10-15万",
  "execution_timeline": "4-6周"
}
```

**Prompt 模板**（营销策略专家人设）：
```python
STRATEGY_PROMPT = """你是资深品牌营销策略顾问。基于以下舆情分析结果，为客户制定可执行的营销方案建议。

**舆情背景**：
- 品牌/产品：{campaign_name}
- 监测关键词：{keywords}
- 情感分布：正面 {pos}% / 负面 {neg}% / 中性 {neu}%
- 核心话题：{topics}
- 五维研判：{dimensions}

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
...
"""
```

**实现要点**：
- LLM 调用超时 180s（策略生成比洞察快，无需 thinking 模式）
- `max_tokens=4096`，温度 `temperature=0.3`（兼顾创意与稳定）
- 失败降级：返回模板化建议（"建议加强正面内容传播..."）

#### 端点2: 生成跟踪洞察

```http
POST /compare
Content-Type: application/json

{
  "baseline": {
    "sentiment": {"positive": 45, "negative": 32, "neutral": 23},
    "volume": 345,
    "topics": [{"name": "后排空间", "count": 128}]
  },
  "current": {
    "sentiment": {"positive": 57, "negative": 24, "neutral": 19},
    "volume": 501,
    "topics": [{"name": "后排空间", "count": 189}]
  },
  "checkpoint_name": "上线后7天"
}
```

**响应**（200 OK）：
```json
{
  "insights": "'后排空间'话题热度显著提升（+47.7%），正面提及率增加12个百分点，营销策略见效。建议继续加大 KOL 体验内容投放，同时监测竞品动态。",
  "trend": "improving",
  "next_steps": ["持续监测竞品反应", "优化 UGC 激励机制"]
}
```

### Go 层实现

#### 1. Service 层（`business/campaign/`）

**文件**：`platform/internal/business/campaign/service.go`

```go
package campaign

import (
	"context"
	"time"
	
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/queue"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

const topicCampaignStrategy = "campaign.strategy"

type Service struct {
	store        Store
	queue        queue.Queue
	analysisSvc  AnalysisService  // 注入，用于获取基线分析
	credits      CreditReserver    // 注入，用于扣费
}

func NewService(store Store, q queue.Queue, analysisSvc AnalysisService, credits CreditReserver) *Service {
	return &Service{store: store, queue: q, analysisSvc: analysisSvc, credits: credits}
}

// Create 创建营销活动并发布策略生成任务
func (s *Service) Create(ctx context.Context, tenantID, analysisID, name, desc, createdBy string) (string, error) {
	// 1. 校验基线分析存在
	if _, err := s.analysisSvc.Get(ctx, tenantID, analysisID); err != nil {
		return "", pkgerrors.Wrap(pkgerrors.ErrNotFound, "baseline analysis not found")
	}
	
	// 2. 扣费（1次额度）
	campaignID := id.New()
	if err := s.credits.TryConsume(ctx, tenantID, campaignID); err != nil {
		return "", err
	}
	
	// 3. 创建记录（status=pending）
	c := &Campaign{
		ID:          campaignID,
		AnalysisID:  analysisID,
		Name:        name,
		Description: desc,
		Status:      StatusPending,
		CreatedBy:   createdBy,
		CreatedAt:   time.Now(),
	}
	if err := s.store.Create(ctx, tenantID, c); err != nil {
		s.credits.RefundByAnalysis(ctx, tenantID, campaignID) // 回补
		return "", err
	}
	
	// 4. 发布队列消息
	msg := map[string]string{
		"campaign_id": campaignID,
		"tenant_id":   tenantID,
		"analysis_id": analysisID,
	}
	if err := s.queue.Publish(ctx, topicCampaignStrategy, msg); err != nil {
		return campaignID, err // 记录已创建，返回 ID，后台定时器会重试
	}
	
	return campaignID, nil
}

// Get 获取活动详情（含检查点列表）
func (s *Service) Get(ctx context.Context, tenantID, campaignID string) (*Campaign, []Checkpoint, error) {
	c, err := s.store.Get(ctx, tenantID, campaignID)
	if err != nil {
		return nil, nil, err
	}
	
	checkpoints, _ := s.store.ListCheckpoints(ctx, tenantID, campaignID)
	return c, checkpoints, nil
}

// AddCheckpoint 添加检查点
func (s *Service) AddCheckpoint(ctx context.Context, tenantID, campaignID, name string, checkpointAt time.Time, keywords []string) (string, error) {
	c, err := s.store.Get(ctx, tenantID, campaignID)
	if err != nil {
		return "", err
	}
	if c.Status != StatusActive {
		return "", pkgerrors.Wrap(pkgerrors.ErrConflict, "campaign must be active to add checkpoints")
	}
	
	cp := &Checkpoint{
		ID:           id.New(),
		CampaignID:   campaignID,
		Name:         name,
		Keywords:     keywords,
		CheckpointAt: checkpointAt,
		Status:       CheckpointPending,
		CreatedAt:    time.Now(),
	}
	return cp.ID, s.store.CreateCheckpoint(ctx, tenantID, cp)
}
```

#### 2. Pipeline（`business/campaign/pipeline.go`）

```go
package campaign

import (
	"context"
	"encoding/json"
	"log/slog"
	
	"github.com/yuqing/platform/internal/engine"
)

type Pipeline struct {
	store       Store
	analysisSvc AnalysisService
	strategyEng engine.StrategyEngine
	logger      *slog.Logger
}

func NewPipeline(store Store, analysisSvc AnalysisService, strategyEng engine.StrategyEngine, logger *slog.Logger) *Pipeline {
	return &Pipeline{store: store, analysisSvc: analysisSvc, strategyEng: strategyEng, logger: logger}
}

// Handle 处理策略生成任务
func (p *Pipeline) Handle(ctx context.Context, msg []byte) error {
	var task struct {
		CampaignID string `json:"campaign_id"`
		TenantID   string `json:"tenant_id"`
		AnalysisID string `json:"analysis_id"`
	}
	if err := json.Unmarshal(msg, &task); err != nil {
		return err
	}
	
	// 1. 更新状态 → generating
	if err := p.store.UpdateStatus(ctx, task.TenantID, task.CampaignID, StatusGenerating); err != nil {
		return err
	}
	
	// 2. 获取基线分析数据
	analysis, err := p.analysisSvc.Get(ctx, task.TenantID, task.AnalysisID)
	if err != nil {
		p.markFailed(ctx, task.TenantID, task.CampaignID, err)
		return err
	}
	
	// 3. 调用 strategy_engine
	req := engine.StrategyRequest{
		AnalysisID: task.AnalysisID,
		Summary: engine.AnalysisSummary{
			Keywords:   analysis.Keywords,
			Sentiment:  analysis.Sentiment,
			Topics:     analysis.Topics,
			Dimensions: analysis.Dimensions,
		},
	}
	resp, err := p.strategyEng.GenerateStrategy(ctx, req)
	if err != nil {
		p.markFailed(ctx, task.TenantID, task.CampaignID, err)
		return err
	}
	
	// 4. 更新结果 → active
	return p.store.SetStrategy(ctx, task.TenantID, task.CampaignID, resp.Content, resp.Summary, resp.Dimensions)
}

func (p *Pipeline) markFailed(ctx context.Context, tenantID, campaignID string, err error) {
	p.logger.Error("campaign pipeline failed", "campaign_id", campaignID, "err", err)
	p.store.UpdateStatus(ctx, tenantID, campaignID, StatusFailed)
}
```

#### 3. Engine 契约（`engine/strategy.go`）

```go
package engine

import "context"

type StrategyEngine interface {
	GenerateStrategy(ctx context.Context, req StrategyRequest) (*StrategyResponse, error)
	CompareCheckpoint(ctx context.Context, req CompareRequest) (*CompareResponse, error)
}

type StrategyRequest struct {
	AnalysisID   string          `json:"analysis_id"`
	CampaignName string          `json:"campaign_name"`
	Summary      AnalysisSummary `json:"analysis_summary"`
}

type StrategyResponse struct {
	Content           string               `json:"strategy_content"`
	Summary           string               `json:"strategy_summary"`
	Dimensions        []DimensionAdvice    `json:"dimensions"`
	EstimatedBudget   string               `json:"estimated_budget,omitempty"`
	ExecutionTimeline string               `json:"execution_timeline,omitempty"`
}

type DimensionAdvice struct {
	Dimension      string `json:"dimension"`
	Advice         string `json:"advice"`
	ExpectedImpact string `json:"expected_impact"`
}

type CompareRequest struct {
	Baseline       Metrics `json:"baseline"`
	Current        Metrics `json:"current"`
	CheckpointName string  `json:"checkpoint_name"`
}

type Metrics struct {
	Sentiment map[string]int `json:"sentiment"`
	Volume    int            `json:"volume"`
	Topics    []TopicCount   `json:"topics"`
}

type CompareResponse struct {
	Insights  string   `json:"insights"`
	Trend     string   `json:"trend"` // improving | stable | declining
	NextSteps []string `json:"next_steps"`
}
```

#### 4. HTTP Transport（`engine/strategy_http.go`）

```go
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type HTTPStrategyEngine struct {
	baseURL string
	client  *http.Client
}

func NewHTTPStrategyEngine(baseURL string) *HTTPStrategyEngine {
	return &HTTPStrategyEngine{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 180 * time.Second}, // 策略生成超时 3 分钟
	}
}

func (e *HTTPStrategyEngine) GenerateStrategy(ctx context.Context, req StrategyRequest) (*StrategyResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/generate-strategy", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("strategy engine request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("strategy engine error %d: %s", resp.StatusCode, body)
	}
	
	var result StrategyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (e *HTTPStrategyEngine) CompareCheckpoint(ctx context.Context, req CompareRequest) (*CompareResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/compare", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("compare request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("compare error %d: %s", resp.StatusCode, body)
	}
	
	var result CompareResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
```

#### 5. API Handler（`api/v1/campaigns.go`）

```go
package v1

import (
	"net/http"
	"time"
	
	"github.com/gin-gonic/gin"
	"github.com/yuqing/platform/internal/api/middleware"
)

func (s *Services) RegisterCampaigns(r *gin.RouterGroup) {
	campaigns := r.Group("/campaigns")
	campaigns.Use(middleware.Auth(s.auth))
	campaigns.Use(middleware.TenantContext())
	campaigns.Use(middleware.RBAC("user"))
	
	campaigns.POST("", s.createCampaign)
	campaigns.GET("/:id", s.getCampaign)
	campaigns.POST("/:id/checkpoints", s.addCheckpoint)
	campaigns.GET("/:id/report", s.getCampaignReport)
}

type CreateCampaignRequest struct {
	AnalysisID  string `json:"analysis_id" binding:"required"`
	Name        string `json:"name" binding:"required,max=100"`
	Description string `json:"description" binding:"max=500"`
}

func (s *Services) createCampaign(c *gin.Context) {
	tenantID := middleware.MustTenantID(c)
	userID := middleware.MustUserID(c)
	
	var req CreateCampaignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, err)
		return
	}
	
	campaignID, err := s.campaign.Create(c.Request.Context(), tenantID, req.AnalysisID, req.Name, req.Description, userID)
	if err != nil {
		respondError(c, err)
		return
	}
	
	c.JSON(http.StatusAccepted, gin.H{
		"campaign_id": campaignID,
		"status":      "generating",
		"message":     "AI 正在生成营销方案建议，预计 30-60 秒",
	})
}

type AddCheckpointRequest struct {
	Name         string    `json:"name" binding:"required,max=100"`
	CheckpointAt time.Time `json:"checkpoint_at" binding:"required"`
	Keywords     []string  `json:"keywords"`
}

func (s *Services) addCheckpoint(c *gin.Context) {
	tenantID := middleware.MustTenantID(c)
	campaignID := c.Param("id")
	
	var req AddCheckpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, err)
		return
	}
	
	checkpointID, err := s.campaign.AddCheckpoint(c.Request.Context(), tenantID, campaignID, req.Name, req.CheckpointAt, req.Keywords)
	if err != nil {
		respondError(c, err)
		return
	}
	
	c.JSON(http.StatusCreated, gin.H{
		"checkpoint_id": checkpointID,
		"status":        "pending",
		"message":       "检查点已创建，将在指定时间自动触发舆情采集",
	})
}
```

### 定时器实现（`cmd/worker/main.go`）

```go
// CheckpointScanner 定时扫描待触发的检查点（每小时一次）
func CheckpointScanner(ctx context.Context, svc *campaign.Service, analysisSvc *analysis.Service) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 扫描 checkpoint_at <= NOW() AND status='pending' 的检查点
			pending, err := svc.ListPendingCheckpoints(ctx)
			if err != nil {
				slog.Error("scan checkpoints", "err", err)
				continue
			}
			
			for _, cp := range pending {
				// 创建新分析任务
				keywords := cp.Keywords
				if len(keywords) == 0 {
					// 复用基线关键词
					baseline, _ := analysisSvc.Get(ctx, cp.TenantID, cp.BaselineAnalysisID)
					keywords = baseline.Keywords
				}
				
				analysisID, err := analysisSvc.Create(ctx, cp.TenantID, analysis.CreateRequest{
					Keywords: keywords,
					Sources:  []string{}, // 全源
					Mode:     "full",
				}, cp.CreatedBy)
				
				if err != nil {
					slog.Error("create checkpoint analysis", "checkpoint_id", cp.ID, "err", err)
					continue
				}
				
				// 更新检查点：绑定 analysis_id，status → fetching
				svc.BindCheckpointAnalysis(ctx, cp.TenantID, cp.ID, analysisID)
			}
		}
	}
}
```

**集成点**：
- `platform/internal/app/container.go:149`：装配 `campaign.Service`，注入 analysisSvc + creditSvc
- `platform/internal/app/pipeline.go`：注册 `campaign.Pipeline`，订阅 `campaign.strategy` 主题
- `platform/cmd/worker/main.go`：启动 `CheckpointScanner` goroutine

---

## 需求2：竞品舆情对比

### 业务流程

```
用户创建竞品（输入名称+关键词） → 创建竞品组（多个竞品）
   ↓
触发竞品分析（选择竞品组 + 分析模式：分析/参照/对比）
   ↓
系统并发采集各竞品舆情 → 聚合对比 → 生成对比报告
```

### 数据库设计

#### 表1: `competitors`（竞品定义）

```sql
-- 迁移: 0009_campaigns_and_competitors.sql (Part 2)
CREATE TABLE competitors (
    id TEXT PRIMARY KEY,                    -- ULID
    tenant_id TEXT NOT NULL,
    
    name TEXT NOT NULL,                     -- 竞品名称（如"雅阁"、"凯美瑞"）
    keywords TEXT[] NOT NULL,               -- 监测关键词
    description TEXT DEFAULT '',            -- 备注
    
    -- 元数据
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_competitors_tenant ON competitors(tenant_id, created_at DESC);
CREATE UNIQUE INDEX idx_competitors_name ON competitors(tenant_id, name);  -- 同租户竞品名称唯一
```

#### 表2: `competitor_groups`（竞品组）

```sql
CREATE TABLE competitor_groups (
    id TEXT PRIMARY KEY,                    -- ULID
    tenant_id TEXT NOT NULL,
    
    name TEXT NOT NULL,                     -- 组名（如"B级轿车对比"）
    competitor_ids TEXT[] NOT NULL,         -- 竞品 ID 数组（至少 2 个）
    
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_competitor_groups_tenant ON competitor_groups(tenant_id, created_at DESC);
```

#### 表3: `competitor_analyses`（竞品分析）

```sql
CREATE TABLE competitor_analyses (
    id TEXT PRIMARY KEY,                    -- ULID
    tenant_id TEXT NOT NULL,
    group_id TEXT NOT NULL REFERENCES competitor_groups(id) ON DELETE CASCADE,
    
    mode TEXT NOT NULL,                     -- analysis | reference | comparison
    status TEXT NOT NULL DEFAULT 'pending', -- pending | running | completed | failed
    
    -- 各竞品的舆情分析 ID（数组长度 = competitor_ids 长度）
    analysis_ids TEXT[] DEFAULT '{}',       -- [analysis_id_1, analysis_id_2, ...]
    
    -- 对比结果（completed 时填充）
    comparison_report JSONB DEFAULT '{}',   -- 对比维度：情感/话题/声量/五维雷达
    insights TEXT DEFAULT '',               -- AI 生成的对比洞察
    
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_competitor_analyses_tenant ON competitor_analyses(tenant_id, created_at DESC);
CREATE INDEX idx_competitor_analyses_group ON competitor_analyses(group_id);
CREATE INDEX idx_competitor_analyses_status ON competitor_analyses(tenant_id, status);
```

**字段说明**：
- `mode`：
  - `analysis`：独立分析各竞品（并发创建 N 个 analysis 任务）
  - `reference`：参照对比（选一个作为基准，其他与之对比）
  - `comparison`：全局对比（生成综合对比报告）
- `comparison_report`：对比结果，结构：
  ```json
  {
    "sentiment_comparison": [
      {"competitor": "雅阁", "positive": 57, "negative": 24, "neutral": 19},
      {"competitor": "凯美瑞", "positive": 62, "negative": 18, "neutral": 20}
    ],
    "volume_comparison": [
      {"competitor": "雅阁", "volume": 501},
      {"competitor": "凯美瑞", "volume": 623}
    ],
    "topic_comparison": {
      "后排空间": {"雅阁": 189, "凯美瑞": 156},
      "油耗表现": {"雅阁": 78, "凯美瑞": 134}
    },
    "radar_chart": {
      "dimensions": ["口碑塑造", "产品力展示", "用户体验", "品牌影响力", "市场趋势"],
      "competitors": {
        "雅阁": [75, 68, 82, 71, 65],
        "凯美瑞": [81, 72, 78, 85, 70]
      }
    }
  }
  ```

### API 设计

#### 1. 创建竞品

```http
POST /api/v1/competitors
Authorization: Bearer <jwt>
Content-Type: application/json

{
  "name": "凯美瑞",
  "keywords": ["凯美瑞", "Camry"],
  "description": "丰田凯美瑞（B级轿车）"
}
```

**响应**（201 Created）：
```json
{
  "id": "01JCABC...",
  "name": "凯美瑞",
  "keywords": ["凯美瑞", "Camry"]
}
```

#### 2. 列出竞品

```http
GET /api/v1/competitors
Authorization: Bearer <jwt>
```

**响应**（200 OK）：
```json
{
  "competitors": [
    {"id": "01JCABC...", "name": "雅阁", "keywords": ["雅阁", "Accord"]},
    {"id": "01JCDEF...", "name": "凯美瑞", "keywords": ["凯美瑞", "Camry"]}
  ]
}
```

#### 3. 创建竞品组

```http
POST /api/v1/competitor-groups
Authorization: Bearer <jwt>
Content-Type: application/json

{
  "name": "B级轿车对比",
  "competitor_ids": ["01JCABC...", "01JCDEF...", "01JCGHI..."]
}
```

**响应**（201 Created）：
```json
{
  "group_id": "01JCXYZ...",
  "name": "B级轿车对比",
  "competitors": [
    {"id": "01JCABC...", "name": "雅阁"},
    {"id": "01JCDEF...", "name": "凯美瑞"},
    {"id": "01JCGHI...", "name": "天籁"}
  ]
}
```

#### 4. 创建竞品分析

```http
POST /api/v1/competitor-analyses
Authorization: Bearer <jwt>
Content-Type: application/json

{
  "group_id": "01JCXYZ...",
  "mode": "comparison",
  "sources": []  // 可选，默认全源
}
```

**响应**（202 Accepted）：
```json
{
  "analysis_id": "01JCZZZ...",
  "status": "running",
  "message": "正在采集 3 个竞品的舆情数据，预计 5-10 分钟"
}
```

**后端流程**：
1. 校验 group 存在且至少 2 个竞品
2. 扣费：N 个竞品 = N 次额度（`credit.Service.TryConsume` 循环调用）
3. 创建 `competitor_analyses` 记录（status=pending）
4. 并发创建 N 个 analysis 任务（复用 `analysis.Service.Create`，关键词来自 competitors 表）
5. 记录 `analysis_ids` 数组
6. 监听各 analysis 完成：全部 completed → 调对比逻辑 → 更新 comparison_report

#### 5. 获取对比报告

```http
GET /api/v1/competitor-analyses/:id/comparison
Authorization: Bearer <jwt>
```

**响应**（200 OK）：
```json
{
  "analysis_id": "01JCZZZ...",
  "mode": "comparison",
  "status": "completed",
  "comparison": {
    "sentiment_comparison": [...],
    "volume_comparison": [...],
    "topic_comparison": {...},
    "radar_chart": {...}
  },
  "insights": "凯美瑞在情感正面率和声量两个维度领先雅阁，但'后排空间'话题雅阁更具优势...",
  "created_at": "2026-09-25T11:00:00Z"
}
```

### Go 层实现

#### 1. Service 层（`business/competitor/`）

**文件**：`platform/internal/business/competitor/service.go`

```go
package competitor

import (
	"context"
	"fmt"
	
	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/pkg/id"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

type Service struct {
	store       Store
	analysisSvc *analysis.Service
	credits     CreditReserver
}

func NewService(store Store, analysisSvc *analysis.Service, credits CreditReserver) *Service {
	return &Service{store: store, analysisSvc: analysisSvc, credits: credits}
}

// CreateCompetitor 创建竞品定义
func (s *Service) CreateCompetitor(ctx context.Context, tenantID, name string, keywords []string, desc, createdBy string) (string, error) {
	if len(keywords) == 0 {
		return "", pkgerrors.Wrap(pkgerrors.ErrBadRequest, "keywords required")
	}
	
	comp := &Competitor{
		ID:          id.New(),
		Name:        name,
		Keywords:    keywords,
		Description: desc,
		CreatedBy:   createdBy,
	}
	return comp.ID, s.store.CreateCompetitor(ctx, tenantID, comp)
}

// CreateGroup 创建竞品组
func (s *Service) CreateGroup(ctx context.Context, tenantID, name string, competitorIDs []string, createdBy string) (string, error) {
	if len(competitorIDs) < 2 {
		return "", pkgerrors.Wrap(pkgerrors.ErrBadRequest, "at least 2 competitors required")
	}
	
	// 校验所有竞品存在
	for _, cid := range competitorIDs {
		if _, err := s.store.GetCompetitor(ctx, tenantID, cid); err != nil {
			return "", pkgerrors.Wrap(pkgerrors.ErrNotFound, fmt.Sprintf("competitor %s not found", cid))
		}
	}
	
	group := &CompetitorGroup{
		ID:            id.New(),
		Name:          name,
		CompetitorIDs: competitorIDs,
		CreatedBy:     createdBy,
	}
	return group.ID, s.store.CreateGroup(ctx, tenantID, group)
}

// CreateAnalysis 创建竞品分析（并发触发各竞品的舆情采集）
func (s *Service) CreateAnalysis(ctx context.Context, tenantID, groupID, mode string, sources []string, createdBy string) (string, error) {
	// 1. 获取竞品组
	group, err := s.store.GetGroup(ctx, tenantID, groupID)
	if err != nil {
		return "", err
	}
	
	// 2. 扣费：N 个竞品 = N 次额度
	analysisID := id.New()
	for i := 0; i < len(group.CompetitorIDs); i++ {
		if err := s.credits.TryConsume(ctx, tenantID, fmt.Sprintf("%s-%d", analysisID, i)); err != nil {
			// 回补已扣的
			for j := 0; j < i; j++ {
				s.credits.RefundByAnalysis(ctx, tenantID, fmt.Sprintf("%s-%d", analysisID, j))
			}
			return "", err
		}
	}
	
	// 3. 创建竞品分析记录（status=pending）
	ca := &CompetitorAnalysis{
		ID:        analysisID,
		GroupID:   groupID,
		Mode:      mode,
		Status:    StatusPending,
		CreatedBy: createdBy,
	}
	if err := s.store.CreateAnalysis(ctx, tenantID, ca); err != nil {
		return "", err
	}
	
	// 4. 并发创建各竞品的舆情分析任务
	var analysisIDs []string
	for _, cid := range group.CompetitorIDs {
		comp, _ := s.store.GetCompetitor(ctx, tenantID, cid)
		
		aid, err := s.analysisSvc.Create(ctx, tenantID, analysis.CreateRequest{
			Keywords: comp.Keywords,
			Sources:  sources,
			Mode:     "full",
		}, createdBy)
		
		if err != nil {
			// 失败不阻塞其他竞品，记录到 analysis_ids 为空串
			analysisIDs = append(analysisIDs, "")
			continue
		}
		analysisIDs = append(analysisIDs, aid)
	}
	
	// 5. 更新 analysis_ids，status → running
	s.store.BindAnalysisIDs(ctx, tenantID, analysisID, analysisIDs)
	s.store.UpdateStatus(ctx, tenantID, analysisID, StatusRunning)
	
	return analysisID, nil
}

// GenerateComparison 生成对比报告（由监听器在所有 analysis 完成后调用）
func (s *Service) GenerateComparison(ctx context.Context, tenantID, competitorAnalysisID string) error {
	ca, err := s.store.GetAnalysis(ctx, tenantID, competitorAnalysisID)
	if err != nil {
		return err
	}
	
	// 1. 获取各竞品的分析结果
	var results []analysis.AnalysisResult
	for _, aid := range ca.AnalysisIDs {
		if aid == "" {
			continue // 跳过失败的
		}
		a, err := s.analysisSvc.Get(ctx, tenantID, aid)
		if err != nil || a.State != analysis.StateCompleted {
			continue
		}
		results = append(results, *a)
	}
	
	if len(results) < 2 {
		return pkgerrors.Wrap(pkgerrors.ErrConflict, "at least 2 completed analyses required for comparison")
	}
	
	// 2. 聚合对比数据
	report := s.buildComparisonReport(ctx, tenantID, ca.GroupID, results)
	
	// 3. 调 LLM 生成对比洞察（可选，失败降级为空）
	insights := s.generateInsights(ctx, report)
	
	// 4. 更新结果 → completed
	return s.store.SetComparison(ctx, tenantID, competitorAnalysisID, report, insights)
}

func (s *Service) buildComparisonReport(ctx context.Context, tenantID, groupID string, results []analysis.AnalysisResult) map[string]interface{} {
	group, _ := s.store.GetGroup(ctx, tenantID, groupID)
	
	// 情感对比
	sentimentComp := []map[string]interface{}{}
	for i, r := range results {
		comp, _ := s.store.GetCompetitor(ctx, tenantID, group.CompetitorIDs[i])
		sentimentComp = append(sentimentComp, map[string]interface{}{
			"competitor": comp.Name,
			"positive":   r.Sentiment["positive"],
			"negative":   r.Sentiment["negative"],
			"neutral":    r.Sentiment["neutral"],
		})
	}
	
	// 声量对比
	volumeComp := []map[string]interface{}{}
	for i, r := range results {
		comp, _ := s.store.GetCompetitor(ctx, tenantID, group.CompetitorIDs[i])
		volumeComp = append(volumeComp, map[string]interface{}{
			"competitor": comp.Name,
			"volume":     len(r.Documents()),
		})
	}
	
	// 话题对比（横向聚合所有竞品的 topics）
	topicComp := map[string]map[string]int{} // {topic: {competitor: count}}
	for i, r := range results {
		comp, _ := s.store.GetCompetitor(ctx, tenantID, group.CompetitorIDs[i])
		for _, t := range r.Topics {
			if topicComp[t.Name] == nil {
				topicComp[t.Name] = make(map[string]int)
			}
			topicComp[t.Name][comp.Name] = t.Count
		}
	}
	
	// 五维雷达（从 dimensions 提取分数）
	radar := map[string]interface{}{
		"dimensions":  []string{"口碑塑造", "产品力展示", "用户体验", "品牌影响力", "市场趋势"},
		"competitors": map[string][]int{},
	}
	for i, r := range results {
		comp, _ := s.store.GetCompetitor(ctx, tenantID, group.CompetitorIDs[i])
		scores := s.extractRadarScores(r.Dimensions) // 从维度结论提取 0-100 分
		radar["competitors"].(map[string][]int)[comp.Name] = scores
	}
	
	return map[string]interface{}{
		"sentiment_comparison": sentimentComp,
		"volume_comparison":    volumeComp,
		"topic_comparison":     topicComp,
		"radar_chart":          radar,
	}
}

func (s *Service) extractRadarScores(dimensions []analysis.Dimension) []int {
	// 简化版：根据 sentiment 映射分数（正面=80-100，中性=50-70，负面=20-50）
	scores := make([]int, 5)
	for i, d := range dimensions {
		if i >= 5 {
			break
		}
		switch d.Sentiment {
		case "positive":
			scores[i] = 85
		case "neutral":
			scores[i] = 60
		case "negative":
			scores[i] = 35
		}
	}
	return scores
}

func (s *Service) generateInsights(ctx context.Context, report map[string]interface{}) string {
	// TODO: 调 LLM 生成对比洞察（可接入 strategy_engine 或 insight_engine）
	// 失败降级：返回空字符串或模板化文本
	return "竞品对比分析已完成，请查看详细数据。"
}
```

#### 2. 监听器（`business/competitor/listener.go`）

```go
package competitor

import (
	"context"
	"log/slog"
	"time"
	
	"github.com/yuqing/platform/internal/business/analysis"
)

// AnalysisCompletionListener 监听 analysis 完成事件，触发竞品对比生成
type AnalysisCompletionListener struct {
	store       Store
	analysisSvc *analysis.Service
	svc         *Service
	logger      *slog.Logger
}

func NewAnalysisCompletionListener(store Store, analysisSvc *analysis.Service, svc *Service, logger *slog.Logger) *AnalysisCompletionListener {
	return &AnalysisCompletionListener{store: store, analysisSvc: analysisSvc, svc: svc, logger: logger}
}

// Run 轮询检查竞品分析是否所有子任务完成（每 30 秒一次）
func (l *AnalysisCompletionListener) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 扫描 status=running 的竞品分析
			running, err := l.store.ListRunningAnalyses(ctx)
			if err != nil {
				l.logger.Error("scan running competitor analyses", "err", err)
				continue
			}
			
			for _, ca := range running {
				// 检查所有 analysis_ids 是否完成
				allCompleted := true
				for _, aid := range ca.AnalysisIDs {
					if aid == "" {
						continue // 跳过失败的
					}
					a, err := l.analysisSvc.Get(ctx, ca.TenantID, aid)
					if err != nil || a.State != analysis.StateCompleted {
						allCompleted = false
						break
					}
				}
				
				if allCompleted {
					// 触发对比报告生成
					if err := l.svc.GenerateComparison(ctx, ca.TenantID, ca.ID); err != nil {
						l.logger.Error("generate comparison", "ca_id", ca.ID, "err", err)
					}
				}
			}
		}
	}
}
```

**集成点**：
- `platform/internal/app/container.go:155`：装配 `competitor.Service`
- `platform/cmd/worker/main.go`：启动 `AnalysisCompletionListener.Run` goroutine

---

## 需求3：指数/热度可视化 + Geo

### 业务需求

1. **三大指数**：情感指数（Sentiment Index）、热度指数（Heat Index）、风险指数（Risk Index）
2. **城市地理分布**：基于关键词提及的城市名称，聚合到城市级别

### 数据库设计

#### 扩展: `analyses` 表添加四列

```sql
-- 迁移: 0010_indices_and_geo.sql
ALTER TABLE analyses ADD COLUMN sentiment_index FLOAT DEFAULT 0;
ALTER TABLE analyses ADD COLUMN heat_index FLOAT DEFAULT 0;
ALTER TABLE analyses ADD COLUMN risk_index FLOAT DEFAULT 0;
ALTER TABLE analyses ADD COLUMN geo_distribution JSONB DEFAULT '{}';

COMMENT ON COLUMN analyses.sentiment_index IS '情感指数 (0-100)：正面占比加权 + 负面占比惩罚';
COMMENT ON COLUMN analyses.heat_index IS '热度指数 (0-100)：文档数量 + 话题数量 + 时间衰减';
COMMENT ON COLUMN analyses.risk_index IS '风险指数 (0-100)：负面占比 + 话题敏感度';
COMMENT ON COLUMN analyses.geo_distribution IS '地理分布: {"北京": 45, "上海": 38, "深圳": 27, ...}';
```

**计算公式**：

1. **情感指数**（Sentiment Index）：
   ```
   SI = (positive% × 100) - (negative% × 50)
   范围：-50 到 100（归一化到 0-100）
   ```

2. **热度指数**（Heat Index）：
   ```
   HI = log10(doc_count + 1) × 20 + topic_count × 5 + time_decay
   time_decay = 衰减因子（最近24h=10, 7天内=5, 30天内=2, 更早=0）
   范围：0-100（cap 到 100）
   ```

3. **风险指数**（Risk Index）：
   ```
   RI = negative% × 100 + sensitive_topic_bonus
   sensitive_topic_bonus = 敏感话题数量 × 10（如"投诉"、"召回"、"事故"）
   范围：0-100
   ```

### NER 集成：百度 LAC

**依赖**: `baidu-lac` (Apache 2.0 许可证)

**安装**:
```bash
pip install lac
```

**Python 实现**（`engines/common/ner.py`）：

```python
from lac import LAC

class CityExtractor:
    def __init__(self):
        # 加载 LAC 模型（首次运行会下载模型文件 ~20MB）
        self.lac = LAC(mode='lac')
        
        # 中国主要城市白名单（省会 + 直辖市 + 计划单列市 + 部分地级市）
        self.city_whitelist = {
            "北京", "上海", "天津", "重庆",  # 直辖市
            "广州", "深圳", "杭州", "成都", "西安", "武汉", "郑州", "南京",
            "苏州", "长沙", "东莞", "沈阳", "青岛", "合肥", "佛山", "济南",
            "福州", "南昌", "长春", "石家庄", "哈尔滨", "昆明", "兰州",
            "乌鲁木齐", "贵阳", "南宁", "银川", "呼和浩特", "西宁", "海口",
            "太原", "拉萨", "大连", "厦门", "宁波", "温州", "无锡", "珠海",
            # 可扩展...
        }
    
    def extract_cities(self, texts: list[str]) -> dict[str, int]:
        """从文本列表提取城市名称并计数"""
        city_counts = {}
        
        for text in texts:
            if not text or len(text) < 10:
                continue
            
            # LAC 分词 + 词性标注 + 命名实体识别
            lac_result = self.lac.run(text)
            words = lac_result[0]
            tags = lac_result[1]
            
            # 提取地名（LOC 标签）
            for word, tag in zip(words, tags):
                if tag == 'LOC' and word in self.city_whitelist:
                    city_counts[word] = city_counts.get(word, 0) + 1
        
        return city_counts
```

**调用时机**：`insight_engine` 分析完成后，额外调用 `CityExtractor` 处理文档正文。

### Go 层实现

#### 1. 指数计算（`business/analysis/indices.go`）

```go
package analysis

import (
	"math"
	"time"
)

// CalculateIndices 计算三大指数（在 pipeline 的 analyzing 步骤完成后调用）
func CalculateIndices(a *AnalysisResult) (sentiment, heat, risk float64) {
	// 1. 情感指数
	total := a.Sentiment["positive"] + a.Sentiment["negative"] + a.Sentiment["neutral"]
	if total > 0 {
		posPct := float64(a.Sentiment["positive"]) / float64(total)
		negPct := float64(a.Sentiment["negative"]) / float64(total)
		sentiment = (posPct * 100) - (negPct * 50)
		sentiment = math.Max(0, math.Min(100, (sentiment+50)/1.5)) // 归一化到 0-100
	}
	
	// 2. 热度指数
	docCount := len(a.Documents())
	topicCount := len(a.Topics)
	timeDecay := calculateTimeDecay(a.CreatedAt)
	heat = math.Log10(float64(docCount+1))*20 + float64(topicCount)*5 + timeDecay
	heat = math.Min(100, heat)
	
	// 3. 风险指数
	if total > 0 {
		negPct := float64(a.Sentiment["negative"]) / float64(total)
		risk = negPct * 100
		
		// 敏感话题检测
		sensitiveBeans := 0
		for _, topic := range a.Topics {
			if isSensitiveTopic(topic.Name) {
				sensitiveBeans++
			}
		}
		risk += float64(sensitiveBeans) * 10
		risk = math.Min(100, risk)
	}
	
	return
}

func calculateTimeDecay(createdAt time.Time) float64 {
	elapsed := time.Since(createdAt)
	switch {
	case elapsed < 24*time.Hour:
		return 10
	case elapsed < 7*24*time.Hour:
		return 5
	case elapsed < 30*24*time.Hour:
		return 2
	default:
		return 0
	}
}

func isSensitiveTopic(topic string) bool {
	sensitive := []string{"投诉", "召回", "事故", "质量问题", "安全隐患", "维权"}
	for _, s := range sensitive {
		if topic == s {
			return true
		}
	}
	return false
}
```

#### 2. Pipeline 集成（`business/analysis/pipeline.go:L120`）

```go
// analyzing 步骤完成后，计算指数
sentiment, heat, risk := CalculateIndices(a)
if err := s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
	a.SentimentIndex = sentiment
	a.HeatIndex = heat
	a.RiskIndex = risk
	return nil
}); err != nil {
	return err
}
```

#### 3. Geo 分布（Python 引擎返回，Go 层直接存储）

**Insight Engine 修改**（`engines/insight_engine/main.py`）：

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
        "sentiment": {...},
        "topics": [...],
        "dimensions": [...],
        "summary": "...",
        "geo_distribution": geo_distribution  # {"北京": 45, "上海": 38, ...}
    }
```

**Go 层接收**（`engine/insight.go:L45`）：

```go
type InsightResponse struct {
	Sentiment       map[string]int         `json:"sentiment"`
	Topics          []Topic                `json:"topics"`
	Dimensions      []Dimension            `json:"dimensions"`
	Summary         string                 `json:"summary"`
	GeoDistribution map[string]int         `json:"geo_distribution"` // 新增
}
```

**Pipeline 存储**（`business/analysis/pipeline.go:L115`）：

```go
if insightResp.GeoDistribution != nil {
	if err := s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error {
		a.GeoDistribution = insightResp.GeoDistribution
		return nil
	}); err != nil {
		return err
	}
}
```

### API 设计

#### 1. 获取分析结果（扩展现有端点）

```http
GET /api/v1/analyses/:id/result
Authorization: Bearer <jwt>
```

**响应**（200 OK，新增三个指数字段）：
```json
{
  "analysis_id": "01JCABCD...",
  "state": "completed",
  "keywords": ["雅阁", "后排空间"],
  "documents": [...],
  "sentiment": {"positive": 57, "negative": 24, "neutral": 19},
  "topics": [...],
  "dimensions": [...],
  "report": "...",
  
  "sentiment_index": 78.5,
  "heat_index": 91.3,
  "risk_index": 24.0,
  "geo_distribution": {
    "北京": 45,
    "上海": 38,
    "深圳": 27,
    "广州": 19,
    "杭州": 15
  }
}
```

### 前端实现

#### 1. 指数卡片（`web/src/components/IndicesCards.tsx`）

```tsx
import { Card, Row, Col, Statistic } from 'antd';
import { TrendingUp, AlertCircle, Heart } from 'lucide-react';

interface IndicesCardsProps {
  sentimentIndex: number;
  heatIndex: number;
  riskIndex: number;
}

export const IndicesCards: React.FC<IndicesCardsProps> = ({
  sentimentIndex,
  heatIndex,
  riskIndex,
}) => {
  return (
    <Row gutter={16}>
      <Col span={8}>
        <Card>
          <Statistic
            title="情感指数"
            value={sentimentIndex.toFixed(1)}
            suffix="/ 100"
            prefix={<Heart style={{ color: getColor(sentimentIndex) }} />}
            valueStyle={{ color: getColor(sentimentIndex) }}
          />
          <div style={{ fontSize: 12, color: '#999', marginTop: 8 }}>
            {getLabel(sentimentIndex, 'sentiment')}
          </div>
        </Card>
      </Col>
      
      <Col span={8}>
        <Card>
          <Statistic
            title="热度指数"
            value={heatIndex.toFixed(1)}
            suffix="/ 100"
            prefix={<TrendingUp style={{ color: '#1890ff' }} />}
            valueStyle={{ color: '#1890ff' }}
          />
          <div style={{ fontSize: 12, color: '#999', marginTop: 8 }}>
            {getLabel(heatIndex, 'heat')}
          </div>
        </Card>
      </Col>
      
      <Col span={8}>
        <Card>
          <Statistic
            title="风险指数"
            value={riskIndex.toFixed(1)}
            suffix="/ 100"
            prefix={<AlertCircle style={{ color: getRiskColor(riskIndex) }} />}
            valueStyle={{ color: getRiskColor(riskIndex) }}
          />
          <div style={{ fontSize: 12, color: '#999', marginTop: 8 }}>
            {getLabel(riskIndex, 'risk')}
          </div>
        </Card>
      </Col>
    </Row>
  );
};

function getColor(value: number): string {
  if (value >= 70) return '#02b940';
  if (value >= 40) return '#9ca3af';
  return '#FF2442';
}

function getRiskColor(value: number): string {
  if (value >= 70) return '#FF2442';
  if (value >= 40) return '#faad14';
  return '#02b940';
}

function getLabel(value: number, type: 'sentiment' | 'heat' | 'risk'): string {
  if (type === 'sentiment') {
    if (value >= 70) return '口碑优秀';
    if (value >= 40) return '口碑一般';
    return '口碑较差';
  }
  if (type === 'heat') {
    if (value >= 70) return '热度极高';
    if (value >= 40) return '热度中等';
    return '热度较低';
  }
  // risk
  if (value >= 70) return '高风险预警';
  if (value >= 40) return '中等风险';
  return '风险可控';
}
```

#### 2. 地理分布图（`web/src/components/GeoChart.tsx`）

```tsx
import { useEffect, useRef } from 'react';
import * as echarts from 'echarts';
import 'echarts/map/js/china'; // 中国地图

interface GeoChartProps {
  distribution: Record<string, number>;
}

export const GeoChart: React.FC<GeoChartProps> = ({ distribution }) => {
  const chartRef = useRef<HTMLDivElement>(null);
  
  useEffect(() => {
    if (!chartRef.current) return;
    
    const chart = echarts.init(chartRef.current);
    
    // 转换数据格式
    const data = Object.entries(distribution).map(([name, value]) => ({
      name,
      value,
    }));
    
    const option: echarts.EChartsOption = {
      title: {
        text: '地理分布',
        left: 'center',
      },
      tooltip: {
        trigger: 'item',
        formatter: '{b}: {c} 条',
      },
      visualMap: {
        min: 0,
        max: Math.max(...Object.values(distribution)),
        left: 'left',
        top: 'bottom',
        text: ['高', '低'],
        inRange: {
          color: ['#e0f3ff', '#006edd'], // 浅蓝 → 深蓝
        },
        calculable: true,
      },
      series: [
        {
          name: '提及数量',
          type: 'map',
          map: 'china',
          roam: true,
          itemStyle: {
            areaColor: '#f3f3f3',
            borderColor: '#999',
          },
          emphasis: {
            itemStyle: {
              areaColor: '#ffd700',
            },
          },
          data,
        },
      ],
    };
    
    chart.setOption(option);
    
    return () => chart.dispose();
  }, [distribution]);
  
  return <div ref={chartRef} style={{ width: '100%', height: 500 }} />;
};
```

#### 3. 集成到详情页（`web/src/pages/AnalysisDetailPage.tsx`）

```tsx
// 在现有 Tabs 中添加新 Tab
<Tabs.TabPane tab="指数面板" key="indices">
  <IndicesCards
    sentimentIndex={analysis.sentiment_index}
    heatIndex={analysis.heat_index}
    riskIndex={analysis.risk_index}
  />
  <Divider />
  <GeoChart distribution={analysis.geo_distribution || {}} />
</Tabs.TabPane>
```

---

## 架构集成

### 1. 组合根扩展（`platform/internal/app/container.go`）

```go
// Build 函数添加新服务装配（约 L149 之后）

// 营销活动服务
strategyEngine := engine.NewHTTPStrategyEngine(cfg.Engines.Strategy.URL)
campaignStore := campaign.NewPGStore(platformPool)
campaignSvc := campaign.NewService(campaignStore, q, analysisSvc, creditSvc)

// 营销活动 Pipeline
campaignPipeline := campaign.NewPipeline(campaignStore, analysisSvc, strategyEngine, logger)
q.Subscribe(ctx, campaign.TopicCampaignStrategy, campaignPipeline.Handle)

// 竞品服务
competitorStore := competitor.NewPGStore(platformPool)
competitorSvc := competitor.NewService(competitorStore, analysisSvc, creditSvc)

// 竞品监听器（独立 goroutine）
competitorListener := competitor.NewAnalysisCompletionListener(competitorStore, analysisSvc, competitorSvc, logger)
go competitorListener.Run(ctx)

// 检查点扫描器（独立 goroutine）
go campaign.CheckpointScanner(ctx, campaignSvc, analysisSvc)

// 注入到 Services
return &v1.Services{
	auth:       authSvc,
	tenant:     tenantSvc,
	analysis:   analysisSvc,
	report:     reportSvc,
	dashboard:  dashboardSvc,
	alert:      alertSvc,
	apikey:     apiKeySvc,
	usage:      usageMeter,
	credit:     creditSvc,
	trends:     trendsSvc,
	campaign:   campaignSvc,    // 新增
	competitor: competitorSvc,  // 新增
}
```

### 2. 配置扩展（`platform/internal/config/config.go`）

```go
type EngineConfig struct {
	Query    EngineEndpoint `yaml:"query"`
	Media    EngineEndpoint `yaml:"media"`
	Insight  EngineEndpoint `yaml:"insight"`
	Report   EngineEndpoint `yaml:"report"`
	Forum    EngineEndpoint `yaml:"forum"`
	Strategy EngineEndpoint `yaml:"strategy"` // 新增
}
```

**配置文件**（`config/config.yaml`）：

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

### 3. 路由注册（`platform/internal/api/v1/router.go`）

```go
func NewRouter(svc *Services, cfg *config.Config) *gin.Engine {
	// ... 现有路由 ...
	
	// v0.2.0 新增路由
	svc.RegisterCampaigns(v1)       // /campaigns
	svc.RegisterCompetitors(v1)     // /competitors, /competitor-groups, /competitor-analyses
	
	return r
}
```

---

## 性能考量

### 1. 并发控制

- **竞品分析**：N 个竞品并发创建 analysis 任务，单租户最大并发 = 4（复用 `analysis.Service` 的 semaphore）
- **检查点扫描**：定时器间隔 1 小时（生产环境可调整为 30 分钟）
- **竞品对比监听**：轮询间隔 30 秒（可优化为事件驱动：analysis 完成时发布事件）

### 2. 数据库索引

- `campaigns`：`(tenant_id, created_at DESC)`, `(tenant_id, analysis_id)`, `(tenant_id, status)`
- `campaign_checkpoints`：`(tenant_id, campaign_id, checkpoint_at)`, `(checkpoint_at) WHERE status='pending'`
- `competitors`：`(tenant_id, created_at DESC)`, `UNIQUE(tenant_id, name)`
- `competitor_analyses`：`(tenant_id, created_at DESC)`, `(group_id)`, `(tenant_id, status)`

### 3. 缓存策略

- **指数计算**：analysis 完成时计算一次，存入 `analyses` 表，不做实时计算
- **Geo 分布**：LAC 模型首次加载耗时 ~2s，进程内单例复用
- **竞品对比报告**：`comparison_report` JSONB 存储，避免重复计算

### 4. 查询优化

- **竞品对比列表**：`GET /competitor-analyses` 分页查询，`LIMIT 20 OFFSET N`
- **检查点扫描**：部分索引 `WHERE status='pending'`，只扫描待触发的记录
- **城市白名单**：内存 set 查找 O(1)，避免全量 LAC 结果

---

## 安全考量

### 1. 权限控制

- **营销活动/竞品**：所有端点经 `middleware.RBAC("user")` 校验，租户隔离经 `tenant_id` 列
- **检查点触发**：定时器由 `cmd/worker` 运行，不暴露 HTTP 端点
- **额度消耗**：Create 时扣费，失败/取消时回补，防超卖

### 2. 输入校验

- **关键词数组**：最多 10 个关键词，每个不超过 50 字符
- **竞品组**：至少 2 个竞品，最多 10 个（防爆炸式并发）
- **检查点时间**：`checkpoint_at` 不能早于当前时间
- **城市名称**：LAC 提取后经白名单过滤，防注入

### 3. 速率限制

- **营销活动创建**：同租户 10 次/小时（复用现有 `middleware.RateLimit`）
- **竞品分析创建**：同租户 5 次/小时（单次最多 10 个竞品 = 10 次分析）
- **检查点添加**：同活动最多 20 个检查点

### 4. 数据隔离

- **租户隔离**：所有表带 `tenant_id` 列 + 复合索引
- **用户归属**：`created_by` 字段记录创建用户 UUID
- **级联删除**：`campaign_checkpoints` 和 `competitor_analyses` 设置 `ON DELETE CASCADE`

---

## 依赖分析

### 1. 新增 Python 依赖

**文件**: `engines/requirements.txt`

```txt
# 现有依赖
fastapi==0.115.0
uvicorn[standard]==0.32.0
scrapling[fetchers]==0.2.5
openai==1.54.3
python-docx==1.1.2

# v0.2.0 新增
lac==2.2.0  # 百度 LAC，Apache 2.0 许可证
```

**安装验证**:
```bash
cd engines
pip install -r requirements.txt
python -c "from lac import LAC; print('LAC OK')"
```

### 2. 新增 Go 依赖

无新增外部依赖（复用现有 pgx/gin/jwt 等）。

### 3. 前端依赖

**文件**: `web/package.json`

```json
{
  "dependencies": {
    "echarts": "^5.5.0"  // 已有，确认包含 china.js 地图
  }
}
```

**中国地图数据**:
```bash
# 确认 node_modules/echarts/map/json/china.json 存在
ls web/node_modules/echarts/map/json/china.json
```

### 4. systemd 服务

**新增**: `scripts/systemd/yuqing-strategy.service`

```ini
[Unit]
Description=Yuqing Strategy Engine
After=network.target

[Service]
Type=simple
User=yuqing
WorkingDirectory=/opt/pangu-source
Environment="PYTHONPATH=/opt/pangu-source"
EnvironmentFile=/opt/yuqing/config/engines.env
ExecStart=/opt/yuqing/engines/venv/bin/uvicorn engines.strategy_engine.main:app --host 127.0.0.1 --port 8005
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

**部署检查清单**:
- [ ] `engines/strategy_engine/` 目录及 `main.py` 文件
- [ ] `yuqing-strategy.service` 安装到 `/etc/systemd/system/`
- [ ] `systemctl daemon-reload && systemctl enable --now yuqing-strategy`
- [ ] 端口 8005 监听确认：`curl http://127.0.0.1:8005/health`

---

## 迁移策略

### 迁移文件顺序

| 文件 | 内容 | 依赖 |
|------|------|------|
| `0009_campaigns_and_competitors.sql` | campaigns, campaign_checkpoints, competitors, competitor_groups, competitor_analyses | 0008 |
| `0010_indices_and_geo.sql` | analyses 表添加 4 列（三大指数 + geo_distribution） | 0008 |

### 迁移脚本

#### 0009_campaigns_and_competitors.sql

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE campaigns (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    analysis_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    strategy_content TEXT NOT NULL DEFAULT '',
    strategy_summary TEXT DEFAULT '',
    strategy_dimensions JSONB DEFAULT '[]',
    status TEXT NOT NULL DEFAULT 'pending',
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_campaigns_tenant ON campaigns(tenant_id, created_at DESC);
CREATE INDEX idx_campaigns_analysis ON campaigns(tenant_id, analysis_id);
CREATE INDEX idx_campaigns_status ON campaigns(tenant_id, status);

CREATE TABLE campaign_checkpoints (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    campaign_id TEXT NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    keywords TEXT[] NOT NULL DEFAULT '{}',
    checkpoint_at TIMESTAMPTZ NOT NULL,
    fetch_analysis_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    metrics JSONB DEFAULT '{}',
    insights TEXT DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_checkpoints_campaign ON campaign_checkpoints(tenant_id, campaign_id, checkpoint_at);
CREATE INDEX idx_checkpoints_status ON campaign_checkpoints(tenant_id, status);
CREATE INDEX idx_checkpoints_time ON campaign_checkpoints(checkpoint_at) WHERE status = 'pending';

CREATE TABLE competitors (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    keywords TEXT[] NOT NULL,
    description TEXT DEFAULT '',
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_competitors_tenant ON competitors(tenant_id, created_at DESC);
CREATE UNIQUE INDEX idx_competitors_name ON competitors(tenant_id, name);

CREATE TABLE competitor_groups (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    competitor_ids TEXT[] NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_competitor_groups_tenant ON competitor_groups(tenant_id, created_at DESC);

CREATE TABLE competitor_analyses (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    group_id TEXT NOT NULL REFERENCES competitor_groups(id) ON DELETE CASCADE,
    mode TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    analysis_ids TEXT[] DEFAULT '{}',
    comparison_report JSONB DEFAULT '{}',
    insights TEXT DEFAULT '',
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_competitor_analyses_tenant ON competitor_analyses(tenant_id, created_at DESC);
CREATE INDEX idx_competitor_analyses_group ON competitor_analyses(group_id);
CREATE INDEX idx_competitor_analyses_status ON competitor_analyses(tenant_id, status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS competitor_analyses;
DROP TABLE IF EXISTS competitor_groups;
DROP TABLE IF EXISTS competitors;
DROP TABLE IF EXISTS campaign_checkpoints;
DROP TABLE IF EXISTS campaigns;
-- +goose StatementEnd
```

#### 0010_indices_and_geo.sql

```sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE analyses ADD COLUMN sentiment_index FLOAT DEFAULT 0;
ALTER TABLE analyses ADD COLUMN heat_index FLOAT DEFAULT 0;
ALTER TABLE analyses ADD COLUMN risk_index FLOAT DEFAULT 0;
ALTER TABLE analyses ADD COLUMN geo_distribution JSONB DEFAULT '{}';

COMMENT ON COLUMN analyses.sentiment_index IS '情感指数 (0-100): 正面占比加权 + 负面占比惩罚';
COMMENT ON COLUMN analyses.heat_index IS '热度指数 (0-100): 文档数量 + 话题数量 + 时间衰减';
COMMENT ON COLUMN analyses.risk_index IS '风险指数 (0-100): 负面占比 + 话题敏感度';
COMMENT ON COLUMN analyses.geo_distribution IS '地理分布: {"北京": 45, "上海": 38, ...}';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE analyses DROP COLUMN IF EXISTS sentiment_index;
ALTER TABLE analyses DROP COLUMN IF EXISTS heat_index;
ALTER TABLE analyses DROP COLUMN IF EXISTS risk_index;
ALTER TABLE analyses DROP COLUMN IF EXISTS geo_distribution;
-- +goose StatementEnd
```

### 迁移执行顺序

```bash
# 本地测试（PostgreSQL 必须运行）
cd platform
YUQING_CONFIG=../config/config.yaml go run ./cmd/cli migrate platform

# 生产部署（按 RUNBOOK §9 流程）
# 1. 编译 CLI（本地交叉编译）
make build  # 生成 bin/yuqing-cli

# 2. 上传到服务器
scp bin/yuqing-cli user@yuqing2:/opt/yuqing/bin/

# 3. psql 事务试跑（验证语法）
psql $YUQING_TEST_PG_URL -c "BEGIN; \i platform/migrations/platform/0009_campaigns_and_competitors.sql; ROLLBACK;"
psql $YUQING_TEST_PG_URL -c "BEGIN; \i platform/migrations/platform/0010_indices_and_geo.sql; ROLLBACK;"

# 4. 执行迁移
ssh user@yuqing2
cd /opt/yuqing
YUQING_CONFIG=/opt/yuqing/config/config.yaml ./bin/yuqing-cli migrate platform

# 5. 验证
psql yuqing_platform -c "\d campaigns"
psql yuqing_platform -c "\d+ analyses" | grep sentiment_index
```

---

## 总结

本技术设计文档描述了 v0.2.0 三个新需求的完整实现方案：

1. **需求1：营销方案建议与跟踪分析**
   - 新增 `campaigns` + `campaign_checkpoints` 两表
   - 新引擎 `strategy_engine`（端口 8005）
   - 定时器扫描检查点，自动触发舆情采集
   - 工作量：8-10 人天

2. **需求2：竞品舆情对比**
   - 新增 `competitors` + `competitor_groups` + `competitor_analyses` 三表
   - 复用现有 `analysis` 管线，并发采集
   - 监听器轮询检测全部完成，生成对比报告
   - 工作量：6-8 人天

3. **需求3：指数/热度可视化 + Geo**
   - `analyses` 表扩展 4 列（三大指数 + geo_distribution）
   - 集成百度 LAC（NER）提取城市名称
   - 前端 ECharts 中国地图渲染
   - 工作量：5-7 人天

**总工作量**: 19-25 人天  
**风险等级**: 中（新引擎 + NER 集成）  
**推荐排期**: 3-4 周（包含测试与联调）

下一步：请查看 `DEVELOPMENT_GUIDE.md`（开发计划）和 `RISK_ASSESSMENT.md`（风险评估）。
