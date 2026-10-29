package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/retrieval"
)

func deliveryHint(c *RetrievalCoverage) string {
	if c == nil {
		return ""
	}
	var hint string
	_ = json.Unmarshal(c.Extra["delivery_hint"], &hint)
	return hint
}
func fatalDeliveryError(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, pkgerrors.ErrAccountingDurability) || errors.Is(err, pkgerrors.ErrForbidden) || errors.Is(err, pkgerrors.ErrConflict))
}

func (p *Pipeline) handleResilient(ctx context.Context, msg TaskMessage, a *AnalysisResult) error {
	if err := p.svc.loadResume(ctx, msg.TenantID, a); err != nil {
		return p.fail(ctx, msg, "resume_evidence_invalid", err)
	}
	if err := p.step(ctx, msg, StateFetching, progressFetching); err != nil {
		return p.fail(ctx, msg, "pipeline_error", err)
	}
	req := FetchRequest{RunID: msg.RunID, TenantID: msg.TenantID, AnalysisID: msg.AnalysisID, Keywords: a.Keywords, Sources: a.Sources, ExcludeWords: a.ExcludeWords, DateFrom: a.DateFrom, DateTo: a.DateTo, MaxResults: 50, ResearchIntent: a.ResearchIntent, ResearchIntentVersion: a.ResearchIntentVersion, Title: a.Name, AnalysisType: a.AnalysisType, ScenarioTemplateID: a.ScenarioTemplateID, ResearchQuestion: a.ResearchQuestion, Resilient: true, DeliveryPolicy: a.DeliveryPolicy, SeedURLs: a.SeedURLs, ResumeFromRunID: a.ResumeFromRunID, RetainedCandidates: a.RetainedCandidates}
	var result FetchResult
	var err error
	if f, ok := p.fetcher.(coverageFetcher); ok {
		result, err = f.FetchWithCoverage(ctx, req)
	} else {
		result.Documents, err = p.fetcher.Fetch(ctx, req)
	}
	if fatalDeliveryError(err) || ctx.Err() != nil {
		return p.fail(ctx, msg, "fetch_failed", err)
	}
	reasons := []string{}
	if err != nil {
		reasons = append(reasons, "采集未完成："+err.Error())
	}
	if result.Warning != "" {
		reasons = append(reasons, result.Warning)
	}
	a.RetrievalCoverage = result.Coverage
	if result.Coverage != nil {
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		auditErr := p.svc.saveCoverage(auditCtx, msg, result.Coverage)
		cancel()
		if auditErr != nil {
			return p.fail(ctx, msg, "coverage_store_failed", auditErr)
		}
	}
	docs := result.Documents
	semantic := result.Coverage != nil && result.Coverage.AdmissionVersion == retrieval.SemanticVersion && deliveryHint(result.Coverage) != "brief"
	if semantic {
		if err := retrieval.ValidateResult(result.Coverage, boundaryMaterials(docs), retrieval.Bounds{}, false); err != nil {
			reasons = append(reasons, "语义准入无法核验："+err.Error())
			semantic = false
		}
	}
	if result.Coverage != nil {
		docs = filterSemanticDocuments(docs, a, result.Coverage)
	} else {
		docs, _ = filterFetchedDocuments(docs, a)
	}
	if !semantic {
		for i := range docs {
			docs[i].SemanticClassification = "uncertain"
			docs[i].EvidenceSpans = nil
		}
	}
	if err := p.svc.SaveDocuments(ctx, msg.TenantID, msg.AnalysisID, docs); err != nil {
		return p.fail(ctx, msg, "document_store_failed", err)
	}
	if err := p.svc.saveMaterialSnapshot(ctx, msg, docs); err != nil {
		return p.fail(ctx, msg, "material_snapshot_failed", err)
	}
	direct, background := []Document{}, []Document{}
	if semantic {
		direct, background = partitionSemanticDocuments(docs)
	}
	if result.Coverage != nil {
		result.Coverage.AcceptedCount = len(docs)
		result.Coverage.DirectCount = len(direct)
		result.Coverage.ContextCount = len(background)
		for _, platform := range result.Coverage.PerPlatform {
			source, _ := platform["source"].(string)
			accepted, directCount, contextCount := 0, 0, 0
			for _, d := range docs {
				if d.SourceType == source {
					accepted++
					if d.SemanticClassification == "direct" {
						directCount++
					}
					if d.SemanticClassification == "context" {
						contextCount++
					}
				}
			}
			platform["accepted_count"] = accepted
			platform["direct_count"] = directCount
			platform["context_count"] = contextCount
		}
		if err := p.svc.saveCoverage(ctx, msg, result.Coverage); err != nil {
			return p.fail(ctx, msg, "coverage_store_failed", err)
		}
	}
	p.setDocCount(ctx, msg, len(direct))
	if err := p.step(ctx, msg, StateAnalyzing, progressAnalyze); err != nil {
		return p.fail(ctx, msg, "pipeline_error", err)
	}
	insight := InsightResult{}
	if len(direct) > 0 && p.analyzer != nil {
		reusable := a.ReusableComponents
		if !sameMaterialVersions(reusable, docs) {
			reusable = nil
		}
		insight, err = p.analyzer.Analyze(ctx, InsightRequest{RunID: msg.RunID, TenantID: msg.TenantID, AnalysisID: msg.AnalysisID, AnalysisType: a.AnalysisType, ScenarioTemplateID: a.ScenarioTemplateID, Title: a.Name, DateFrom: a.DateFrom, DateTo: a.DateTo, Documents: direct, ContextDocuments: background, ResearchIntent: a.ResearchIntent, Mode: p.modeForTenant(msg.TenantID), Resilient: true, ReusableComponents: reusable})
		if fatalDeliveryError(err) || ctx.Err() != nil {
			return p.fail(ctx, msg, "insight_failed", err)
		}
		if err != nil {
			reasons = append(reasons, "分析未完成："+err.Error())
		}
		if insight.Warning != "" {
			reasons = append(reasons, insight.Warning)
		}
		if err := p.saveResilientCoverage(ctx, msg, result.Coverage, "insight", insight.MaterialCoverage); err != nil {
			return p.fail(ctx, msg, "coverage_store_failed", err)
		}
	} else if len(docs) > 0 {
		reasons = append(reasons, "材料语义准入或分析服务未完成；保留资料，不推断情绪和风险")
	}
	mode := p.modeForTenant(msg.TenantID)
	full := completeVerifiedInsight(insight, direct, background, mode)
	insight = verifiedPartialInsight(insight, direct, background)
	level := "empty"
	if len(docs) > 0 {
		level = "brief"
	}
	if len(insight.Sentiments) > 0 || len(insight.Dimensions) > 0 {
		level = "limited"
	}
	if full {
		level = "full"
	}
	if level != "full" {
		reasons = append(reasons, "未满足完整研判的全部材料、分类、维度和引用要求；本次报告额度配对回补")
	}
	if len(docs) == 0 {
		reasons = append(reasons, "未取得符合所选来源和日期范围的可用材料")
	}
	if err := p.step(ctx, msg, StateGeneratingReport, progressReport); err != nil {
		return p.fail(ctx, msg, "pipeline_error", err)
	}
	content := ""
	if p.generator != nil {
		unverified := []Document{}
		if !semantic {
			unverified = docs
		}
		out, generateErr := p.generator.Generate(ctx, ReportRequest{RunID: msg.RunID, TenantID: msg.TenantID, AnalysisID: msg.AnalysisID, TemplateID: a.ReportTemplateID, AnalysisType: a.AnalysisType, ScenarioTemplateID: a.ScenarioTemplateID, Title: a.Name, DateFrom: a.DateFrom, DateTo: a.DateTo, Documents: direct, ContextDocuments: background, UnverifiedDocuments: unverified, ResearchIntent: a.ResearchIntent, InsightMaterialCoverage: insight.MaterialCoverage, Sentiments: insight.Sentiments, Topics: insight.Topics, Dimensions: insight.Dimensions, Summary: insight.Summary, Mode: mode, InsightAvailable: insightHasOutput(insight), Resilient: true, DeliveryLevel: level, DeliveryReasons: reasons, DeliveryPolicy: a.DeliveryPolicy})
		if fatalDeliveryError(generateErr) || ctx.Err() != nil {
			return p.fail(ctx, msg, "report_failed", generateErr)
		}
		if generateErr != nil {
			reasons = append(reasons, "报告服务不可用，已保存确定性成果："+generateErr.Error())
		} else {
			if out.Warning != "" {
				reasons = append(reasons, out.Warning)
			}
			valid := out.Content != "" && out.DeliveryLevel == level
			if level == "full" || level == "limited" {
				valid = valid && validReportConsumption(out.MaterialCoverage, docs, insight, full, mode)
			}
			if valid {
				content = out.Content
			}
			if err := p.saveResilientCoverage(ctx, msg, result.Coverage, "report", out.MaterialCoverage); err != nil {
				return p.fail(ctx, msg, "coverage_store_failed", err)
			}
		}
	}
	// An independently escaped renderer also covers Python outages. Full uses
	// exactly the independently verified source components, never unread writing.
	if content == "" {
		content = deterministicDelivery(a, level, reasons, docs, insight)
	}
	if err := p.svc.completeDelivery(ctx, msg, level, reasons, content, insight); err != nil {
		return err
	}
	completed := *a
	completed.State = StateCompleted
	completed.DeliveryLevel = level
	completed.DeliveryReasons = append([]string{}, reasons...)
	completed.ReportContent = content
	completed.FinishedAt = time.Now().UTC()
	if a.DeliveryPolicy == "full_only" && level != "full" {
		completed.State = StateFailed
	}
	if completed.ReportContent != "" {
		if err = p.ensureReportRecord(ctx, msg, &completed); err != nil {
			return err
		}
	}
	if a.DeliveryPolicy == "full_only" && level != "full" {
		return fmt.Errorf("full delivery unavailable; %s artifact preserved", level)
	}
	return nil
}

