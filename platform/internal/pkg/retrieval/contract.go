// Package retrieval holds the evidence boundary shared by query transports and
// the analysis pipeline. Semantic relevance never overrides these checks.
package retrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const SemanticVersion = "semantic-v2"

func SourceLabel(source string) string {
	return map[string]string{"news": "新闻", "weibo": "微博", "wechat": "公众号", "douyin": "抖音", "toutiao": "今日头条", "xigua": "西瓜视频", "xiaohongshu": "小红书", "bilibili": "B站"}[source]
}
func NormalizeQuote(value string) string {
	text := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
	pairs := map[rune]rune{'"': '"', '\'': '\'', '“': '”', '‘': '’', '「': '」', '『': '』', '«': '»'}
	characters := []rune(text)
	for len(characters) >= 2 && pairs[characters[0]] == characters[len(characters)-1] {
		characters = characters[1 : len(characters)-1]
	}
	return string(characters)
}

type Coverage struct {
	AdmissionVersion        string                     `json:"admission_version"`
	ProviderCandidates      int                        `json:"provider_candidates"`
	UnusableCount           int                        `json:"unusable_count"`
	IrrelevantCount         int                        `json:"irrelevant_count"`
	SourceMismatchCount     int                        `json:"source_mismatch_count"`
	AcceptedCount           int                        `json:"accepted_count"`
	CandidateTruncated      bool                       `json:"candidate_truncated"`
	DirectCount             int                        `json:"direct_count"`
	ContextCount            int                        `json:"context_count"`
	PerKeyword              []map[string]any           `json:"per_keyword"`
	PerPlatform             []map[string]any           `json:"per_platform"`
	CandidateAudit          []map[string]any           `json:"candidate_audit"`
	ResearchIntent          map[string]any             `json:"research_intent,omitempty"`
	Warning                 string                     `json:"warning,omitempty"`
	FilterLimitations       string                     `json:"filter_limitations,omitempty"`
	UnverifiableDateCount   int                        `json:"unverifiable_date_count"`
	InsightMaterialCoverage map[string]any             `json:"insight_material_coverage,omitempty"`
	ReportMaterialCoverage  map[string]any             `json:"report_material_coverage,omitempty"`
	Extra                   map[string]json.RawMessage `json:"-"`
}

// Preserve additional provider diagnostics rather than dropping their meaning
// while converting between engine, business, and durable audit contracts.
func (c *Coverage) UnmarshalJSON(raw []byte) error {
	type plain Coverage
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return err
	}
	known, _ := json.Marshal(decoded)
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(known, &keys)
	for key := range keys {
		delete(all, key)
	}
	// An object with no extension fields keeps the zero value. This preserves
	// historical lexical records across PostgreSQL JSONB reconstruction.
	if len(all) > 0 {
		decoded.Extra = all
	}
	*c = Coverage(decoded)
	return nil
}
func (c Coverage) MarshalJSON() ([]byte, error) {
	type plain Coverage
	b, err := json.Marshal(plain(c))
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	_ = json.Unmarshal(b, &all)
	for key, value := range c.Extra {
		if _, exists := all[key]; !exists {
			all[key] = value
		}
	}
	return json.Marshal(all)
}

type Material struct {
	ID, Title, Content, URL, FinalURL, SourceType, PublishedAt, Classification, Reason string
	EvidenceSpans                                                                      []string
	ObservedSpans                                                                      []map[string]int
	DateSources                                                                        map[string]string
}
type Bounds struct {
	Sources, ExcludeWords []string
	DateFrom, DateTo      string
}

var platformDomains = []struct {
	source  string
	domains []string
}{
	{"douyin", []string{"douyin.com", "iesdouyin.com"}},
	{"toutiao", []string{"toutiao.com"}}, {"xigua", []string{"ixigua.com"}},
	{"weibo", []string{"weibo.com", "weibo.cn"}}, {"wechat", []string{"mp.weixin.qq.com"}},
	{"xiaohongshu", []string{"xiaohongshu.com", "xhslink.com"}}, {"bilibili", []string{"bilibili.com", "b23.tv"}},
	{"kuaishou", []string{"kuaishou.com", "ksurl.cn"}}, {"zhihu", []string{"zhihu.com"}},
	{"news", []string{"36kr.com", "sohu.com", "ifeng.com", "sina.com.cn", "news.qq.com", "thepaper.cn", "xinhuanet.com", "people.com.cn", "cnr.cn", "cctv.com", "chinanews.com.cn", "yicai.com", "jiemian.com", "stcn.com", "cnstock.com", "nbd.com.cn", "cs.com.cn"}},
}

