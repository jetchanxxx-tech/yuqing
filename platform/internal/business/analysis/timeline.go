package analysis

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TimelineNode represents a source record, not an inferred event stage.
type TimelineNode struct {
	eventAt             time.Time
	DocumentID          string   `json:"document_id"`
	Kind                string   `json:"kind"`
	Basis               string   `json:"basis"`
	EventTime           string   `json:"event_time"`
	Title               string   `json:"title"`
	SourceType          string   `json:"source_type"`
	SourceName          string   `json:"source_name"`
	Author              string   `json:"author"`
	EvidenceDocumentIDs []string `json:"evidence_document_ids"`
	EvidenceURLs        []string `json:"evidence_urls"`
	Limitations         []string `json:"limitations"`
}

type UnlocatedDocument struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	URL        string `json:"url,omitempty"`
	SourceName string `json:"source_name"`
	Reason     string `json:"reason"`
}

type TimelineCoverage struct {
	Scope          string `json:"scope"`
	SourceCount    int    `json:"source_count"`
	DuplicateCount int    `json:"duplicate_count"`
	TimedCount     int    `json:"timed_count"`
	UnlocatedCount int    `json:"unlocated_count"`
	RelationReason string `json:"relation_reason"`
}

type TimelineResponse struct {
	Nodes      []TimelineNode      `json:"nodes"`
	Edges      []any               `json:"edges"`
	Unlocated  []UnlocatedDocument `json:"unlocated"`
	Coverage   TimelineCoverage    `json:"coverage"`
	Warnings   []string            `json:"warnings"`
	NextCursor string              `json:"next_cursor"`
}

// BuildTimeline sorts only source publication times. No relation or official
// identity fields exist in Document, so neither propagation nor official stages
// can be asserted from this evidence.
func BuildTimeline(docs []Document, offset, limit int) TimelineResponse {
	result := TimelineResponse{
		Nodes: []TimelineNode{}, Edges: []any{}, Unlocated: []UnlocatedDocument{},
		Warnings: []string{"insufficient_evidence"},
		Coverage: TimelineCoverage{Scope: "this_analysis_collected_documents", SourceCount: len(docs), RelationReason: "insufficient_evidence"},
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return result
	}
	ordered := append([]Document(nil), docs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	selected := make(map[string]Document, len(ordered))
	for _, doc := range ordered {
		parsedURL, validURL := sourceURL(doc.URL)
		key := doc.SourceType + "\x00" + doc.SourceName + "\x00"
		switch {
		case validURL:
			key += parsedURL
		case doc.ContentHash != "":
			key += "hash:" + doc.ContentHash
		default:
			key += "id:" + doc.ID
		}
		if previous, exists := selected[key]; exists {
			result.Coverage.DuplicateCount++
			previousTime, previousErr := time.Parse(time.RFC3339Nano, previous.PublishedAt)
			candidateTime, candidateErr := time.Parse(time.RFC3339Nano, doc.PublishedAt)
			if (previousErr != nil && candidateErr == nil) || (previousErr == nil && candidateErr == nil && candidateTime.Before(previousTime)) {
				selected[key] = doc
			}
			continue
		}
		selected[key] = doc
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		doc := selected[key]
		parsedURL, validURL := sourceURL(doc.URL)
		published, err := time.Parse(time.RFC3339Nano, doc.PublishedAt)
		switch {
		case doc.ID == "" || !validURL:
			result.Unlocated = append(result.Unlocated, UnlocatedDocument{DocumentID: doc.ID, Title: doc.Title, SourceName: doc.SourceName, Reason: "source_not_verifiable"})
		case err != nil:
			result.Unlocated = append(result.Unlocated, UnlocatedDocument{DocumentID: doc.ID, Title: doc.Title, URL: parsedURL, SourceName: doc.SourceName, Reason: "missing_published_at"})
		default:
			result.Nodes = append(result.Nodes, TimelineNode{
				eventAt:    published.UTC(),
				DocumentID: doc.ID, Kind: "source_record", Basis: "fact", EventTime: published.UTC().Format(time.RFC3339Nano),
				Title: doc.Title, SourceType: doc.SourceType, SourceName: doc.SourceName, Author: doc.Author,
				EvidenceDocumentIDs: []string{doc.ID}, EvidenceURLs: []string{parsedURL},
				Limitations: []string{"publication_time_from_collected_source_not_independently_verified"},
			})
		}
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		if !result.Nodes[i].eventAt.Equal(result.Nodes[j].eventAt) {
			return result.Nodes[i].eventAt.Before(result.Nodes[j].eventAt)
		}
		return result.Nodes[i].DocumentID < result.Nodes[j].DocumentID
	})
	if len(result.Nodes) > 0 {
		result.Nodes[0].Kind = "first_observed"
		result.Nodes[0].Limitations = append(result.Nodes[0].Limitations, "earliest_in_collected_verifiable_records_only")
	}
	result.Coverage.TimedCount = len(result.Nodes)
	result.Coverage.UnlocatedCount = len(result.Unlocated)
	if len(result.Unlocated) > 0 {
		result.Warnings = append(result.Warnings, "unlocated_documents")
	}
	if len(result.Nodes) == 0 {
		result.Warnings = append(result.Warnings, "no_timed_evidence")
	}
	size := len(result.Nodes) + len(result.Unlocated)
	if offset >= size {
		result.Nodes = []TimelineNode{}
		result.Unlocated = []UnlocatedDocument{}
		return result
	}
	end := offset + limit
	if end >= size {
		end = size
	} else {
		result.NextCursor = strconv.Itoa(end)
	}
	nodeStart := min(offset, len(result.Nodes))
	nodeEnd := min(end, len(result.Nodes))
	result.Nodes = result.Nodes[nodeStart:nodeEnd]
	unlocatedStart := max(0, offset-result.Coverage.TimedCount)
	unlocatedEnd := max(0, end-result.Coverage.TimedCount)
	result.Unlocated = result.Unlocated[unlocatedStart:unlocatedEnd]
	return result
}

func sourceURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil {
		return "", false
	}
	parsed.Fragment = ""
	return parsed.String(), true
}
