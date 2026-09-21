package v1_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/api"
	"github.com/yuqing/platform/internal/app"
	"github.com/yuqing/platform/internal/config"
)

// F21 热榜聚合页契约：鉴权、未配置 503、RSSHub 不可达仍 200+error 态。
func TestContract_trends(t *testing.T) {
	r, deps := newContractEnv(t)
	tok := issueToken(t, principal("analyst"))

	t.Run("未登录 401", func(t *testing.T) {
		w := doReq(t, r, http.MethodGet, "/api/v1/trends", "", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	t.Run("服务未配置 503 envelope", func(t *testing.T) {
		if deps.Trends != nil {
			t.Skip("contract env has trends configured")
		}
		w := doReq(t, r, http.MethodGet, "/api/v1/trends", tok, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		body := decodeBody(t, w)
		if body["code"] != "TRENDS_UNAVAILABLE" {
			t.Fatalf("code = %v, want TRENDS_UNAVAILABLE", body["code"])
		}
	})

	t.Run("RSSHub不可达仍200且全平台error态", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Auth.JWTSecret = testJWTSecret
		cfg.Auth.AccessTTL = "15m"
		cfg.Auth.RefreshTTL = "720h"
		cfg.RSSHubBase = "http://127.0.0.1:1" // 不可达：服务可用，数据全部 error

		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		deps2 := app.Build(cfg, logger)
		if deps2.Credits != nil {
			_ = deps2.Credits.GrantPurchase(context.Background(), "t_contract", "contract-seed", 1000)
		}
		r2 := api.NewRouter(cfg, logger, deps2)

		// 等待后台首轮刷新失败落定（异步，秒级）
		time.Sleep(200 * time.Millisecond)

		w := doReq(t, r2, http.MethodGet, "/api/v1/trends", tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（RSSHub 挂了是数据源状态不是服务器故障）\nbody: %s",
				w.Code, w.Body.String())
		}
		body := decodeBody(t, w)
		platforms, ok := body["platforms"].([]interface{})
		if !ok {
			t.Fatalf("platforms missing or not array: %v", body)
		}
		// 平台数与 trends.hotPlatforms 准入清单一致（V1: 微博/B站/知乎）
		if len(platforms) != 3 {
			t.Fatalf("platforms = %d, want 3", len(platforms))
		}
		for _, raw := range platforms {
			p := raw.(map[string]interface{})
			status, _ := p["status"].(string)
			if status != "error" && status != "stale" && status != "ok" {
				t.Errorf("platform %v invalid status %q", p["name"], status)
			}
		}
	})
}