func expectedDimensions(mode string) []string {
	if mode == "quick" {
		return []string{"heat", "sentiment", "deep_cause"}
	}
	return []string{"background", "heat", "sentiment", "group", "deep_cause"}
}
func phaseCoverage(coverage map[string]any, name string) map[string]any {
	phases, _ := coverage["phases"].([]any)
	for _, raw := range phases {
		phase, _ := raw.(map[string]any)
		if phase["phase"] == name {
			return phase
		}
	}
	return nil
}
func completePhase(coverage map[string]any, name string, docs []Document) bool {
	phase := phaseCoverage(coverage, name)
	if phase == nil {
		return false
	}
	return retrieval.ValidateConsumption(map[string]any{"version": "complete-chunks-v1", "phases": []any{phase}}, boundaryMaterials(docs)) == nil
}
func completeVerifiedInsight(ins InsightResult, direct, background []Document, mode string) bool {
	if len(direct) < 2 || strings.TrimSpace(ins.Summary) == "" || validateSemanticInsight(ins, direct, background) != nil || !topicsGrounded(ins.Topics, direct) {
		return false
	}
	urls := map[string]bool{}
	for _, d := range direct {
		urls[retrieval.URLKey(materialForBoundary(d))] = true
	}
	if len(urls) < 2 {
		return false
	}
	if !completePhase(ins.MaterialCoverage, "classification", direct) {
		return false
	}
	all := append(append([]Document{}, direct...), background...)
	recommendation := false
	byID := map[string]Dimension{}
	for _, d := range ins.Dimensions {
		if _, ok := byID[d.ID]; ok {
			return false
		}
		byID[d.ID] = d
		for _, r := range d.Recommendations {
			stage, _ := r["stage"].(string)
			action, _ := r["action"].(string)
			rationale, _ := r["rationale"].(string)
			if strings.TrimSpace(stage) != "" && strings.TrimSpace(action) != "" && strings.TrimSpace(rationale) != "" {
				recommendation = true
			}
		}
	}
	for _, name := range expectedDimensions(mode) {
		d, ok := byID[name]
		if !ok || !d.Complete || d.Findings == "" || d.DeepRead == "" || len(d.Quotes) == 0 || !completePhase(ins.MaterialCoverage, "dimension:"+name, all) {
			return false
		}
	}
	if !recommendation {
		return false
	}
	summary := phaseCoverage(ins.MaterialCoverage, "summary")
	if summary == nil || summary["failure_code"] != nil && summary["failure_code"] != "" && summary["compiled"] != true {
		return false
	}
	return true
}