func URLSource(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("invalid public material URL")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || !strings.Contains(host, ".") {
		return "", fmt.Errorf("nonpublic material URL")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return "", fmt.Errorf("nonpublic material URL")
	}
	for _, platform := range platformDomains {
		for _, domain := range platform.domains {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return platform.source, nil
			}
		}
	}
	return "custom_web", nil
}

func HardBoundary(m Material, b Bounds) error {
	actual, err := URLSource(m.URL)
	if err != nil {
		return err
	}
	if actual != m.SourceType {
		return fmt.Errorf("source does not match original URL")
	}
	if m.FinalURL != "" {
		actual, err = URLSource(m.FinalURL)
		if err != nil {
			return err
		}
		if actual != m.SourceType {
			return fmt.Errorf("source does not match final URL")
		}
	}
	if len(b.Sources) > 0 {
		found := false
		for _, source := range b.Sources {
			if source == m.SourceType {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("source was not selected")
		}
	}
	content := strings.ToLower(m.Title + " " + m.Content)
	for _, word := range b.ExcludeWords {
		word = strings.ToLower(strings.TrimSpace(word))
		if word != "" && strings.Contains(content, word) {
			return fmt.Errorf("explicit exclusion word")
		}
	}
	if b.DateFrom != "" || b.DateTo != "" {
		day, err := publicationDay(m.PublishedAt)
		if err != nil {
			return fmt.Errorf("publication date unavailable")
		}
		if (b.DateFrom != "" && day < b.DateFrom) || (b.DateTo != "" && day > b.DateTo) {
			return fmt.Errorf("publication date outside requested range")
		}
		var observed string
		for _, source := range []string{"provider", "body"} {
			if raw := m.DateSources[source]; raw != "" {
				other, parseErr := publicationDay(raw)
				if parseErr != nil {
					return fmt.Errorf("publication date source unverifiable")
				}
				if other != day || (observed != "" && other != observed) {
					return fmt.Errorf("publication date conflict")
				}
				observed = other
			}
		}
	}
	return nil
}
func publicationDay(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format(time.DateOnly), nil
	}
	for _, layout := range []string{time.DateOnly, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format(time.DateOnly), nil
		}
	}
	return "", fmt.Errorf("unverifiable publication date")
}

func ValidateMaterial(m Material) error {
	if strings.TrimSpace(m.ID) == "" || (m.Classification != "direct" && m.Classification != "context") || strings.TrimSpace(m.Reason) == "" || len(m.EvidenceSpans) == 0 || strings.TrimSpace(m.Content) == "" {
		return fmt.Errorf("missing or invalid semantic decision for %q", m.ID)
	}
	for _, span := range m.EvidenceSpans {
		if strings.TrimSpace(span) == "" || !strings.Contains(m.Content, span) {
			return fmt.Errorf("evidence not bound to document %q", m.ID)
		}
	}
	length := utf8.RuneCountInString(m.Content)
	for _, span := range m.ObservedSpans {
		start, sok := span["start"]
		end, eok := span["end"]
		if !sok || !eok || start < 0 || end <= start || end > length {
			return fmt.Errorf("invalid observed span for %q", m.ID)
		}
	}
	return nil
}

