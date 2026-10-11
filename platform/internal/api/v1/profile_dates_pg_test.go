package v1_test

import (
	"context"
	"net/http"
	"testing"
)

func TestProfileDatesPGUserListBounds(t *testing.T) {
	e := newBillingActorPGEnv(t)
	token, _, admin := mustRegister(t, e.router, "dates-admin@example.invalid", "Date Admin")
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO platform_user_roles(user_id,role) VALUES ($1,'platform_admin')`, admin["user_id"]); err != nil {
		t.Fatal(err)
	}
	for i, at := range []string{"2026-03-08T04:59:59Z", "2026-03-08T05:00:00Z", "2026-03-09T03:59:59Z", "2026-03-09T04:00:00Z"} {
		ids := []string{"date-before", "date-start", "date-last", "date-after"}
		if _, err := e.pool.Exec(context.Background(), `INSERT INTO users(id,email,password_hash,name,status,created_at) VALUES ($1,$2,'fixture-unused-hash','K8 date fixture','active',$3::timestamptz)`, ids[i], ids[i]+"@example.invalid", at); err != nil {
			t.Fatal(err)
		}
	}
	w := doReq(t, e.router, http.MethodGet, "/api/v1/admin/users?q=K8%20date%20fixture&created_from=2026-03-08T05:00:00Z&created_to=2026-03-09T04:00:00Z&page_size=1", token, nil)
	if w.Code != 200 {
		t.Fatalf("date range status=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	items := body["items"].([]any)
	if body["total"] != float64(2) || len(items) != 1 || items[0].(map[string]any)["id"] != "date-start" {
		t.Errorf("inclusive/exclusive bounds ignored or pagination total inconsistent: %s", w.Body.String())
	}
	for _, query := range []string{"created_from=bad", "created_from=2026-03-08", "created_from=2026-03-09T04:00:00Z&created_to=2026-03-08T05:00:00Z"} {
		w = doReq(t, e.router, http.MethodGet, "/api/v1/admin/users?"+query, token, nil)
		if w.Code != 400 {
			t.Errorf("invalid range accepted: %s status=%d", query, w.Code)
		}
	}
}
