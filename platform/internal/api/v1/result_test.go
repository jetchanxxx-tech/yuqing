package v1_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/yuging/platform/internal/business/analysis"
)

// TestContract_analyses_resultReturnsCollectedDocuments 覆盖 /result 端点。
//
// 回归背景：该端点曾是硬编码空数组的桩 —— 即使管线已采集到文档，
// 前端拿到的 documents 始终为 []，任务显示"已完成"但看不到任何内容。
func TestContract_analyses_resultReturnsCollectedDocuments(t *testing.T) {
	r, deps := newContractEnv(t)
	access, _, user := mustRegister(t, r, "docs-result@example.com", "Docs")
	tenantID, _ := user["tenant_id"].(string)
	if tenantID == "" {
		t.Fatal("register response missing tenant_id")
	}

	w := doReq(t, r, "POST", "/api/v1/analyses", access, map[string]any{
		"name":          "雅阁后排监测",
		"analysis_type": "brand",
		"keywords":      []string{"雅阁后排"},
		"sources":       []string{"news"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	id, _ := decodeBody(t, w)["id"].(string)
	if id == "" {
		t.Fatal("create response missing id")
	}

	// 测试环境不启动管线（engines.query.url 为空），直接注入采集结果
	deps.Analysis.AddDocuments(context.Background(), tenantID, id, []analysis.Document{
		{
			ID: "doc-1", Title: "雅阁内饰:后排空间是第一生产力",
			URL:     "https://www.pcauto.com.cn/cxxj/1566/15662859.html",
			Content: "留适当的包裹性和支撑度。", SourceType: "news", SourceName: "新闻",
			ContentHash: "9ff870bc6037291a",
		},
	})

	w2 := doReq(t, r, "GET", "/api/v1/analyses/"+id+"/result", access, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("result status = %d, want 200", w2.Code)
	}
	body := decodeBody(t, w2)

	docs, ok := body["documents"].([]any)
	if !ok {
		t.Fatalf("documents field is %T, want array (body=%s)", body["documents"], w2.Body.String())
	}
	if len(docs) != 1 {
		t.Fatalf("documents length = %d, want 1", len(docs))
	}
	first, _ := docs[0].(map[string]any)
	if first["title"] != "雅阁内饰:后排空间是第一生产力" {
		t.Errorf("doc title = %v", first["title"])
	}
	if first["url"] == nil || first["url"] == "" {
		t.Error("doc url should be present")
	}
	if got := body["doc_count"]; got != float64(1) {
		t.Errorf("doc_count = %v, want 1", got)
	}
}

// 空结果必须序列化为 [] 而非 null —— 前端 .map() 遇到 null 会崩溃
func TestContract_analyses_resultEmptyIsArrayNotNull(t *testing.T) {
	r, _ := newContractEnv(t)
	access, _, _ := mustRegister(t, r, "empty-result@example.com", "Empty")

	w := doReq(t, r, "POST", "/api/v1/analyses", access, map[string]any{"name": "空结果"})
	id, _ := decodeBody(t, w)["id"].(string)

	w2 := doReq(t, r, "GET", "/api/v1/analyses/"+id+"/result", access, nil)
	if !strings.Contains(w2.Body.String(), `"documents":[]`) {
		t.Errorf("empty documents should serialize as [], got: %s", w2.Body.String())
	}
}

// 洞察结果（情感/话题/摘要/警告）必须真实返回，而非硬编码零值。
func TestContract_analyses_resultReturnsInsightData(t *testing.T) {
	r, deps := newContractEnv(t)
	access, _, user := mustRegister(t, r, "insight-result@example.com", "Insight")
	tenantID, _ := user["tenant_id"].(string)

	w := doReq(t, r, "POST", "/api/v1/analyses", access, map[string]any{
		"name":          "洞察测试",
		"analysis_type": "brand",
		"keywords":      []string{"雅阁"},
		"sources":       []string{"news"},
	})
	id, _ := decodeBody(t, w)["id"].(string)

	ctx := context.Background()
	if err := deps.Analysis.SetInsight(ctx, tenantID, id, analysis.InsightResult{
		Summary: "舆情总体可控",
		Sentiments: []analysis.Sentiment{
			{DocumentID: "d1", Sentiment: "negative", Score: 0.8},
			{DocumentID: "d2", Sentiment: "positive", Score: 0.9},
		},
		Topics: []analysis.Topic{
			{ID: "t1", Name: "后排空间", Keywords: []string{"后排"}, DocCount: 2, Trend: "rising"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := deps.Analysis.SetReport(ctx, tenantID, id, "rep-1", "<html>报告</html>"); err != nil {
		t.Fatal(err)
	}

	w2 := doReq(t, r, "GET", "/api/v1/analyses/"+id+"/result", access, nil)
	if w2.Code != http.StatusOK {
		t.Fatalf("result status = %d, want 200 (body=%s)", w2.Code, w2.Body.String())
	}
	body := decodeBody(t, w2)

	if body["summary"] != "舆情总体可控" {
		t.Errorf("summary = %v", body["summary"])
	}
	sents, ok := body["sentiments"].(map[string]any)
	if !ok {
		t.Fatalf("sentiments is %T, want object", body["sentiments"])
	}
	if sents["positive"] != float64(1) || sents["negative"] != float64(1) || sents["neutral"] != float64(0) {
		t.Errorf("sentiment counts = %+v, want {1,1,0}", sents)
	}
	items, ok := sents["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("sentiment items = %v, want 2", sents["items"])
	}
	topics, ok := body["topics"].([]any)
	if !ok || len(topics) != 1 {
		t.Fatalf("topics = %v, want 1", body["topics"])
	}
	if topics[0].(map[string]any)["name"] != "后排空间" {
		t.Errorf("topic name = %v", topics[0])
	}
	report, ok := body["report"].(map[string]any)
	if !ok {
		t.Fatalf("report is %T, want object", body["report"])
	}
	if report["id"] != "rep-1" || report["content"] != "<html>报告</html>" {
		t.Errorf("report = %+v", report)
	}
}

// 报告正文是 KB 级 HTML：只能经 /result 按需返回。
// 列表与详情是被高频拉取的生命周期端点（详情页轮询 state），
// 内联正文会放大每次轮询的响应体。
func TestContract_analyses_lifecycleOmitsReportBody(t *testing.T) {
	r, deps := newContractEnv(t)
	access, _, user := mustRegister(t, r, "report-body@example.com", "Body")
	tenantID, _ := user["tenant_id"].(string)

	w := doReq(t, r, "POST", "/api/v1/analyses", access, map[string]any{
		"name":          "报告体积",
		"analysis_type": "brand",
		"keywords":      []string{"雅阁"},
		"sources":       []string{"news"},
	})
	id, _ := decodeBody(t, w)["id"].(string)

	const reportBody = "<html><body>巨大报告正文</body></html>"
	if err := deps.Analysis.SetReport(context.Background(), tenantID, id, "rep-body", reportBody); err != nil {
		t.Fatal(err)
	}

	detail := doReq(t, r, "GET", "/api/v1/analyses/"+id, access, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detail.Code)
	}
	if strings.Contains(detail.Body.String(), "report_content") {
		t.Errorf("detail must not inline the report body: %s", detail.Body.String())
	}

	list := doReq(t, r, "GET", "/api/v1/analyses", access, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.Code)
	}
	if strings.Contains(list.Body.String(), "report_content") {
		t.Errorf("list must not inline the report body: %s", list.Body.String())
	}

	// 正文仍必须经 /result 提供 —— 前端报告 Tab 依赖它。
	res := decodeBody(t, doReq(t, r, "GET", "/api/v1/analyses/"+id+"/result", access, nil))
	report, ok := res["report"].(map[string]any)
	if !ok {
		t.Fatalf("report is %T, want object", res["report"])
	}
	if report["content"] != reportBody {
		t.Errorf("report.content = %v, want the stored body", report["content"])
	}
}