func ValidateResult(c *Coverage, docs []Material, b Bounds, strictAudit bool) error {
	if c == nil || c.AdmissionVersion != SemanticVersion {
		return fmt.Errorf("semantic-v2 admission required")
	}
	for _, n := range []int{c.ProviderCandidates, c.UnusableCount, c.IrrelevantCount, c.SourceMismatchCount, c.AcceptedCount, c.DirectCount, c.ContextCount, c.UnverifiableDateCount} {
		if n < 0 {
			return fmt.Errorf("negative admission count")
		}
	}
	if c.AcceptedCount != len(docs) || c.DirectCount+c.ContextCount != len(docs) || c.ProviderCandidates < len(docs) {
		return fmt.Errorf("inconsistent admitted material counts")
	}
	if strictAudit && (c.PerKeyword == nil || c.PerPlatform == nil || c.CandidateAudit == nil) {
		return fmt.Errorf("admission audit arrays must be non-null")
	}
	audit := map[string]map[string]any{}
	for _, candidate := range c.CandidateAudit {
		id, _ := candidate["id"].(string)
		if id != "" {
			if _, exists := audit[id]; exists {
				return fmt.Errorf("duplicate candidate audit ID")
			}
			audit[id] = candidate
		}
	}
	seen := map[string]bool{}
	direct, background := 0, 0
	for _, doc := range docs {
		if seen[doc.ID] {
			return fmt.Errorf("duplicate admitted document ID")
		}
		seen[doc.ID] = true
		if err := ValidateMaterial(doc); err != nil {
			return err
		}
		if err := HardBoundary(doc, b); err != nil {
			return fmt.Errorf("document %s: %w", doc.ID, err)
		}
		if doc.Classification == "direct" {
			direct++
		} else {
			background++
		}
		if strictAudit || len(audit) > 0 {
			candidate, exists := audit[doc.ID]
			if !exists {
				return fmt.Errorf("admitted ID missing from candidate audit")
			}
			classification, _ := candidate["semantic_classification"].(string)
			if classification != doc.Classification {
				return fmt.Errorf("audit classification mismatch")
			}
			// The ID must identify the exact observed body. Verbatim quotes in a
			// substituted response body do not prove that body was ever fetched.
			if original, ok := candidate["content"].(string); ok && original != "" {
				if original != doc.Content {
					return fmt.Errorf("admitted body differs from observed candidate")
				}
			} else if expected, ok := candidate["content_sha256"].(string); ok && expected != "" {
				sum := sha256.Sum256([]byte(doc.Content))
				if expected != hex.EncodeToString(sum[:]) {
					return fmt.Errorf("admitted body does not match observed content hash")
				}
			}
		}
	}
	if direct != c.DirectCount || background != c.ContextCount {
		return fmt.Errorf("semantic class counts do not match actual documents")
	}
	return nil
}

func URLKey(m Material) string {
	raw := m.FinalURL
	if raw == "" {
		raw = m.URL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	return u.String()
}
func BodyKey(content string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(content), " ")))
	return hex.EncodeToString(sum[:])
}

// ValidateConsumption checks codepoint offsets against the actual persisted
// materials, and requires every consumed document to be covered without gaps.
func ValidateConsumption(coverage map[string]any, docs []Material) error {
	if coverage["version"] != "complete-chunks-v1" {
		return fmt.Errorf("missing material consumption protocol")
	}
	phases, ok := coverage["phases"].([]any)
	if !ok || len(phases) == 0 {
		return fmt.Errorf("missing material consumption phases")
	}
	byID := map[string]Material{}
	ranges := map[string][][2]int{}
	for _, doc := range docs {
		byID[doc.ID] = doc
	}
	for _, rawPhase := range phases {
		phase, ok := rawPhase.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid consumption phase")
		}
		if pending, ok := phase["unprocessed"].([]any); !ok || len(pending) > 0 {
			return fmt.Errorf("unprocessed material remains")
		}
		spans, ok := phase["read_spans"].([]any)
		if !ok {
			return fmt.Errorf("missing consumed spans")
		}
		for _, rawSpan := range spans {
			span, ok := rawSpan.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid consumed span")
			}
			id, _ := span["document_id"].(string)
			doc, exists := byID[id]
			if !exists {
				return fmt.Errorf("unknown consumed document")
			}
			start, sok := jsonInteger(span["start"])
			end, eok := jsonInteger(span["end"])
			if !sok || !eok || start < 0 || end <= start || end > utf8.RuneCountInString(doc.Content) || span["material_class"] != doc.Classification {
				return fmt.Errorf("invalid consumed material bounds")
			}
			ranges[id] = append(ranges[id], [2]int{start, end})
		}
	}
	for _, doc := range docs {
		segments := ranges[doc.ID]
		sort.Slice(segments, func(i, j int) bool { return segments[i][0] < segments[j][0] })
		position := 0
		for _, segment := range segments {
			if segment[0] > position {
				return fmt.Errorf("unread gap in material %s", doc.ID)
			}
			if segment[1] > position {
				position = segment[1]
			}
		}
		if position != utf8.RuneCountInString(doc.Content) {
			return fmt.Errorf("material %s not fully consumed", doc.ID)
		}
	}
	return nil
}
func jsonInteger(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), n == float64(int(n))
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}
