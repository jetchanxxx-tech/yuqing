package app

import (
	"testing"
	"time"

	"github.com/yuging/platform/internal/config"
)

// 回归背景：管线总预算曾直接取 engines.query.timeout —— 那是「单个采集请求」
// 的预算。接入 insight/report 两个 LLM 阶段后，采集 + 两次分析 + 一次报告
// 共用同一份 deadline，慢分析会让 p.step 拿到 ctx.Err() 并把任务判为
// failed(timeout)，把「分析失败不致命」的设计变成「任务失败、结果不可见」。
//
// 正确语义：各阶段各有自己的预算（与 config 中的 timeout 一致），
// 管线总预算 = 已接线阶段的预算之和。
func TestPipelineBudget_sumsConfiguredStageBudgets(t *testing.T) {
	tests := []struct {
		name       string
		queryURL   string
		queryTmo   string
		insightURL string
		insightTmo string
		reportURL  string
		reportTmo  string
		want       time.Duration
	}{
		{
			name:     "示例配置：60s 采集 + 120s 分析 + 300s 报告",
			queryURL: "http://localhost:8000", queryTmo: "60s",
			insightURL: "http://localhost:8002", insightTmo: "120s",
			reportURL: "http://localhost:8003", reportTmo: "300s",
			want: 480 * time.Second,
		},
		{
			name:     "未接线 insight/report：超时值仍在配置里但不计入",
			queryURL: "http://localhost:8000", queryTmo: "60s",
			insightTmo: "120s", reportTmo: "300s",
			want: 60 * time.Second,
		},
		{
			name:     "只接线 insight",
			queryURL: "http://localhost:8000", queryTmo: "90s",
			insightURL: "http://localhost:8002", insightTmo: "120s",
			reportTmo: "300s",
			want:      210 * time.Second,
		},
		{
			name:     "全部为空：返回 0，交由 NewPipeline 兜底 3 分钟",
			queryTmo: "60s",
			want:     0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Engines.Query.URL = tc.queryURL
			cfg.Engines.Query.Timeout = tc.queryTmo
			cfg.Engines.Insight.URL = tc.insightURL
			cfg.Engines.Insight.Timeout = tc.insightTmo
			cfg.Engines.Report.URL = tc.reportURL
			cfg.Engines.Report.Timeout = tc.reportTmo

			got := pipelineBudget(cfg)
			if got != tc.want {
				t.Errorf("pipelineBudget() = %v, want %v", got, tc.want)
			}
		})
	}
}

// 接线后总预算必须严格大于采集预算 —— 否则分析阶段仍会在报告前耗尽 deadline。
func TestPipelineBudget_exceedsQueryBudgetWhenEnginesConfigured(t *testing.T) {
	cfg := &config.Config{}
	cfg.Engines.Query.URL = "http://localhost:8000"
	cfg.Engines.Query.Timeout = "60s"
	cfg.Engines.Insight.URL = "http://localhost:8002"
	cfg.Engines.Insight.Timeout = "120s"
	cfg.Engines.Report.URL = "http://localhost:8003"
	cfg.Engines.Report.Timeout = "300s"

	if got := pipelineBudget(cfg); got <= 60*time.Second {
		t.Errorf("pipelineBudget() = %v, must exceed the 60s query budget once insight/report are wired",
			got)
	}
}

// 非法超时值只跳过该项，不影响其他已接线的阶段。
func TestPipelineBudget_ignoresInvalidEntryOnly(t *testing.T) {
	cfg := &config.Config{}
	cfg.Engines.Query.URL = "http://localhost:8000"
	cfg.Engines.Query.Timeout = "60s"
	cfg.Engines.Insight.URL = "http://localhost:8002"
	cfg.Engines.Insight.Timeout = "不是时长"
	cfg.Engines.Report.URL = "http://localhost:8003"
	cfg.Engines.Report.Timeout = "300s"

	if got := pipelineBudget(cfg); got != 360*time.Second {
		t.Errorf("pipelineBudget() = %v, want 6m (60s + 300s)", got)
	}
}
