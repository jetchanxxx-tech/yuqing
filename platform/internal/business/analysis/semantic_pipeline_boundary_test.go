package analysis

import (
	"context"
	"testing"
	"time"
)

func semanticBoundaryDocs() []Document {
	return []Document{
		{ID:"direct-1", Title:"iPhone18 Pro", Content:"iPhone18 Pro battery review", URL:"https://www.toutiao.com/article/1", FinalURL:"https://www.toutiao.com/article/1", SourceType:"toutiao", SemanticClassification:"direct", SemanticReason:"related model", EvidenceSpans:[]string{"iPhone18 Pro"}, PublishedAt:"2026-10-01"},
		{ID:"direct-2", Title:"iPhone18 Max", Content:"iPhone18 Max display review", URL:"https://www.toutiao.com/article/2", FinalURL:"https://www.toutiao.com/article/2", SourceType:"toutiao", SemanticClassification:"direct", SemanticReason:"related model", EvidenceSpans:[]string{"iPhone18 Max"}, PublishedAt:"2026-10-02"},
		{ID:"background", Title:"Phone market", Content:"Phone market background", URL:"https://www.toutiao.com/article/3", FinalURL:"https://www.toutiao.com/article/3", SourceType:"toutiao", SemanticClassification:"context", SemanticReason:"background only", EvidenceSpans:[]string{"market background"}, PublishedAt:"2026-10-03"},
	}
}

type semanticBoundaryAnalyzer struct { got InsightRequest; calls int }
func (a *semanticBoundaryAnalyzer) Analyze(_ context.Context, req InsightRequest) (InsightResult,error) {
	a.got=req; a.calls++
	return InsightResult{Summary:"reviewed",Sentiments:[]Sentiment{{DocumentID:"direct-1",Sentiment:"neutral"}}},nil
}

func TestSemanticPipelineContextExcludedFromDirectMetrics(t *testing.T) {
	docs:=semanticBoundaryDocs()
	fetcher:=&admissionFetcher{documents:docs,coverage:RetrievalCoverage{AdmissionVersion:"semantic-v2",ProviderCandidates:3,AcceptedCount:3,DirectCount:2,ContextCount:1,PerKeyword:[]map[string]any{}}}
	p,svc:=newTestPipeline(t,fetcher,5*time.Second)
	analyzer:=&semanticBoundaryAnalyzer{}; p.WithAnalyzer(analyzer)
	a,err:=svc.Create(context.Background(),CreateAnalysisRequest{TenantID:"t1",Name:"iPhone18 family",Keywords:[]string{"iphone18","iphone18pro","iphone18max"},Sources:[]string{"toutiao"}}); if err!=nil {t.Fatal(err)}
	if err=p.Handle(context.Background(),TaskMessage{TenantID:"t1",AnalysisID:a.ID});err!=nil {t.Fatal(err)}
	if len(analyzer.got.Documents)!=2 {t.Fatalf("context contaminated direct sentiment/topic input: %+v",analyzer.got.Documents)}
}

func TestSemanticPipelineRecountsDirectEvidenceAfterHardBoundsAndDedup(t *testing.T) {
	for _,name:=range []string{"outside date","duplicate URL","duplicate body"} {
		t.Run(name,func(t *testing.T){
			docs:=semanticBoundaryDocs()[:2]
			switch name {case "outside date": docs[1].PublishedAt="2026-09-01";case "duplicate URL":docs[1].URL=docs[0].URL;docs[1].FinalURL=docs[0].FinalURL;case "duplicate body":docs[1].Content=docs[0].Content}
			fetcher:=&admissionFetcher{documents:docs,coverage:RetrievalCoverage{AdmissionVersion:"semantic-v2",ProviderCandidates:2,AcceptedCount:2,DirectCount:2,PerKeyword:[]map[string]any{}}}
			p,svc:=newTestPipeline(t,fetcher,5*time.Second); analyzer:=&semanticBoundaryAnalyzer{};p.WithAnalyzer(analyzer)
			a,err:=svc.Create(context.Background(),CreateAnalysisRequest{TenantID:"t1",Name:"family",Keywords:[]string{"iphone18"},Sources:[]string{"toutiao"},DateFrom:"2026-10-01",DateTo:"2026-10-15"});if err!=nil {t.Fatal(err)}
			if err=p.Handle(context.Background(),TaskMessage{TenantID:"t1",AnalysisID:a.ID});err==nil {t.Fatal("two-direct gate trusted the upstream prefilter count")}
			if analyzer.calls!=0 {t.Fatal("insufficient direct evidence reached an LLM")}
		})
	}
}