func verifiedPartialInsight(ins InsightResult, direct, background []Document) InsightResult {
	out := ins
	out.Sentiments = []Sentiment{}
	out.Dimensions = []Dimension{}
	out.Topics = []Topic{}
	out.Summary = ""
	directIDs := map[string]Document{}
	all := map[string]Document{}
	for _, d := range direct {
		directIDs[d.ID] = d
		all[d.ID] = d
	}
	for _, d := range background {
		all[d.ID] = d
	}
	seen := map[string]bool{}
	for _, s := range ins.Sentiments {
		d, ok := directIDs[s.DocumentID]
		if ok && !seen[s.DocumentID] && (s.Sentiment == "positive" || s.Sentiment == "negative" || s.Sentiment == "neutral") && readDocumentInPhase(ins.MaterialCoverage, "classification", d) {
			out.Sentiments = append(out.Sentiments, s)
			seen[s.DocumentID] = true
		}
	}
	for _, dim := range ins.Dimensions {
		valid := dim.Findings != "" && dim.DeepRead != ""
		for _, q := range dim.Quotes {
			d, ok := all[q.DocumentID]
			text := retrieval.NormalizeQuote(q.Text)
			if !ok || text == "" || q.Source == "" || (!strings.Contains(retrieval.NormalizeQuote(d.Content), text) && !strings.Contains(retrieval.NormalizeQuote(d.Title), text)) || (q.Source != d.SourceType && q.Source != d.SourceName && q.Source != retrieval.SourceLabel(d.SourceType)) {
				valid = false
			}
		}
		complete := completePhase(ins.MaterialCoverage, "dimension:"+dim.ID, append(append([]Document{}, direct...), background...))
		if !complete {
			valid = valid && len(dim.BatchComponents) > 0
			for _, component := range dim.BatchComponents {
				spans, ok := component["material_spans"].([]any)
				if !ok || len(spans) == 0 {
					valid = false
				}
				for _, raw := range spans {
					span, ok := raw.(map[string]any)
					if !ok || !validReadSpan(span, all) {
						valid = false
					}
				}
			}
		}
		if valid {
			out.Dimensions = append(out.Dimensions, dim)
		}
	}
	if topicsGrounded(ins.Topics, direct) && len(out.Sentiments) == len(direct) {
		out.Topics = ins.Topics
	}
	if len(out.Dimensions) == len(ins.Dimensions) && len(out.Sentiments) == len(direct) {
		out.Summary = ins.Summary
	}
	return out
}
func readDocumentInPhase(coverage map[string]any, phase string, d Document) bool {
	p := phaseCoverage(coverage, phase)
	if p == nil {
		return false
	}
	spans, _ := p["read_spans"].([]any)
	filtered := []any{}
	for _, raw := range spans {
		span, _ := raw.(map[string]any)
		if span["document_id"] == d.ID {
			filtered = append(filtered, span)
		}
	}
	return retrieval.ValidateConsumption(map[string]any{"version": "complete-chunks-v1", "phases": []any{map[string]any{"read_spans": filtered, "unprocessed": []any{}}}}, boundaryMaterials([]Document{d})) == nil
}
func validReadSpan(span map[string]any, docs map[string]Document) bool {
	documentID, _ := span["document_id"].(string)
	d, ok := docs[documentID]
	if !ok {
		return false
	}
	number := func(v any) (int, bool) {
		switch n := v.(type) {
		case float64:
			return int(n), n == float64(int(n))
		case int:
			return n, true
		}
		return 0, false
	}
	start, sok := number(span["start"])
	end, eok := number(span["end"])
	return sok && eok && start >= 0 && end > start && end <= utf8.RuneCountInString(d.Content) && span["material_class"] == d.SemanticClassification
}
func validReportConsumption(coverage map[string]any, docs []Document, ins InsightResult, full bool, mode string) bool {
	if retrieval.ValidateConsumption(coverage, boundaryMaterials(docs)) == nil {
		return true
	}
	phase := phaseCoverage(coverage, "report")
	if phase == nil || phase["input_basis"] != "verified_components" {
		return false
	}
	if !full {
		return false
	}
	reads, _ := phase["derived_reads"].([]any)
	names := map[string]bool{}
	for _, r := range reads {
		item, _ := r.(map[string]any)
		name, _ := item["component"].(string)
		names[name] = true
	}
	if !names["summary"] || !names["classification"] {
		return false
	}
	for _, d := range expectedDimensions(mode) {
		if !names["dimension:"+d] {
			return false
		}
	}
	return true
}

func (p *Pipeline) saveResilientCoverage(ctx context.Context, msg TaskMessage, coverage *RetrievalCoverage, stage string, value map[string]any) error {
	if coverage == nil {
		return nil
	}
	return p.svc.setMaterialCoverage(ctx, msg, stage, value)
}
