package monitorplan

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuqing/platform/internal/business/analysis"
)

// RiskTagRule is supplied and versioned by the caller; no default risk
// classification or alert configuration is inferred from a template.
type RiskTagRule struct {
	ID           string   `json:"id"`
	Version      int      `json:"version"`
	Terms        []string `json:"terms"`
	ContextTerms []string `json:"context_terms"`
}

// RiskTagResult contains only traceable document references, never inferred
// quotations, alert deliveries, or propagation relationships.
type RiskTagResult struct {
	TenantID             string   `json:"tenant_id"`
	AnalysisID           string   `json:"analysis_id"`
	RuleID               string   `json:"rule_id"`
	RuleVersion          int      `json:"rule_version"`
	Status               string   `json:"status"` // matched | no_match | insufficient_evidence
	EvidenceDocumentIDs  []string `json:"evidence_document_ids"`
	EvidenceURLs         []string `json:"evidence_urls"`
	InsufficientEvidence bool     `json:"insufficient_evidence"`
	Reason               string   `json:"reason,omitempty"`
}

// EvaluateRiskTag matches a caller-approved rule against documents that the
// caller has already fetched by tenantID and analysisID. Document has no
// tenant/analysis fields, so this pure function cannot authenticate provenance;
// callers MUST enforce both scopes before passing documents or using results.
// A result never sends an alert or enables a plan setting.
func EvaluateRiskTag(tenantID, analysisID string, docs []analysis.Document, ruleID string, version int, rules []RiskTagRule) (RiskTagResult, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(analysisID) == "" || strings.TrimSpace(ruleID) == "" || version < 1 {
		return RiskTagResult{}, fmt.Errorf("monitorplan: tenant, analysis and versioned rule are required")
	}
	var selected *RiskTagRule
	for index := range rules {
		if rules[index].ID != ruleID || rules[index].Version != version {
			continue
		}
		if selected != nil {
			return RiskTagResult{}, fmt.Errorf("monitorplan: duplicate risk rule %q v%d", ruleID, version)
		}
		selected = &rules[index]
	}
	if selected == nil || !validRiskTerms(selected.Terms) || !validRiskTerms(selected.ContextTerms) {
		return RiskTagResult{}, fmt.Errorf("monitorplan: unknown or incomplete risk rule %q v%d", ruleID, version)
	}
	result := RiskTagResult{TenantID: tenantID, AnalysisID: analysisID, RuleID: ruleID, RuleVersion: version,
		EvidenceDocumentIDs: []string{}, EvidenceURLs: []string{}}
	if len(docs) == 0 {
		result.Status, result.InsufficientEvidence, result.Reason = "insufficient_evidence", true, "no_documents"
		return result, nil
	}
	byID := make(map[string]analysis.Document, len(docs))
	byURL := make(map[string]analysis.Document, len(docs))
	conflictingIDs := make(map[string]bool)
	conflictingURLs := make(map[string]bool)
	for _, doc := range docs {
		if doc.ID != "" {
			if previous, exists := byID[doc.ID]; exists && !sameRiskDocument(previous, doc) {
				conflictingIDs[doc.ID] = true
			}
			byID[doc.ID] = doc
		}
		if validRiskURL(doc.URL) {
			if previous, exists := byURL[doc.URL]; exists && (previous.ID == "" || doc.ID == "" || !sameRiskDocument(previous, doc)) {
				conflictingURLs[doc.URL] = true
			}
			byURL[doc.URL] = doc
		}
	}
	ordered := append([]analysis.Document(nil), docs...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].ID != ordered[j].ID {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].URL < ordered[j].URL
	})
	seenIDs, seenURLs := make(map[string]bool), make(map[string]bool)
	uncertain := false
	for _, doc := range ordered {
		paired, unpaired := riskTermsInDocument(doc, selected.Terms, selected.ContextTerms)
		if unpaired {
			uncertain = true
		}
		if !paired {
			continue
		}
		if strings.TrimSpace(doc.ID) == "" || !validRiskURL(doc.URL) || conflictingIDs[doc.ID] || conflictingURLs[doc.URL] {
			uncertain = true
			continue
		}
		if seenIDs[doc.ID] || seenURLs[doc.URL] {
			continue
		}
		seenIDs[doc.ID], seenURLs[doc.URL] = true, true
		result.EvidenceDocumentIDs = append(result.EvidenceDocumentIDs, doc.ID)
		result.EvidenceURLs = append(result.EvidenceURLs, doc.URL)
	}
	result.InsufficientEvidence = uncertain
	switch {
	case len(result.EvidenceDocumentIDs) > 0:
		result.Status = "matched"
	case uncertain:
		result.Status = "insufficient_evidence"
	default:
		result.Status = "no_match"
	}
	if uncertain {
		result.Reason = "ambiguous_or_unverifiable_documents"
	}
	return result, nil
}

func validRiskTerms(terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if strings.TrimSpace(term) == "" || term != strings.TrimSpace(term) {
			return false
		}
	}
	return true
}

func validRiskURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}

func sameRiskDocument(left, right analysis.Document) bool {
	return left.URL == right.URL && left.Title == right.Title && left.Content == right.Content && left.ContentHash == right.ContentHash && left.SourceType == right.SourceType && left.SourceName == right.SourceName && left.Author == right.Author && left.PublishedAt == right.PublishedAt
}

func riskTermsInDocument(doc analysis.Document, terms, contexts []string) (paired, unpaired bool) {
	for _, text := range []string{doc.Title, doc.Content} {
		for _, clause := range strings.FieldsFunc(strings.ToLower(text), func(char rune) bool {
			return strings.ContainsRune("。！？!?.；;，,：:\n\r", char)
		}) {
			termFound := anyRiskTerm(clause, terms)
			if !termFound {
				continue
			}
			if anyRiskTerm(clause, contexts) {
				paired = true
			} else {
				unpaired = true
			}
		}
	}
	return paired, unpaired && !paired
}

func anyRiskTerm(text string, terms []string) bool {
	for _, term := range terms {
		if containsRiskTerm(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func containsRiskTerm(text, term string) bool {
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], term)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(term)
		beforeOK := true
		if start > 0 && asciiRiskWord(term[0]) {
			previous, _ := utf8.DecodeLastRuneInString(text[:start])
			beforeOK = !unicode.IsLetter(previous) && !unicode.IsDigit(previous)
		}
		afterOK := true
		if end < len(text) && asciiRiskWord(term[len(term)-1]) {
			next, _ := utf8.DecodeRuneInString(text[end:])
			afterOK = !unicode.IsLetter(next) && !unicode.IsDigit(next)
		}
		if beforeOK && afterOK {
			return true
		}
		offset = start + 1
	}
	return false
}

func asciiRiskWord(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
}
