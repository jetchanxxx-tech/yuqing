package v1_test

import (
	"net/http"
	"testing"
)

func TestContractAnalysisScenePersistsAndRejectsUnknown(t *testing.T) {
	router, _ := newContractEnv(t)
	access, _, _ := mustRegister(t, router, "scene-contract@example.com", "Scene")
	created := doReq(t, router, "POST", "/api/v1/analyses", access, map[string]any{
		"name": "质量投诉", "analysis_type": "event", "scenario_template_id": "quality_complaint",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create scene: %d %s", created.Code, created.Body.String())
	}
	body := decodeBody(t, created)
	id := body["id"].(string)
	read := decodeBody(t, doReq(t, router, "GET", "/api/v1/analyses/"+id, access, nil))
	if read["scenario_template_id"] != "quality_complaint" || read["analysis_type"] != "event" {
		t.Fatalf("scene or user perspective lost: %+v", read)
	}
	rejected := doReq(t, router, "POST", "/api/v1/analyses", access, map[string]any{
		"name": "未知场景", "scenario_template_id": "invented",
	})
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("unknown scene accepted: %d %s", rejected.Code, rejected.Body.String())
	}
}
