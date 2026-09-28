package v1_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/yuqing/platform/internal/business/analysis"
	"github.com/yuqing/platform/internal/platform/auth"
)

func TestContractTimelineTenantEvidenceAndLimits(t *testing.T) {
	r, deps := newContractEnv(t)
	access, _, user := mustRegister(t, r, "timeline-owner@example.com", "Owner")
	other, _, _ := mustRegister(t, r, "timeline-other@example.com", "Other")
	tenantID := user["tenant_id"].(string)
	created := doReq(t, r, http.MethodPost, "/api/v1/analyses", access, map[string]any{"name": "Timeline", "keywords": []string{"incident"}, "sources": []string{"news"}})
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	id := decodeBody(t, created)["id"].(string)
	deps.Analysis.AddDocuments(context.Background(), tenantID, id, []analysis.Document{
		{ID: "1", URL: "https://example.org/1", SourceName: "news", PublishedAt: "2026-09-28T07:00:00Z"},
		{ID: "2", URL: "https://example.org/2", SourceName: "news"},
	})
	path := "/api/v1/analyses/" + id + "/timeline"
	resp := doReq(t, r, http.MethodGet, path+"?limit=1", access, nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("timeline: %d %s", resp.Code, resp.Body.String())
	}
	body := decodeBody(t, resp)
	if len(body["nodes"].([]any)) != 1 || len(body["edges"].([]any)) != 0 || len(body["unlocated"].([]any)) != 0 || body["next_cursor"] != "1" {
		t.Fatalf("page: %v", body)
	}
	if next := decodeBody(t, doReq(t, r, http.MethodGet, path+"?limit=1&cursor=1", access, nil)); len(next["unlocated"].([]any)) != 1 || next["next_cursor"] != "" {
		t.Fatalf("next page: %v", next)
	}
	for _, p := range []string{path + "?limit=101", path + "?cursor=-1", path + "?cursor=wat"} {
		if got := doReq(t, r, http.MethodGet, p, access, nil); got.Code != http.StatusBadRequest {
			t.Errorf("invalid query %s: %d", p, got.Code)
		}
	}
	if got := doReq(t, r, http.MethodGet, path, other, nil); got.Code != http.StatusNotFound {
		t.Fatalf("cross tenant: %d %s", got.Code, got.Body.String())
	}
	if got := doReq(t, r, http.MethodGet, path, "", nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", got.Code)
	}
	if got := doReq(t, r, http.MethodGet, path, issueToken(t, auth.Principal{
		UserID: "no-permission", TenantID: tenantID, Roles: []string{"unknown"}, TenantStatus: "active",
	}), nil); got.Code != http.StatusForbidden {
		t.Fatalf("missing analyses:read permission: %d", got.Code)
	}
	additional := make([]analysis.Document, 2001)
	for index := range additional {
		additional[index] = analysis.Document{ID: "extra-" + strconv.Itoa(index)}
	}
	deps.Analysis.AddDocuments(context.Background(), tenantID, id, additional)
	if got := doReq(t, r, http.MethodGet, path, access, nil); got.Code != http.StatusBadRequest {
		t.Fatalf("unbounded analysis should fail closed: %d", got.Code)
	}
}
