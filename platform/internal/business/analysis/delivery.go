package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"reflect"
	"strings"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/retrieval"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

func validateDeliveryInputs(req *CreateAnalysisRequest) error {
	if req.DeliveryPolicy == "" && os.Getenv("YUQING_RESILIENT_DELIVERY") == "1" {
		req.DeliveryPolicy = "best_available"
	}
	if req.DeliveryPolicy != "" && req.DeliveryPolicy != "best_available" && req.DeliveryPolicy != "full_only" {
		return pkgerrors.Wrap(pkgerrors.ErrBadRequest, "unsupported delivery_policy")
	}
	if len(req.SeedURLs) > 10 {
		return pkgerrors.Wrap(pkgerrors.ErrBadRequest, "at most10 seed URLs are allowed")
	}
	seen := map[string]bool{}
	for _, raw := range req.SeedURLs {
		source, err := retrieval.URLSource(raw)
		if err != nil || len(raw) > 4096 {
			return pkgerrors.Wrap(pkgerrors.ErrBadRequest, "seed URL must be a public selected source URL")
		}
		selected := false
		for _, s := range req.Sources {
			if source == s {
				selected = true
			}
		}
		if !selected || seen[raw] {
			return pkgerrors.Wrap(pkgerrors.ErrBadRequest, "seed URL must be unique and match a selected source")
		}
		seen[raw] = true
	}
	return nil
}
func resilient(a *AnalysisResult) bool {
	return a.DeliveryPolicy == "best_available" || a.DeliveryPolicy == "full_only"
}
func nonFullDelivery(level string) bool {
	return level == "limited" || level == "brief" || level == "empty"
}
func (s *Service) SetModelIdentity(fn func() string) { s.modelIdentity = fn }
func (s *Service) currentModelFingerprint() string {
	if s.modelIdentity == nil {
		return ""
	}
	identity := s.modelIdentity()
	if identity == "" || identity == "\x00" {
		return ""
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}
func deliveryInputSnapshot(a *AnalysisResult) map[string]any {
	return map[string]any{"name": a.Name, "analysis_type": a.AnalysisType, "scenario_template_id": a.ScenarioTemplateID, "research_question": a.ResearchQuestion, "keywords": nonNullStrings(a.Keywords), "sources": nonNullStrings(a.Sources), "exclude_words": nonNullStrings(a.ExcludeWords), "date_from": a.DateFrom, "date_to": a.DateTo, "seed_urls": nonNullStrings(a.SeedURLs)}
}
func insightFromAnalysis(a *AnalysisResult) InsightResult {
	return InsightResult{Summary: a.Summary, Sentiments: a.Sentiments, Topics: a.Topics, Dimensions: a.Dimensions, MaterialCoverage: a.InsightMaterialCoverage, Warning: a.Warning, BatchComponents: a.InsightBatchComponents}
}

// Resume always performs normal admission. Old debt and snapshots are never
// reopened, even when the source run was refunded.
func (s *Service) Resume(ctx context.Context, tenantID, analysisID string, actor billingpolicy.Actor) error {
	actor.Permission = "analyses:rerun"
	if _, err := s.Get(ctx, tenantID, analysisID); err != nil {
		return err
	}
	if _, ok := s.store.(*pgStore); ok {
		return s.admitPG(ctx, tenantID, nil, analysisID, actor, true)
	}
	a, err := s.Get(ctx, tenantID, analysisID)
	if err != nil {
		return err
	}
	old := a.CurrentRunID
	if err = s.Rerun(ctx, tenantID, analysisID, actor); err != nil {
		return err
	}
	return s.store.mutate(ctx, tenantID, analysisID, func(a *AnalysisResult) error { a.ResumeFromRunID = old; return nil })
}

func (s *Service) loadResume(ctx context.Context, tenantID string, a *AnalysisResult) error {
	if a.ResumeFromRunID == "" {
		return nil
	}
	pg, ok := s.store.(*pgStore)
	if !ok {
		return nil
	}
	var fingerprint string
	var inputs, insight, materials []byte
	if err := pg.pool.QueryRow(ctx, `SELECT model_fingerprint,inputs_snapshot,insight_snapshot,materials_snapshot FROM analysis_runs WHERE id=$1 AND tenant_id=$2 AND analysis_id=$3 AND state IN ('completed','failed','canceled')`, a.ResumeFromRunID, tenantID, a.ID).Scan(&fingerprint, &inputs, &insight, &materials); err != nil {
		return err
	}
	var oldInputs map[string]any
	var currentInputs map[string]any
	_ = json.Unmarshal(inputs, &oldInputs)
	_ = json.Unmarshal(marshalJSON(deliveryInputSnapshot(a)), &currentInputs)
	if !reflect.DeepEqual(oldInputs, currentInputs) {
		return nil
	}
	rows, err := pg.pool.Query(ctx, `SELECT metadata,content,content_sha256 FROM analysis_candidate_materials WHERE run_id=$1 AND tenant_id=$2 AND analysis_id=$3 ORDER BY created_at,document_id LIMIT 100`, a.ResumeFromRunID, tenantID, a.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var content, hash string
		if err = rows.Scan(&raw, &content, &hash); err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(content))
		if hex.EncodeToString(sum[:]) != hash {
			return fmt.Errorf("retained material hash mismatch")
		}
		var item map[string]any
		if err = json.Unmarshal(raw, &item); err != nil {
			return err
		}
		item["content"] = content
		item["content_sha256"] = hash
		a.RetainedCandidates = append(a.RetainedCandidates, item)
	}
	if len(a.RetainedCandidates) == 0 {
		var oldDocs []Document
		if err = json.Unmarshal(materials, &oldDocs); err != nil {
			return err
		}
		for _, d := range oldDocs {
			var item map[string]any
			_ = json.Unmarshal(marshalJSON(d), &item)
			sum := sha256.Sum256([]byte(d.Content))
			item["content_sha256"] = hex.EncodeToString(sum[:])
			item["channel"] = "retained"
			a.RetainedCandidates = append(a.RetainedCandidates, item)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if fingerprint != "" && fingerprint == a.ModelFingerprint {
		var prior map[string]any
		if err = json.Unmarshal(insight, &prior); err != nil {
			return err
		}
		prior["_materials_snapshot"] = json.RawMessage(materials)
		a.ReusableComponents = prior
	}
	return nil
}
func sameMaterialVersions(previous map[string]any, docs []Document) bool {
	raw, ok := previous["_materials_snapshot"].(json.RawMessage)
	if !ok {
		return false
	}
	var old []Document
	if json.Unmarshal(raw, &old) != nil || len(old) != len(docs) {
		return false
	}
	byID := map[string]Document{}
	for _, d := range old {
		byID[d.ID] = d
	}
	for _, d := range docs {
		o, ok := byID[d.ID]
		if !ok || o.Content != d.Content || o.URL != d.URL || o.FinalURL != d.FinalURL || o.PublishedAt != d.PublishedAt || o.SourceType != d.SourceType || o.SemanticClassification != d.SemanticClassification {
			return false
		}
	}
	delete(previous, "_materials_snapshot")
	return true
}

func (s *Service) saveMaterialSnapshot(ctx context.Context, msg TaskMessage, docs []Document) error {
	pg, ok := s.store.(*pgStore)
	if !ok || msg.RunID == "" {
		return nil
	}
	tag, err := pg.pool.Exec(ctx, `UPDATE analysis_runs r SET materials_snapshot=$4 FROM analyses a WHERE r.id=$1 AND r.tenant_id=$2 AND r.analysis_id=$3 AND a.id=r.analysis_id AND a.tenant_id=r.tenant_id AND a.current_run_id=r.id AND a.state NOT IN ('completed','failed','canceled')`, msg.RunID, msg.TenantID, msg.AnalysisID, marshalJSON(docs))
	if err == nil && tag.RowsAffected() != 1 {
		return pkgerrors.ErrConflict
	}
	return err
}

// completeDelivery atomically commits grade, artifact, run snapshot, and the
// paired refund through the existing PG mutation transaction.
func (s *Service) completeDelivery(ctx context.Context, msg TaskMessage, level string, reasons []string, content string, insight InsightResult) error {
	err := s.store.mutate(ctx, msg.TenantID, msg.AnalysisID, func(a *AnalysisResult) error {
		if IsTerminal(string(a.State)) {
			return pkgerrors.ErrConflict
		}
		a.DeliveryLevel = level
		a.DeliveryReasons = append([]string{}, reasons...)
		a.ReportContent = content
		a.ReportID = "run:" + msg.RunID
		a.Summary = insight.Summary
		a.Sentiments = insight.Sentiments
		a.Topics = insight.Topics
		a.Dimensions = insight.Dimensions
		a.InsightBatchComponents = insight.BatchComponents
		a.InsightMaterialCoverage = insight.MaterialCoverage
		a.Warning = strings.Join(reasons, "；")
		a.State = StateCompleted
		a.Progress = 100
		a.FinishedAt = time.Now().UTC()
		if a.DeliveryPolicy == "full_only" && level != "full" {
			a.State = StateFailed
			a.ErrorCode = "full_delivery_unavailable"
		}
		return nil
	})
	if err == nil && nonFullDelivery(level) {
		s.refundCredit(ctx, msg.TenantID, msg.AnalysisID)
	}
	return err
}

func deterministicDelivery(a *AnalysisResult, level string, reasons []string, docs []Document, ins InsightResult) string {
	var b strings.Builder
	esc := html.EscapeString
	label := map[string]string{"full": "完整研判", "limited": "受限研判", "brief": "资料简报", "empty": "空结果回执"}[level]
	b.WriteString("<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><title>" + esc(a.Name) + "</title><body><h1>" + esc(label) + "</h1><p>" + esc(a.Name) + "</p><p>交付等级：" + esc(level) + "</p><p>日期范围：" + esc(a.DateFrom) + " 至 " + esc(a.DateTo) + "</p><p>可用材料：" + fmt.Sprint(len(docs)) + "；已分类材料：" + fmt.Sprint(len(ins.Sentiments)) + "</p><ul>")
	for _, r := range reasons {
		b.WriteString("<li>" + esc(r) + "</li>")
	}
	b.WriteString("</ul>")
	if level == "full" || level == "limited" {
		b.WriteString("<h2>已验证分析分项</h2><p>" + esc(ins.Summary) + "</p>")
		for _, d := range ins.Dimensions {
			b.WriteString("<section><h3>" + esc(d.Name) + "</h3><p>" + esc(d.Findings) + "</p><p>" + esc(d.DeepRead) + "</p>")
			for _, q := range d.Quotes {
				b.WriteString("<blockquote>" + esc(q.Text) + " — " + esc(q.Source) + "</blockquote>")
			}
			for _, r := range d.Recommendations {
				b.WriteString("<p>建议：" + esc(fmt.Sprint(r["stage"])) + " — " + esc(fmt.Sprint(r["action"])) + "；依据：" + esc(fmt.Sprint(r["rationale"])) + "</p>")
			}
			b.WriteString("</section>")
		}
	}
	if level == "empty" {
		b.WriteString("<p>本次未取得符合范围的可用材料。此回执不包含舆情结论。</p>")
	}
	b.WriteString("<h2>来源材料</h2>")
	for _, d := range docs {
		b.WriteString("<article><h3>" + esc(d.Title) + "</h3><p>" + esc(d.SourceType) + " " + esc(d.PublishedAt) + "</p><p>" + esc(d.URL) + "</p><p>材料类型：" + esc(d.ContentKind) + "；语义分类：" + esc(d.SemanticClassification) + "</p><pre>" + esc(d.Content) + "</pre></article>")
	}
	if a.RetrievalCoverage != nil {
		b.WriteString("<h2>采集覆盖记录</h2><pre>" + esc(string(marshalJSON(a.RetrievalCoverage))) + "</pre>")
	}
	b.WriteString("</body></html>")
	return b.String()
}

func (s *Service) saveInsightSnapshot(ctx context.Context, msg TaskMessage, insight InsightResult) error {
	pg, ok := s.store.(*pgStore)
	if !ok {
		return s.SetInsight(ctx, msg.TenantID, msg.AnalysisID, insight)
	}
	tag, err := pg.pool.Exec(ctx, `UPDATE analysis_runs r SET insight_snapshot=$4 FROM analyses a WHERE r.id=$1 AND r.tenant_id=$2 AND r.analysis_id=$3 AND a.id=r.analysis_id AND a.tenant_id=r.tenant_id AND a.current_run_id=r.id AND a.state NOT IN ('completed','failed','canceled')`, msg.RunID, msg.TenantID, msg.AnalysisID, marshalJSON(insight))
	if err == nil && tag.RowsAffected() != 1 {
		return pkgerrors.ErrConflict
	}
	return err
}
