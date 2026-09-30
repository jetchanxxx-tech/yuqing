package v1_test

import (
	"net/http/httptest"
	"testing"
)

func TestMonitorPlansContractNoExecutionAndTenantIsolation(t *testing.T) {
	r, deps := newContractEnv(t)
	owner := issueToken(t, principal("analyst"))
	viewer := issueToken(t, principal("viewer"))
	other := principal("analyst")
	other.TenantID = "other-tenant"
	otherToken := issueToken(t, other)
	base := "/api/v1/monitor-plans"
	if got := doReq(t, r, "GET", base, "", nil); got.Code != 401 || decodeBody(t, got)["code"] != "UNAUTHORIZED" {
		t.Fatalf("unauthenticated list: %d %s", got.Code, got.Body.String())
	}
	beforePreview := doReq(t, r, "GET", base, owner, nil)
	if beforePreview.Code != 200 || len(decodeBody(t, beforePreview)["plans"].([]any)) != 0 {
		t.Fatalf("expected empty plans array: %d %s", beforePreview.Code, beforePreview.Body.String())
	}
	if got := doReq(t, r, "GET", base+"/templates", owner, nil); got.Code != 200 || len(decodeBody(t, got)["templates"].([]any)) != 6 {
		t.Fatalf("catalog: %d %s", got.Code, got.Body.String())
	}
	preview := doReq(t, r, "POST", base+"/preview", owner, map[string]any{"template_id": "brand_daily", "template_version": 1, "inputs": map[string]string{"brand_name": "品牌"}})
	if preview.Code != 200 || len(decodeBody(t, preview)["config"].(map[string]any)) != 7 {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	if got := doReq(t, r, "GET", base, owner, nil); got.Code != 200 || len(decodeBody(t, got)["plans"].([]any)) != 0 {
		t.Fatalf("preview wrote draft: %d %s", got.Code, got.Body.String())
	}
	if got := doReq(t, r, "POST", base, viewer, map[string]any{}); got.Code != 403 {
		t.Fatalf("viewer create: %d %s", got.Code, got.Body.String())
	}
	creditsBefore, err := deps.Credits.Balance(t.Context(), "t_contract")
	if err != nil {
		t.Fatal(err)
	}
	created := doReq(t, r, "POST", base, owner, map[string]any{"tenant_id": "other-tenant", "owner_id": "fake", "name": "草稿", "template_id": "brand_daily", "template_version": 1, "analysis_type": "brand", "inputs": map[string]string{"brand_name": "品牌"}, "config": decodeBody(t, preview)["config"]})
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	item := decodeBody(t, created)
	if item["tenant_id"] != "t_contract" || item["owner_id"] != "u_contract" || item["state"] != "draft" {
		t.Fatalf("identity/state: %v", item)
	}
	id := item["plan_id"].(string)
	otherUser := principal("analyst")
	otherUser.UserID = "another-user"
	if got := doReq(t, r, "PATCH", base+"/"+id, issueToken(t, otherUser), map[string]any{"revision": 1, "name": "stolen"}); got.Code != 403 || decodeBody(t, got)["request_id"] == "" {
		t.Fatalf("other user's patch: %d %s", got.Code, got.Body.String())
	}
	if got := doReq(t, r, "GET", base+"/"+id, otherToken, nil); got.Code != 404 {
		t.Fatalf("cross tenant read: %d", got.Code)
	}
	if got := doReq(t, r, "PATCH", base+"/"+id, otherToken, map[string]any{"revision": 1, "name": "stolen"}); got.Code != 404 {
		t.Fatalf("cross tenant patch: %d", got.Code)
	}
	if got := doReq(t, r, "PATCH", base+"/"+id, viewer, map[string]any{"revision": 1, "name": "stolen"}); got.Code != 403 {
		t.Fatalf("viewer patch: %d", got.Code)
	}
	updated := doReq(t, r, "PATCH", base+"/"+id, owner, map[string]any{"revision": 1, "name": "新草稿", "tenant_id": "other-tenant", "owner_id": "fake", "state": "draft"})
	if updated.Code != 200 || decodeBody(t, updated)["revision"] != float64(2) || decodeBody(t, updated)["tenant_id"] != "t_contract" || decodeBody(t, updated)["owner_id"] != "u_contract" {
		t.Fatalf("patch: %d %s", updated.Code, updated.Body.String())
	}
	if got := doReq(t, r, "PATCH", base+"/"+id, owner, map[string]any{"revision": 1, "name": "stale"}); got.Code != 409 || decodeBody(t, got)["request_id"] == "" {
		t.Fatalf("conflict: %d %s", got.Code, got.Body.String())
	}
	conflict := doReq(t, r, "PATCH", base+"/"+id, owner, map[string]any{"revision": 1, "name": "stale"})
	if _, ok := decodeBody(t, conflict)["details"]; !ok {
		t.Fatalf("conflict missing details field: %s", conflict.Body.String())
	}
	if got := doReq(t, r, "GET", base, otherToken, nil); got.Code != 200 || len(decodeBody(t, got)["plans"].([]any)) != 0 {
		t.Fatalf("cross tenant list: %d %s", got.Code, got.Body.String())
	}
	if got := doReq(t, r, "GET", base+"?limit=0", owner, nil); got.Code != 400 {
		t.Fatalf("invalid paging: %d", got.Code)
	}
	if got := doReq(t, r, "POST", base+"/"+id+"/run", owner, nil); got.Code != 404 {
		t.Fatalf("run must not be exposed: %d", got.Code)
	}
	creditsAfter, err := deps.Credits.Balance(t.Context(), "t_contract")
	if err != nil || creditsBefore != creditsAfter {
		t.Fatalf("credits changed: %v -> %v, %v", creditsBefore, creditsAfter, err)
	}
}

func TestMonitorPlansRejectsInvalidDraftConfigOnCreateAndPatch(t *testing.T) {
	r, _ := newContractEnv(t)
	owner := issueToken(t, principal("analyst"))
	base := "/api/v1/monitor-plans"
	preview := doReq(t, r, "POST", base+"/preview", owner, map[string]any{
		"template_id": "brand_daily", "template_version": 1, "inputs": map[string]string{"brand_name": "品牌"},
	})
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	createBody := func(config map[string]any) map[string]any {
		return map[string]any{"name": "draft", "template_id": "brand_daily", "template_version": 1,
			"inputs": map[string]string{"brand_name": "品牌"}, "config": config}
	}
	created := doReq(t, r, "POST", base, owner, createBody(decodeBody(t, preview)["config"].(map[string]any)))
	if created.Code != 201 {
		t.Fatalf("valid draft: %d %s", created.Code, created.Body.String())
	}
	id := decodeBody(t, created)["plan_id"].(string)
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"empty keywords", func(config map[string]any) { config["keywords"].(map[string]any)["value"] = []string{} }},
		{"unauthorized sources", func(config map[string]any) { config["sources"].(map[string]any)["value"] = []string{"forum"} }},
		{"unavailable proposed", func(config map[string]any) { config["sources"].(map[string]any)["state"] = "proposed" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := decodeBody(t, preview)["config"].(map[string]any)
			test.change(config)
			for _, request := range []struct {
				method string
				path   string
				body   any
			}{
				{"POST", base, createBody(config)},
				{"PATCH", base + "/" + id, map[string]any{"revision": 1, "config": config}},
			} {
				got := doReq(t, r, request.method, request.path, owner, request.body)
				body := decodeBody(t, got)
				if got.Code != 400 || body["code"] != "BAD_REQUEST" || body["request_id"] == "" {
					t.Fatalf("%s invalid config: %d %s", request.method, got.Code, got.Body.String())
				}
			}
		})
	}
	got := doReq(t, r, "GET", base+"/"+id, owner, nil)
	if got.Code != 200 || decodeBody(t, got)["revision"] != float64(1) {
		t.Fatalf("invalid patches changed draft: %d %s", got.Code, got.Body.String())
	}
}

