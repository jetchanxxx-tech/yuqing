package monitorplan

import (
	"context"
	"fmt"
	"strings"
)

type SourceCapability struct {
	Available      bool
	Authorized     bool
	FeatureEnabled bool
}

// SourceChecker must inspect runtime source health, authorization and the
// tenant's plan feature key. Nil checker fails closed.
type SourceChecker func(ctx context.Context, tenantID, planCode, source string) (SourceCapability, error)

type Store interface {
	Create(context.Context, Plan) (*Plan, error)
	Get(context.Context, string, string) (*Plan, error)
	Update(context.Context, string, Plan) (*Plan, error)
	Delete(context.Context, string, string, string, int) error
	List(context.Context, string, int, int) ([]Plan, error)
}

type Service struct {
	store      Store
	sources    SourceChecker
	planExists func(string) bool
}

func NewService(store Store, sources SourceChecker, planExists func(string) bool) *Service {
	return &Service{store: store, sources: sources, planExists: planExists}
}

func (s *Service) Create(ctx context.Context, draft Plan) (*Plan, error) {
	return s.store.Create(ctx, draft)
}
func (s *Service) Get(ctx context.Context, tenantID, id string) (*Plan, error) {
	return s.store.Get(ctx, tenantID, id)
}
func (s *Service) Update(ctx context.Context, tenantID string, draft Plan) (*Plan, error) {
	return s.store.Update(ctx, tenantID, draft)
}

func (s *Service) Delete(ctx context.Context, tenantID, ownerID, id string, revision int) error {
	return s.store.Delete(ctx, tenantID, ownerID, id, revision)
}
func (s *Service) List(ctx context.Context, tenantID string, limit, offset int) ([]Plan, error) {
	return s.store.List(ctx, tenantID, limit, offset)
}

var requiredInput = map[string]string{
	"brand_daily": "brand_name", "product_launch": "product_name", "quality_complaint": "product_name",
	"competitor_update": "competitor_name", "crisis": "scenario", "campaign_review": "scenario",
}

// Preview computes seven draft candidates without storing or executing them.
func (s *Service) Preview(ctx context.Context, req PreviewRequest) (Preview, error) {
	if req.TenantID == "" || s.planExists == nil || !s.planExists(req.PlanCode) {
		return Preview{}, fmt.Errorf("monitorplan: unknown tenant or plan")
	}
	resolved, err := ResolveAnalysisType(req.TemplateID, req.TemplateVersion, req.AnalysisType)
	if req.TemplateID == "" {
		return Preview{}, fmt.Errorf("monitorplan: template_id required")
	}
	if err != nil {
		return Preview{}, fmt.Errorf("monitorplan: invalid template or perspective: %w", err)
	}
	field := requiredInput[req.TemplateID]
	word := strings.TrimSpace(req.Inputs[field])
	if word == "" || len([]rune(word)) > 100 {
		return Preview{}, fmt.Errorf("monitorplan: %s is required (max 100 characters)", field)
	}
	proposed := func(value any, origin string) Candidate {
		return Candidate{State: "proposed", Value: value, Source: origin}
	}
	unavailable := func(origin, reason string) Candidate {
		return Candidate{State: "unavailable", Source: origin, Reason: reason}
	}
	config := map[string]Candidate{
		"keywords":         proposed([]string{word}, "input:"+field),
		"exclude_words":    proposed([]string{}, "template:review_required"),
		"sources":          unavailable("runtime:news", "来源的运行状态、授权和套餐功能尚未全部验证"),
		"monitoring_cycle": unavailable("runtime:scheduler", "持久调度未接通"),
		"risk_tags":        unavailable("runtime:rules", "标签执行器未接通"),
		"alert_rules":      unavailable("runtime:notifications", "预警通知未接通"),
		"report_template":  unavailable("runtime:renderer", "模板渲染未接通"),
	}
	if s.sources != nil {
		capability, checkErr := s.sources(ctx, req.TenantID, req.PlanCode, "news")
		if checkErr == nil && capability.Available && capability.Authorized && capability.FeatureEnabled {
			config["sources"] = proposed([]string{"news"}, "runtime:authorized_source_and_plan_feature")
		}
	}
	warnings := []string{}
	for _, key := range []string{"sources", "monitoring_cycle", "risk_tags", "alert_rules", "report_template"} {
		if config[key].State == "unavailable" {
			warnings = append(warnings, key+": "+config[key].Reason)
		}
	}
	return Preview{AnalysisType: resolved, Config: config, Warnings: warnings}, nil
}

// ResolveAnalysisType preserves a user's perspective separately from the
// template ID. An empty template ID denotes a manual or historical analysis;
// historical empty analysis_type stays empty for the existing generic prompt.
func ResolveAnalysisType(templateID string, version int, selected string) (AnalysisTypeResolution, error) {
	if templateID == "" {
		if version != 0 {
			return AnalysisTypeResolution{}, fmt.Errorf("monitorplan: manual analysis has template version %d", version)
		}
		if selected == "" {
			return AnalysisTypeResolution{Source: "legacy_empty", Explanation: "历史分析未设置视角，沿用通用提示词"}, nil
		}
		if err := ValidateAnalysisType(selected); err != nil {
			return AnalysisTypeResolution{}, err
		}
		return AnalysisTypeResolution{AnalysisType: selected, Source: "manual", Explanation: "手动选择的分析视角"}, nil
	}

	template, ok := LookupTemplate(templateID, version)
	if !ok {
		return AnalysisTypeResolution{}, fmt.Errorf("monitorplan: unknown template %q version %d", templateID, version)
	}
	result := AnalysisTypeResolution{
		TemplateID:      template.ID,
		TemplateVersion: template.Version,
		AnalysisType:    template.DefaultAnalysisType,
		Source:          "template_default",
		Explanation:     fmt.Sprintf("模板 %s v%d 默认分析视角", template.ID, template.Version),
	}
	if selected != "" {
		if err := ValidateAnalysisType(selected); err != nil {
			return AnalysisTypeResolution{}, err
		}
		result.AnalysisType = selected
		result.Source = "user_override"
		result.Explanation = fmt.Sprintf("用户修改模板 %s v%d 的分析视角", template.ID, template.Version)
	}
	return result, nil
}
