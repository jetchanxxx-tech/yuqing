package monitorplan

import (
	"reflect"
	"testing"

	"github.com/yuqing/platform/internal/business/analysis"
)

func testRiskRule() []RiskTagRule {
	return []RiskTagRule{{ID: "caller_quality", Version: 2, Terms: []string{"苹果"}, ContextTerms: []string{"手机"}}}
}

func TestEvaluateRiskTagEvidenceAndVersion(t *testing.T) {
	docs := []analysis.Document{
		{ID: "doc-b", URL: "https://example.com/b", Content: "苹果手机出现发热问题。"},
		{ID: "doc-a", URL: "https://example.com/a", Title: "苹果手机的使用反馈"},
	}
	got, err := EvaluateRiskTag("tenant-a", "analysis-a", docs, "caller_quality", 2, testRiskRule())
	if err != nil {
		t.Fatal(err)
	}
	if got.TenantID != "tenant-a" || got.AnalysisID != "analysis-a" || got.RuleID != "caller_quality" || got.RuleVersion != 2 || got.Status != "matched" {
		t.Fatalf("result scope/version/status = %+v", got)
	}
	if !reflect.DeepEqual(got.EvidenceDocumentIDs, []string{"doc-a", "doc-b"}) || !reflect.DeepEqual(got.EvidenceURLs, []string{"https://example.com/a", "https://example.com/b"}) {
		t.Fatalf("evidence = %+v", got)
	}
}

func TestEvaluateRiskTagNoDocumentsAndNoMatch(t *testing.T) {
	noDocs, err := EvaluateRiskTag("tenant-a", "analysis-a", nil, "caller_quality", 2, testRiskRule())
	if err != nil || noDocs.Status != "insufficient_evidence" || noDocs.EvidenceDocumentIDs == nil || len(noDocs.EvidenceDocumentIDs) != 0 || noDocs.EvidenceURLs == nil || len(noDocs.EvidenceURLs) != 0 {
		t.Fatalf("no documents must never produce evidence: %+v, %v", noDocs, err)
	}
	noMatch, err := EvaluateRiskTag("tenant-a", "analysis-a", []analysis.Document{{ID: "one", URL: "https://example.com/one", Content: "天气晴朗。"}}, "caller_quality", 2, testRiskRule())
	if err != nil || noMatch.Status != "no_match" || len(noMatch.EvidenceDocumentIDs) != 0 {
		t.Fatalf("no match = %+v, %v", noMatch, err)
	}
}

func TestEvaluateRiskTagDoesNotBlindlyMatchAmbiguity(t *testing.T) {
	for _, doc := range []analysis.Document{
		{ID: "one", URL: "https://example.com/one", Content: "苹果的口感很好。"},
		{ID: "two", URL: "https://example.com/two", Content: "苹果的口感很好。手机只是另一个话题。"},
		{ID: "three", URL: "https://example.com/three", Title: "苹果", Content: "手机只是另一个话题。"},
		{ID: "four", URL: "https://example.com/four", Content: "苹果是好吃的水果，手机则是另一个话题。"},
	} {
		got, err := EvaluateRiskTag("t", "a", []analysis.Document{doc}, "caller_quality", 2, testRiskRule())
		if err != nil || got.Status != "insufficient_evidence" || len(got.EvidenceDocumentIDs) != 0 {
			t.Errorf("ambiguous %s = %+v, %v", doc.ID, got, err)
		}
	}
}

func TestEvaluateRiskTagConflictingDuplicatesFailClosed(t *testing.T) {
	first := analysis.Document{ID: "same", URL: "https://example.com/a", Content: "苹果手机有讨论。"}
	for _, conflicting := range []analysis.Document{
		{ID: "same", URL: "https://example.com/b", Content: "苹果手机有讨论。"},
		{ID: "other", URL: "https://example.com/a", Content: "苹果手机出现不同内容。"},
	} {
		for _, docs := range [][]analysis.Document{{first, conflicting}, {conflicting, first}} {
			got, err := EvaluateRiskTag("t", "a", docs, "caller_quality", 2, testRiskRule())
			if err != nil || got.Status != "insufficient_evidence" || len(got.EvidenceDocumentIDs) != 0 {
				t.Errorf("conflicting duplicates = %+v, %v", got, err)
			}
		}
	}
}

