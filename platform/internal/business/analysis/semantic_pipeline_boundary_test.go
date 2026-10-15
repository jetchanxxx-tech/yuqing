package analysis

import (
	"context"
	"testing"
	"time"
	"unicode/utf8"
)

func semanticBoundaryDocs() []Document {
	return []Document{
		{ID: "direct-1", Title: "iPhone18 Pro", Content: "iPhone18 Pro battery review", URL: "https://www.toutiao.com/article/1", FinalURL: "https://www.toutiao.com/article/1", SourceType: "toutiao", SemanticClassification: "direct", SemanticReason: "related model", EvidenceSpans: []string{"iPhone18 Pro"}, PublishedAt: "2026-10-01"},
		{ID: "direct-2", Title: "iPhone18 Max", Content: "iPhone18 Max display review", URL: "https://www.toutiao.com/article/2", FinalURL: "https://www.toutiao.com/article/2", SourceType: "toutiao", SemanticClassification: "direct", SemanticReason: "related model", EvidenceSpans: []string{"iPhone18 Max"}, PublishedAt: "2026-10-02"},
		{ID: "background", Title: "Phone market", Content: "Phone market background", URL: "https://www.toutiao.com/article/3", FinalURL: "https://www.toutiao.com/article/3", SourceType: "toutiao", SemanticClassification: "context", SemanticReason: "background only", EvidenceSpans: []string{"market background"}, PublishedAt: "2026-10-03"},
	}
}

type semanticBoundaryAnalyzer struct {
	got   InsightRequest
	calls int
}

func (a *semanticBoundaryAnalyzer) Analyze(_ context.Context, req InsightRequest) (InsightResult, error) {
	a.got = req
	a.calls++
	spans := []any{}
	for _, doc := range append(append([]Document{}, req.Documents...), req.ContextDocuments...) {
		spans = append(spans, map[string]any{"document_id": doc.ID, "start": 0, "end": utf8.RuneCountInString(doc.Content), "material_class": doc.SemanticClassification})
	}
	return InsightResult{Summary: "reviewed", Sentiments: []Sentiment{{DocumentID: "direct-1", Sentiment: "neutral"}, {DocumentID: "direct-2", Sentiment: "neutral"}}, MaterialCoverage: map[string]any{"version": "complete-chunks-v1", "phases": []any{map[string]any{"phase": "classification", "read_spans": spans, "unprocessed": []any{}}}}}, nil
}

func TestSemanticPipelineContextExcludedFromDirectMetrics(t *testing.T) {
	docs := semanticBoundaryDocs()
	fetcher := &admissionFetcher{documents: docs, coverage: RetrievalCoverage{AdmissionVersion: "semantic-v2", ProviderCandidates: 3, AcceptedCount: 3, DirectCount: 2, ContextCount: 1, PerKeyword: []map[string]any{}}}
	p, svc := newTestPipeline(t, fetcher, 5*time.Second)
	analyzer := &semanticBoundaryAnalyzer{}
	p.WithAnalyzer(analyzer)
	a, err := svc.Create(context.Background(), CreateAnalysisRequest{TenantID: "t1", Name: "iPhone18 family", Keywords: []string{"iphone18", "iphone18pro", "iphone18max"}, Sources: []string{"toutiao"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Handle(context.Background(), TaskMessage{TenantID: "t1", AnalysisID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if len(analyzer.got.Documents) != 2 {
		t.Fatalf("context contaminated direct sentiment/topic input: %+v", analyzer.got.Documents)
	}
}

func TestSemanticPipelineRecountsDirectEvidenceAfterHardBoundsAndDedup(t *testing.T) {
	for _, name := range []string{"outside date", "duplicate URL", "duplicate body"} {
		t.Run(name, func(t *testing.T) {
			docs := semanticBoundaryDocs()[:2]
			switch name {
			case "outside date":
				docs[1].PublishedAt = "2026-09-01"
			case "duplicate URL":
				docs[1].URL = docs[0].URL
				docs[1].FinalURL = docs[0].FinalURL
			case "duplicate body":
				docs[1].Content = docs[0].Content
				docs[1].EvidenceSpans = append([]string{}, docs[0].EvidenceSpans...)
			}
			fetcher := &admissionFetcher{documents: docs, coverage: RetrievalCoverage{AdmissionVersion: "semantic-v2", ProviderCandidates: 2, AcceptedCount: 2, DirectCount: 2, PerKeyword: []map[string]any{}}}
			p, svc := newTestPipeline(t, fetcher, 5*time.Second)
			analyzer := &semanticBoundaryAnalyzer{}
			p.WithAnalyzer(analyzer)
			a, err := svc.Create(context.Background(), CreateAnalysisRequest{TenantID: "t1", Name: "family", Keywords: []string{"iphone18"}, Sources: []string{"toutiao"}, DateFrom: "2026-10-01", DateTo: "2026-10-15"})
			if err != nil {
				t.Fatal(err)
			}
			if err = p.Handle(context.Background(), TaskMessage{TenantID: "t1", AnalysisID: a.ID}); err == nil {
				t.Fatal("two-direct gate trusted the upstream prefilter count")
			}
			if analyzer.calls != 0 {
				t.Fatal("insufficient direct evidence reached an LLM")
			}
		})
	}
}

func TestSemanticInsightAcceptsCanonicalPlatformQuotesWithWrappers(t *testing.T) {
	direct := []Document{{ID: "d1", Content: "后座靠背偏直", SourceType: "wechat"}}
	insight := InsightResult{Sentiments: []Sentiment{{DocumentID: "d1", Sentiment: "negative"}}, Dimensions: []Dimension{{ID: "sentiment", Quotes: []Quote{{DocumentID: "d1", Text: "「靠背偏直」", Source: "公众号"}}}}}
	if err := validateSemanticInsight(insight, direct, nil); err != nil {
		t.Fatalf("valid Python-verified quote rejected by Go boundary: %v", err)
	}
	insight.Dimensions[0].Quotes[0].Source = "今日头条"
	if err := validateSemanticInsight(insight, direct, nil); err == nil {
		t.Fatal("quote moved to a different platform")
	}
}
