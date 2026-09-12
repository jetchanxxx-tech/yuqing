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
