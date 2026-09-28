package analysis

import "testing"

func TestBuildTimelineEvidence(t *testing.T) {
	docs := []Document{
		{ID: "later", URL: "https://example.org/2", SourceName: "news", PublishedAt: "2026-09-28T08:00:00Z"},
		{ID: "first", URL: "https://example.org/1", SourceName: "news", PublishedAt: "2026-09-28T07:00:00Z"},
		{ID: "duplicate", URL: "https://example.org/1", SourceName: "news", PublishedAt: "2026-09-28T07:00:00Z"},
		{ID: "tie", URL: "https://example.org/3", SourceName: "news", PublishedAt: "2026-09-28T07:00:00Z"},
		{ID: "unknown", URL: "https://example.org/4", SourceName: "news"},
		{ID: "invalid", URL: "https://example.org/5", SourceName: "news", PublishedAt: "not-a-date"},
	}
	first := BuildTimeline(docs, 0, 2)
	if len(first.Nodes) != 2 || first.Nodes[0].DocumentID != "duplicate" || first.Nodes[1].DocumentID != "tie" {
		t.Fatalf("first page: %+v", first.Nodes)
	}
	if first.Nodes[0].Kind != "first_observed" || first.Nodes[0].Basis != "fact" || first.Nodes[0].EvidenceURLs[0] != "https://example.org/1" {
		t.Fatalf("first node: %+v", first.Nodes[0])
	}
	if first.NextCursor != "2" || first.Coverage.DuplicateCount != 1 || first.Coverage.TimedCount != 3 || first.Coverage.UnlocatedCount != 2 {
		t.Fatalf("coverage: %+v", first)
	}
	second := BuildTimeline(docs, 2, 2)
	if len(second.Nodes) != 1 || second.Nodes[0].DocumentID != "later" || len(second.Unlocated) != 1 || second.NextCursor != "4" {
		t.Fatalf("second page: %+v", second)
	}
	third := BuildTimeline(docs, 4, 2)
	if len(third.Nodes) != 0 || len(third.Unlocated) != 1 || third.NextCursor != "" {
		t.Fatalf("third page: %+v", third)
	}
	if len(first.Edges) != 0 || first.Coverage.RelationReason != "insufficient_evidence" {
		t.Fatalf("invented propagation: %+v", first)
	}
}

func TestBuildTimelineDoesNotInferOfficialResponseOrCrossPlatformRelations(t *testing.T) {
	docs := []Document{
		{ID: "one", URL: "https://a.example/post", SourceName: "a", ContentHash: "same", PublishedAt: "2026-09-28T07:00:00Z", Title: "official response"},
		{ID: "two", URL: "https://b.example/post", SourceName: "b", ContentHash: "same", PublishedAt: "2026-09-28T08:00:00Z"},
		{ID: "no-url", SourceName: "a", PublishedAt: "2026-09-28T09:00:00Z"},
	}
	result := BuildTimeline(docs, 0, 20)
	if len(result.Nodes) != 2 || result.Nodes[1].DocumentID != "two" || len(result.Unlocated) != 1 {
		t.Fatalf("distinct evidence: %+v", result)
	}
	if len(result.Edges) != 0 || result.Nodes[0].Kind == "official_response" {
		t.Fatalf("unverified stages: %+v", result)
	}
}

func TestBuildTimelinePrefersDatedDuplicate(t *testing.T) {
	result := BuildTimeline([]Document{
		{ID: "a", URL: "https://example.org/one", SourceName: "news"},
		{ID: "b", URL: "https://example.org/one", SourceName: "news", PublishedAt: "2026-09-28T07:00:00Z"},
	}, 0, 10)
	if len(result.Nodes) != 1 || result.Nodes[0].DocumentID != "b" || result.Coverage.DuplicateCount != 1 {
		t.Fatalf("dated duplicate must be selected: %+v", result)
	}
}

func TestBuildTimelineOrdersFractionalSecondsAndTimezones(t *testing.T) {
	result := BuildTimeline([]Document{
		{ID: "later", SourceName: "news", URL: "https://example.org/later", PublishedAt: "2026-09-28T15:00:00.1+08:00"},
		{ID: "earlier", SourceName: "news", URL: "https://example.org/earlier", PublishedAt: "2026-09-28T07:00:00Z"},
	}, 0, 10)
	if len(result.Nodes) != 2 || result.Nodes[0].DocumentID != "earlier" || result.Nodes[1].DocumentID != "later" {
		t.Fatalf("must compare instants, not timestamp strings: %+v", result.Nodes)
	}
}
