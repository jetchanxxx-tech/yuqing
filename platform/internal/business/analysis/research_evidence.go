package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/retrieval"
	"github.com/yuqing/platform/internal/platform/billingpolicy"
)

type ResearchIntentRequest struct {
	RunID    string         `json:"run_id"`
	TenantID string         `json:"tenant_id,omitempty"`
	Intent   map[string]any `json:"intent"`
	Version  string         `json:"version"`
	Model    string         `json:"model"`
}
type ResearchIntentResult struct {
	Intent  map[string]any `json:"intent"`
	Version string         `json:"version"`
	Reused  bool           `json:"reused"`
}
type RetrievalAuditRequest struct {
	RunID    string             `json:"run_id"`
	TenantID string             `json:"tenant_id,omitempty"`
	Coverage *RetrievalCoverage `json:"coverage"`
}

func (s *Service) SaveResearchIntent(ctx context.Context, analysisID string, req ResearchIntentRequest) (ResearchIntentResult, error) {
	result := ResearchIntentResult{}
	pg, ok := s.store.(*pgStore)
	if !ok {
		return result, pkgerrors.ErrServiceUnavailable
	}
	if req.RunID == "" || req.Version != retrieval.SemanticVersion || req.Intent["version"] != req.Version || strings.TrimSpace(req.Model) == "" || strings.TrimSpace(fmt.Sprint(req.Intent["topic"])) == "" {
		return result, pkgerrors.ErrBadRequest
	}
	if err := validateIntentShape(req.Intent); err != nil {
		return result, err
	}
	tx, err := pg.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	var tenantID, actorID, keyID string
	if err = tx.QueryRow(ctx, `SELECT tenant_id,COALESCE(actor_user_id,''),COALESCE(actor_api_key_id,'') FROM analysis_runs WHERE id=$1 AND analysis_id=$2`, req.RunID, analysisID).Scan(&tenantID, &actorID, &keyID); err != nil {
		return result, notFoundOrInternal(err, "analysis run not found")
	}
	if req.TenantID != "" && req.TenantID != tenantID {
		return result, pkgerrors.ErrNotFound
	}
	if err = billingpolicy.CheckActorTx(ctx, tx, tenantID, billingpolicy.Actor{UserID: actorID, APIKeyID: keyID, Permission: "analyses:create"}); err != nil {
		return result, err
	}
	a, err := scanAnalysis(tx.QueryRow(ctx, `SELECT `+analysisColumns+` FROM analyses WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, analysisID, tenantID))
	if err != nil {
		return result, notFoundOrInternal(err, "analysis not found")
	}
	if a.CurrentRunID != req.RunID || a.State != StateFetching {
		return result, pkgerrors.ErrConflict
	}
	if words, exists := req.Intent["original_keywords"]; exists && string(marshalJSON(words)) != string(marshalJSON(nonNullStrings(a.Keywords))) {
		return result, pkgerrors.Wrap(pkgerrors.ErrBadRequest, "research intent changed original keywords")
	}
	var raw []byte
	var version, state string
	if err = tx.QueryRow(ctx, `SELECT research_intent,research_intent_version,state FROM analysis_runs WHERE id=$1 FOR UPDATE`, req.RunID).Scan(&raw, &version, &state); err != nil {
		return result, err
	}
	if state != string(StateFetching) {
		return result, pkgerrors.ErrConflict
	}
	if version != "" {
		if err = json.Unmarshal(raw, &result.Intent); err != nil {
			return result, err
		}
		result.Version = version
		result.Reused = true
		if version != req.Version || !reflect.DeepEqual(result.Intent, req.Intent) {
			return result, pkgerrors.ErrConflict
		}
		return result, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE analysis_runs SET research_intent=$2,research_intent_version=$3,research_intent_model=$4,research_intent_created_at=now() WHERE id=$1`, req.RunID, marshalJSON(req.Intent), req.Version, req.Model); err != nil {
		return result, err
	}
	if _, err = tx.Exec(ctx, `UPDATE analyses SET research_intent=$3,research_intent_version=$4 WHERE id=$1 AND tenant_id=$2`, analysisID, tenantID, marshalJSON(req.Intent), req.Version); err != nil {
		return result, err
	}
	result.Intent = req.Intent
	result.Version = req.Version
	return result, tx.Commit(ctx)
}

func validateIntentShape(intent map[string]any) error {
	allowed := map[string]bool{}
	for _, key := range []string{"version", "original_keywords", "research_question", "title", "analysis_type", "scenario_template_id", "topic", "entities", "aliases", "relations", "focus", "non_assumptions", "search_queries", "model"} {
		allowed[key] = true
	}
	for key := range intent {
		if !allowed[key] {
			return pkgerrors.Wrap(pkgerrors.ErrBadRequest, "unsupported research intent field")
		}
	}
	if value, ok := intent["topic"].(string); !ok || strings.TrimSpace(value) == "" {
		return pkgerrors.ErrBadRequest
	}
	return nil
}
func nonNullStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// SaveRetrievalAudit stores observed facts even after cancellation or a newer
// rerun. Only the matching run may update the analysis's current view.
func (s *Service) SaveRetrievalAudit(ctx context.Context, analysisID string, req RetrievalAuditRequest) error {
	if req.Coverage == nil || req.Coverage.AdmissionVersion != retrieval.SemanticVersion || req.RunID == "" {
		return pkgerrors.ErrBadRequest
	}
	pg, ok := s.store.(*pgStore)
	if !ok {
		return pkgerrors.ErrServiceUnavailable
	}
	tx, err := pg.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var tenantID string
	if err = tx.QueryRow(ctx, `SELECT tenant_id FROM analysis_runs WHERE id=$1 AND analysis_id=$2`, req.RunID, analysisID).Scan(&tenantID); err != nil {
		return notFoundOrInternal(err, "analysis run not found")
	}
	if req.TenantID != "" && req.TenantID != tenantID {
		return pkgerrors.ErrNotFound
	}
	var current string
	var purged *time.Time
	if err = tx.QueryRow(ctx, `SELECT COALESCE(current_run_id,''),closure_purged_at FROM analyses WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, analysisID, tenantID).Scan(&current, &purged); err != nil {
		return notFoundOrInternal(err, "analysis not found")
	}
	if purged != nil {
		return pkgerrors.ErrConflict
	}
	if err = storeCoverageTx(ctx, tx, tenantID, analysisID, req.RunID, req.Coverage); err != nil {
		return err
	}
	if current == req.RunID {
		if _, err = tx.Exec(ctx, `UPDATE analyses SET retrieval_coverage=$3 WHERE id=$1 AND tenant_id=$2`, analysisID, tenantID, marshalJSON(req.Coverage)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func storeCoverageTx(ctx context.Context, tx pgx.Tx, tenantID, analysisID, runID string, coverage *RetrievalCoverage) error {
	if coverage == nil {
		return nil
	}
	var previousRaw []byte
	if err := tx.QueryRow(ctx, `SELECT retrieval_coverage FROM analysis_runs WHERE id=$1 AND tenant_id=$2 AND analysis_id=$3 FOR UPDATE`, runID, tenantID, analysisID).Scan(&previousRaw); err != nil {
		return err
	}
	if len(previousRaw) > 0 {
		var previous RetrievalCoverage
		if err := json.Unmarshal(previousRaw, &previous); err != nil {
			return err
		}
		if coverage.InsightMaterialCoverage == nil {
			coverage.InsightMaterialCoverage = previous.InsightMaterialCoverage
		}
		if coverage.ReportMaterialCoverage == nil {
			coverage.ReportMaterialCoverage = previous.ReportMaterialCoverage
		}
	}
	for _, candidate := range coverage.CandidateAudit {
		content, _ := candidate["content"].(string)
		if content == "" {
			continue
		}
		documentID, _ := candidate["id"].(string)
		if documentID == "" || utf8.RuneCountInString(content) > 200000 {
			return pkgerrors.ErrBadRequest
		}
		sum := sha256.Sum256([]byte(content))
		hash := hex.EncodeToString(sum[:])
		delete(candidate, "content")
		candidate["content_sha256"] = hash
		candidate["content_char_count"] = utf8.RuneCountInString(content)
		if _, err := tx.Exec(ctx, `INSERT INTO analysis_candidate_materials(run_id,tenant_id,analysis_id,document_id,content,content_sha256,metadata) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(run_id,document_id,content_sha256) DO UPDATE SET metadata=EXCLUDED.metadata`, runID, tenantID, analysisID, documentID, content, hash, marshalJSON(candidate)); err != nil {
			return err
		}
	}
	raw := marshalJSON(coverage)
	sum := sha256.Sum256(raw)
	if _, err := tx.Exec(ctx, `INSERT INTO analysis_retrieval_audits(id,run_id,tenant_id,analysis_id,coverage,coverage_hash) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(run_id,coverage_hash) DO NOTHING`, id.New(), runID, tenantID, analysisID, raw, hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE analysis_runs SET retrieval_coverage=$2,insight_material_coverage=$3,report_material_coverage=$4 WHERE id=$1`, runID, raw, marshalJSON(coverage.InsightMaterialCoverage), marshalJSON(coverage.ReportMaterialCoverage))
	return err
}

func (s *Service) saveCoverage(ctx context.Context, msg TaskMessage, coverage *RetrievalCoverage) error {
	if coverage == nil {
		return nil
	}
	if _, ok := s.store.(*pgStore); ok && msg.RunID != "" && coverage.AdmissionVersion == retrieval.SemanticVersion {
		return s.SaveRetrievalAudit(ctx, msg.AnalysisID, RetrievalAuditRequest{RunID: msg.RunID, TenantID: msg.TenantID, Coverage: coverage})
	}
	return s.store.mutate(ctx, msg.TenantID, msg.AnalysisID, func(a *AnalysisResult) error { a.RetrievalCoverage = coverage; return nil })
}

func materialForBoundary(d Document) retrieval.Material {
	return retrieval.Material{ID: d.ID, Title: d.Title, Content: d.Content, URL: d.URL, FinalURL: d.FinalURL, SourceType: d.SourceType, PublishedAt: d.PublishedAt, Classification: d.SemanticClassification, Reason: d.SemanticReason, EvidenceSpans: d.EvidenceSpans, ObservedSpans: d.ObservedSpans, DateSources: d.DateSources}
}
func boundaryMaterials(docs []Document) []retrieval.Material {
	out := make([]retrieval.Material, 0, len(docs))
	for _, doc := range docs {
		out = append(out, materialForBoundary(doc))
	}
	return out
}

func (s *Service) decorateResult(a *AnalysisResult) {
	if a.RetrievalCoverage != nil {
		a.InsightMaterialCoverage = a.RetrievalCoverage.InsightMaterialCoverage
		a.ReportMaterialCoverage = a.RetrievalCoverage.ReportMaterialCoverage
	}
	if !a.StartedAt.IsZero() {
		start := a.StartedAt
		a.RunStartedAt = &start
		timeout := s.taskTimeout
		if timeout <= 0 {
			timeout = 2760 * time.Second
		}
		deadline := start.Add(timeout)
		a.TaskDeadlineAt = &deadline
	}
}

func (s *Service) setMaterialCoverage(ctx context.Context, msg TaskMessage, stage string, consumption map[string]any) error {
	a, err := s.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil {
		return err
	}
	if a.RetrievalCoverage == nil {
		return pkgerrors.ErrConflict
	}
	coverage := a.RetrievalCoverage
	if stage == "insight" {
		coverage.InsightMaterialCoverage = consumption
	} else {
		coverage.ReportMaterialCoverage = consumption
	}
	return s.saveCoverage(ctx, msg, coverage)
}
func (p *Pipeline) researchIntent(ctx context.Context, msg TaskMessage) map[string]any {
	a, err := p.svc.Get(ctx, msg.TenantID, msg.AnalysisID)
	if err != nil || a.ResearchIntentVersion != retrieval.SemanticVersion {
		return nil
	}
	return a.ResearchIntent
}
func partitionSemanticDocuments(docs []Document) ([]Document, []Document) {
	direct, background := []Document{}, []Document{}
	for _, doc := range docs {
		if doc.SemanticClassification == "direct" {
			direct = append(direct, doc)
		} else if doc.SemanticClassification == "context" {
			background = append(background, doc)
		}
	}
	return direct, background
}

func validateSemanticInsight(insight InsightResult, direct, background []Document) error {
	allowed := map[string]Document{}
	directIDs := map[string]bool{}
	for _, doc := range direct {
		allowed[doc.ID] = doc
		directIDs[doc.ID] = true
	}
	for _, doc := range background {
		allowed[doc.ID] = doc
	}
	seen := map[string]bool{}
	for _, sentiment := range insight.Sentiments {
		if !directIDs[sentiment.DocumentID] || seen[sentiment.DocumentID] || (sentiment.Sentiment != "positive" && sentiment.Sentiment != "negative" && sentiment.Sentiment != "neutral") {
			return fmt.Errorf("sentiment is not bound to one direct document")
		}
		seen[sentiment.DocumentID] = true
	}
	if len(seen) != len(directIDs) {
		return fmt.Errorf("direct sentiment classification is incomplete")
	}
	normalize := retrieval.NormalizeQuote
	for _, dimension := range insight.Dimensions {
		for _, quote := range dimension.Quotes {
			doc, exists := allowed[quote.DocumentID]
			text := normalize(quote.Text)
			if !exists || text == "" || strings.TrimSpace(quote.Source) == "" || (!strings.Contains(normalize(doc.Content), text) && !strings.Contains(normalize(doc.Title), text)) || (quote.Source != doc.SourceType && quote.Source != doc.SourceName && quote.Source != retrieval.SourceLabel(doc.SourceType)) {
				return fmt.Errorf("insight quote is not bound to its source document")
			}
		}
	}
	return nil
}
func filterSemanticDocuments(docs []Document, a *AnalysisResult, coverage *RetrievalCoverage) []Document {
	ordered := append([]Document{}, docs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].SemanticClassification == "direct" && ordered[j].SemanticClassification != "direct"
	})
	filtered := make([]Document, 0, len(ordered))
	urls, bodies := map[string]bool{}, map[string]bool{}
	for _, doc := range ordered {
		m := materialForBoundary(doc)
		reason := ""
		if err := retrieval.HardBoundary(m, retrieval.Bounds{Sources: a.Sources, ExcludeWords: a.ExcludeWords, DateFrom: a.DateFrom, DateTo: a.DateTo}); err != nil {
			reason = err.Error()
		}
		url, body := retrieval.URLKey(m), retrieval.BodyKey(doc.Content)
		if reason == "" && (urls[url] || bodies[body]) {
			reason = "duplicate URL or cleaned content"
		}
		if reason != "" {
			for _, candidate := range coverage.CandidateAudit {
				if candidate["id"] == doc.ID {
					candidate["platform_rejection"] = reason
					candidate["status"] = "platform_rejected"
				}
			}
			continue
		}
		urls[url] = true
		bodies[body] = true
		filtered = append(filtered, doc)
	}
	return filtered
}