func TestEvaluateRiskTagRejectsDuplicateWithMissingID(t *testing.T) {
	verified := analysis.Document{ID: "one", URL: "https://example.com/post", Content: "苹果手机出现问题。"}
	unverifiable := verified
	unverifiable.ID = ""
	got, err := EvaluateRiskTag("t", "a", []analysis.Document{verified, unverifiable}, "caller_quality", 2, testRiskRule())
	if err != nil || got.Status != "insufficient_evidence" || len(got.EvidenceDocumentIDs) != 0 {
		t.Fatalf("unverifiable duplicate = %+v, %v", got, err)
	}
}

func TestEvaluateRiskTagDoesNotMatchInsideEnglishWords(t *testing.T) {
	rules := []RiskTagRule{{ID: "caller_english", Version: 1, Terms: []string{"apple"}, ContextTerms: []string{"phone"}}}
	doc := analysis.Document{ID: "id", URL: "https://example.com/post", Content: "Pineapple phone exists."}
	got, err := EvaluateRiskTag("t", "a", []analysis.Document{doc}, "caller_english", 1, rules)
	if err != nil || got.Status != "no_match" || len(got.EvidenceDocumentIDs) != 0 {
		t.Fatalf("substring falsely matched: %+v, %v", got, err)
	}
	doc.Content = "Apple phone exists."
	got, err = EvaluateRiskTag("t", "a", []analysis.Document{doc}, "caller_english", 1, rules)
	if err != nil || got.Status != "matched" || len(got.EvidenceDocumentIDs) != 1 {
		t.Fatalf("whole word not matched: %+v, %v", got, err)
	}
}

func TestEvaluateRiskTagDeduplicatesDocumentAndURL(t *testing.T) {
	doc := analysis.Document{ID: "doc-b", URL: "https://example.com/same", Content: "苹果手机有讨论。"}
	alias := doc
	alias.ID = "doc-a"
	got, err := EvaluateRiskTag("t", "a", []analysis.Document{doc, doc, alias}, "caller_quality", 2, testRiskRule())
	if err != nil || !reflect.DeepEqual(got.EvidenceDocumentIDs, []string{"doc-a"}) || len(got.EvidenceURLs) != 1 {
		t.Fatalf("duplicate = %+v, %v", got, err)
	}
}

func TestEvaluateRiskTagRejectsUnverifiableEvidenceAndUnknownRules(t *testing.T) {
	for _, doc := range []analysis.Document{
		{ID: "", URL: "https://example.com/post", Content: "苹果手机有讨论。"},
		{ID: "one", URL: "", Content: "苹果手机有讨论。"},
		{ID: "two", URL: "javascript:alert(1)", Content: "苹果手机有讨论。"},
	} {
		got, err := EvaluateRiskTag("t", "a", []analysis.Document{doc}, "caller_quality", 2, testRiskRule())
		if err != nil || got.Status != "insufficient_evidence" || len(got.EvidenceDocumentIDs) != 0 {
			t.Errorf("invalid evidence %+v = %+v, %v", doc, got, err)
		}
	}
	for _, tc := range []struct {
		tenant, analysisID, ruleID string
		version                    int
		rules                      []RiskTagRule
	}{
		{"", "a", "caller_quality", 2, testRiskRule()},
		{"t", "", "caller_quality", 2, testRiskRule()},
		{"t", "a", "unknown", 2, testRiskRule()},
		{"t", "a", "caller_quality", 1, testRiskRule()},
		{"t", "a", "caller_quality", 2, nil},
		{"t", "a", "caller_quality", 2, []RiskTagRule{{ID: "caller_quality", Version: 2, Terms: []string{"苹果"}}}},
	} {
		if result, err := EvaluateRiskTag(tc.tenant, tc.analysisID, nil, tc.ruleID, tc.version, tc.rules); err == nil || len(result.EvidenceDocumentIDs) != 0 {
			t.Errorf("unapproved rule/scope accepted: %+v, %v", result, err)
		}
	}
}