func TestMonitorPlansPatchPreservesEditsAndResetsToTemplatePerspective(t *testing.T) {
	r, _ := newContractEnv(t)
	owner := issueToken(t, principal("analyst"))
	base := "/api/v1/monitor-plans"
	preview := doReq(t, r, "POST", base+"/preview", owner, map[string]any{"template_id": "quality_complaint", "template_version": 1, "analysis_type": "event", "inputs": map[string]string{"product_name": "耳机"}})
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	config := decodeBody(t, preview)["config"].(map[string]any)
	keywords := config["keywords"].(map[string]any)
	keywords["value"] = []string{"用户编辑词"}
	keywords["source"] = "user_edit"
	created := doReq(t, r, "POST", base, owner, map[string]any{"name": "投诉", "template_id": "quality_complaint", "template_version": 1, "analysis_type": "event", "inputs": map[string]string{"product_name": "耳机"}, "config": config})
	if created.Code != 201 {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	id := decodeBody(t, created)["plan_id"].(string)
	if got := doReq(t, r, "PATCH", base+"/"+id, owner, map[string]any{"revision": 1, "analysis_type": ""}); got.Code != 200 || decodeBody(t, got)["analysis_type"] != "brand" {
		t.Fatalf("reset = %d %s", got.Code, got.Body.String())
	}
	got := doReq(t, r, "GET", base+"/"+id, owner, nil)
	if got.Code != 200 || decodeBody(t, got)["config"].(map[string]any)["keywords"].(map[string]any)["source"] != "user_edit" {
		t.Fatalf("edits lost: %d %s", got.Code, got.Body.String())
	}
	config["sources"].(map[string]any)["state"] = "proposed"
	if got := doReq(t, r, "PATCH", base+"/"+id, owner, map[string]any{"revision": 2, "config": config}); got.Code != 400 {
		t.Fatalf("unverified source must not be proposed: %d %s", got.Code, got.Body.String())
	}
	config["sources"].(map[string]any)["state"] = "enabled"
	if got := doReq(t, r, "PATCH", base+"/"+id, owner, map[string]any{"revision": 2, "config": config}); got.Code != 400 {
		t.Fatalf("enabled source must be rejected: %d %s", got.Code, got.Body.String())
	}
}

func TestMonitorPlansDeleteDraftRequiresOwnerAndRevision(t *testing.T) {
	r, _ := newContractEnv(t)
	owner := issueToken(t, principal("analyst"))
	base := "/api/v1/monitor-plans"
	preview := doReq(t, r, "POST", base+"/preview", owner, map[string]any{"template_id": "brand_daily", "template_version": 1, "inputs": map[string]string{"brand_name": "品牌"}})
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	created := doReq(t, r, "POST", base, owner, map[string]any{"name": "draft", "template_id": "brand_daily", "template_version": 1, "inputs": map[string]string{"brand_name": "品牌"}, "config": decodeBody(t, preview)["config"]})
	if created.Code != 201 {
		t.Fatal(created.Body.String())
	}
	id := decodeBody(t, created)["plan_id"].(string)
	other := principal("analyst")
	other.UserID = "other-user"
	if got := doReq(t, r, "DELETE", base+"/"+id, issueToken(t, other), map[string]int{"revision": 1}); got.Code != 403 {
		t.Fatalf("other user delete: %d", got.Code)
	}
	if got := doReq(t, r, "DELETE", base+"/"+id, owner, map[string]int{"revision": 0}); got.Code != 400 {
		t.Fatalf("missing revision: %d", got.Code)
	}
	if got := doReq(t, r, "DELETE", base+"/"+id, owner, map[string]int{"revision": 2}); got.Code != 409 {
		t.Fatalf("stale delete: %d %s", got.Code, got.Body.String())
	}
	if got := doReq(t, r, "DELETE", base+"/"+id, owner, map[string]int{"revision": 1}); got.Code != 204 {
		t.Fatalf("delete: %d %s", got.Code, got.Body.String())
	}
	if got := doReq(t, r, "GET", base+"/"+id, owner, nil); got.Code != 404 {
		t.Fatalf("deleted draft still present: %d", got.Code)
	}
}

func TestMonitorPlansUnknownPlanUnsafeStateAndPageCount(t *testing.T) {
	r, _ := newContractEnv(t)
	base := "/api/v1/monitor-plans"
	badPlan := principal("analyst")
	badPlan.PlanCode = "unknown"
	invalid := doReq(t, r, "POST", base+"/preview", issueToken(t, badPlan), map[string]any{"template_id": "brand_daily", "template_version": 1, "inputs": map[string]string{"brand_name": "品牌"}})
	if invalid.Code != 400 || decodeBody(t, invalid)["code"] != "BAD_REQUEST" || decodeBody(t, invalid)["request_id"] == "" {
		t.Fatalf("unknown plan: %d %s", invalid.Code, invalid.Body.String())
	}
	owner := issueToken(t, principal("analyst"))
	preview := doReq(t, r, "POST", base+"/preview", owner, map[string]any{"template_id": "brand_daily", "template_version": 1, "inputs": map[string]string{"brand_name": "品牌"}})
	config := decodeBody(t, preview)["config"]
	create := func(name string, state string) *httptest.ResponseRecorder {
		return doReq(t, r, "POST", base, owner, map[string]any{"name": name, "state": state, "template_id": "brand_daily", "template_version": 1, "inputs": map[string]string{"brand_name": "品牌"}, "config": config})
	}
	if got := create("invalid", "enabled"); got.Code != 400 || decodeBody(t, got)["code"] != "BAD_REQUEST" {
		t.Fatalf("enabled state accepted: %d %s", got.Code, got.Body.String())
	}
	first := create("first", "draft")
	second := create("second", "draft")
	if first.Code != 201 || second.Code != 201 {
		t.Fatalf("creates: %d %d", first.Code, second.Code)
	}
	id := decodeBody(t, first)["plan_id"].(string)
	if got := doReq(t, r, "PATCH", base+"/"+id, owner, map[string]any{"revision": 1, "state": "enabled", "tenant_id": "other"}); got.Code != 400 {
		t.Fatalf("state escalation: %d", got.Code)
	}
	list := doReq(t, r, "GET", base+"?limit=1", owner, nil)
	result := decodeBody(t, list)
	if list.Code != 200 || result["page_count"] != float64(1) || len(result["plans"].([]any)) != 1 {
		t.Fatalf("page semantics: %d %s", list.Code, list.Body.String())
	}
	if _, ok := result["total"]; ok {
		t.Fatalf("page size falsely labeled total: %s", list.Body.String())
	}
	other := principal("analyst")
	other.TenantID = "other"
	missing := doReq(t, r, "GET", base+"/"+id, issueToken(t, other), nil)
	if missing.Code != 404 || decodeBody(t, missing)["request_id"] == "" {
		t.Fatalf("tenant isolation/error envelope: %d %s", missing.Code, missing.Body.String())
	}
	if _, ok := decodeBody(t, missing)["details"]; !ok {
		t.Fatalf("404 missing details field: %s", missing.Body.String())
	}
	stale := doReq(t, r, "PATCH", base+"/"+id, owner, map[string]any{"revision": 2, "name": "stale"})
	if stale.Code != 409 || decodeBody(t, stale)["code"] != "CONFLICT" || decodeBody(t, stale)["request_id"] == "" {
		t.Fatalf("revision conflict: %d %s", stale.Code, stale.Body.String())
	}
	if got := doReq(t, r, "GET", base, "", nil); got.Code != 401 {
		t.Fatalf("anonymous list: %d %s", got.Code, got.Body.String())
	}
	if got := doReq(t, r, "POST", base, issueToken(t, principal("viewer")), map[string]any{}); got.Code != 403 {
		t.Fatalf("viewer create: %d %s", got.Code, got.Body.String())
	}
}
