package analysis

import (
	"context"
	"testing"
)

func TestPipelineForwardsSceneToInsightAndReport(t *testing.T) {
	pipeline, svc := newTestPipeline(t, &fakeFetcher{docs: sampleDocs(2)}, 5e9)
	analyzer := &fakeAnalyzer{res: InsightResult{Summary: "summary", Sentiments: []Sentiment{{DocumentID: "d1", Sentiment: "neutral"}}}}
	generator := &fakeGenerator{res: ReportResult{ReportID: "report-1", Content: "report"}}
	pipeline = pipeline.WithAnalyzer(analyzer).WithGenerator(generator)
	created, err := svc.Create(context.Background(), CreateAnalysisRequest{
		TenantID: "t1", Name: "投诉", AnalysisType: "event", ScenarioTemplateID: "quality_complaint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pipeline.Handle(context.Background(), TaskMessage{TenantID: "t1", AnalysisID: created.ID}); err != nil {
		t.Fatal(err)
	}
	if analyzer.gotReq.ScenarioTemplateID != "quality_complaint" || generator.gotReq.ScenarioTemplateID != "quality_complaint" || generator.gotReq.AnalysisType != "event" {
		t.Fatalf("scene lost in pipeline: %+v / %+v", analyzer.gotReq, generator.gotReq)
	}
}
