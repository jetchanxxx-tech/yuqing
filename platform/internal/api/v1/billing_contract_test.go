package v1_test

import (
	"net/http"
	"testing"
)

// 收费体系（方案 B）端点契约：锁定响应形状与 fail-closed 行为。
func TestContract_billing_planB(t *testing.T) {
	r, deps := newContractEnv(t)
	tok := issueToken(t, principal("analyst"))

	t.Run("GET /billing/plans 返回方案 B 目录 + 渠道列表", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/billing/plans", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d\nbody: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		plans, _ := body["plans"].([]interface{})
		if len(plans) != 4 {
			t.Fatalf("plans = %d, want 4（free/lite/pro/enterprise）", len(plans))
		}
		addons, _ := body["addons"].([]interface{})
		if len(addons) != 1 {
			t.Fatalf("addons = %d, want 1（加购包）", len(addons))
		}
		channels, _ := body["channels"].([]interface{})
		if len(channels) != 0 {
			t.Fatalf("channels = %v, want 空（测试环境未配置渠道）", channels)
		}
	})

	t.Run("GET /billing/credits 返回余额", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/billing/credits", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d\nbody: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		if _, ok := body["balance"].(float64); !ok {
			t.Fatalf("balance 缺失或非数字: %v", body["balance"])
		}
	})

	t.Run("POST /billing/orders 未知商品被拒", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/billing/orders", tok,
			map[string]any{"sku_code": "nope", "channel": "alipay"})
		if w.Code == http.StatusCreated {
			t.Fatal("未知商品不应创建成功")
		}
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404\nbody: %s", w.Code, w.Body.String())
		}
	})

	t.Run("POST /billing/orders 未配置渠道 fail-closed", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/billing/orders", tok,
			map[string]any{"sku_code": "lite", "channel": "alipay"})
		if w.Code == http.StatusCreated {
			t.Fatal("未配置渠道不应创建成功（防线下收款无凭证）")
		}
	})

	t.Run("GET /billing/orders/:id 未知订单 404 envelope", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/billing/orders/missing", tok, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404\nbody: %s", w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		if body["code"] != "NOT_FOUND" {
			t.Fatalf("code = %v, want NOT_FOUND", body["code"])
		}
	})

	t.Run("POST 回调无鉴权但渠道未配置时被拒", func(t *testing.T) {
		w := doReq(t, r, http.MethodPost, "/api/v1/callbacks/payment/alipay", "", nil)
		if w.Code == http.StatusOK {
			t.Fatal("未配置渠道的回调不应 ACK")
		}
		_ = deps
	})
}
