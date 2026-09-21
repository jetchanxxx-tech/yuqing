package trends

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newFakeRSSHub 起一个罐头 RSSHub：/weibo/search/hot 返回有效 JSON Feed，
// 其余路由 404（模拟部分平台路由失效）。
func newFakeRSSHub() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/weibo/search/hot", func(w http.ResponseWriter, _ *http.Request) {
		var b strings.Builder
		b.WriteString(`{"version":"https://jsonfeed.org/version/1.1","items":[`)
		for i := 1; i <= 25; i++ {
			if i > 1 {
				b.WriteString(",")
			}
			b.WriteString(fmt.Sprintf(`{"id":"https://s.weibo.com/%d","url":"https://s.weibo.com/%d","title":"热搜%d","description":"%d万"}`, i, i, i, i*100))
		}
		b.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(b.String()))
	})
	return httptest.NewServer(mux)
}

func TestNewService_prefillsAllPlatformsInstantly(t *testing.T) {
	// 指向不可达地址：NewService 必须立即返回（同步路径无网络 IO）
	start := time.Now()
	svc := NewService("http://127.0.0.1:1", 1*time.Hour, nil)
	defer svc.Close()
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("NewService blocked %v (must not sync-wait on network)", elapsed)
	}

	platforms := svc.GetAll(context.Background())
	if len(platforms) != len(hotPlatforms) {
		t.Fatalf("platforms = %d, want %d", len(platforms), len(hotPlatforms))
	}
	for _, p := range platforms {
		if p.Status != StateError {
			t.Errorf("platform %s initial status = %q, want error", p.Name, p.Status)
		}
	}
}

func TestGetAll_stableTabOrder(t *testing.T) {
	svc := NewService("http://127.0.0.1:1", 1*time.Hour, nil)
	defer svc.Close()

	// 多次读取顺序必须稳定（map 迭代随机，GetAll 必须按 hotPlatforms 排序）
	first := svc.GetAll(context.Background())
	for i := 0; i < 5; i++ {
		again := svc.GetAll(context.Background())
		for j := range first {
			if first[j].Name != again[j].Name {
				t.Fatalf("tab order unstable: run%d[%d]=%s, want %s",
					i, j, again[j].Name, first[j].Name)
			}
		}
	}
	if first[0].Name != "微博" {
		t.Errorf("first tab = %q, want 微博", first[0].Name)
	}
}

func TestRefreshAll_parsesFeedAndTruncatesAt20(t *testing.T) {
	ts := newFakeRSSHub()
	defer ts.Close()

	svc := NewService(ts.URL, 1*time.Hour, nil)
	defer svc.Close()

	waitOK(t, svc)

	weibo, ok := svc.Get("微博")
	if !ok || weibo.Status != StateOK {
		t.Fatalf("weibo status = %q, want ok", weibo.Status)
	}
	if len(weibo.Items) != 20 {
		t.Errorf("items = %d, want 20 (truncated)", len(weibo.Items))
	}
	if weibo.Items[0].Title != "热搜1" || weibo.Items[0].Rank != 1 {
		t.Errorf("item[0] = %+v", weibo.Items[0])
	}
	if weibo.Items[0].Hot != "100万" {
		t.Errorf("hot = %q, want 100万", weibo.Items[0].Hot)
	}
	if weibo.Items[19].Rank != 20 {
		t.Errorf("item[19].rank = %d, want 20", weibo.Items[19].Rank)
	}
}

func TestRefreshAll_preservesLastGoodOnFailure(t *testing.T) {
	ts := newFakeRSSHub()
	svc := NewService(ts.URL, 1*time.Hour, nil)

	waitOK(t, svc)
	ts.Close() // 关掉 fake server，后续刷新必失败

	svc.refreshAll(context.Background())

	weibo, _ := svc.Get("微博")
	if weibo.Status != StateStale {
		t.Errorf("status = %q, want stale（失败必须保留 last-good）", weibo.Status)
	}
	if len(weibo.Items) != 20 {
		t.Errorf("last-good items lost: %d", len(weibo.Items))
	}
}

func TestRefreshAll_neverSucceededStaysErrorWithReason(t *testing.T) {
	svc := NewService("http://127.0.0.1:1", 1*time.Hour, nil)
	defer svc.Close()

	svc.refreshAll(context.Background())

	weibo, _ := svc.Get("微博")
	if weibo.Status != StateError {
		t.Errorf("status = %q, want error（从未成功过不能伪造 stale）", weibo.Status)
	}
	if len(weibo.Items) != 0 {
		t.Errorf("items = %d, want 0", len(weibo.Items))
	}
	// 审核中1：真实失败原因必须覆盖预填的「正在加载」
	if weibo.Error == "" || weibo.Error == "正在加载" {
		t.Errorf("error = %q, want real failure reason", weibo.Error)
	}
}

func TestRefreshAll_emptyUpstreamListKeepsLastGood(t *testing.T) {
	// 审核中2：上游 200 + 空 items 视为失败，不得以「ok+0条」清掉 last-good
	ts := newFakeRSSHub()
	mux := http.NewServeMux()
	mux.HandleFunc("/weibo/search/hot", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	empty := httptest.NewServer(mux)
	defer empty.Close()

	svc := NewService(ts.URL, 1*time.Hour, nil)
	waitOK(t, svc)

	// 切换到空列表上游
	svc.mu.Lock()
	svc.rsshubBase = empty.URL
	svc.mu.Unlock()

	svc.refreshAll(context.Background())

	weibo, _ := svc.Get("微博")
	if weibo.Status == StateOK && len(weibo.Items) == 0 {
		t.Fatal("empty upstream wiped last-good with ok+0 items")
	}
	if len(weibo.Items) != 20 {
		t.Errorf("last-good items lost: %d", len(weibo.Items))
	}
}

func TestFetchPlatform_route404IsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	svc := NewService(ts.URL, 1*time.Hour, nil)
	defer svc.Close()

	snap := svc.fetchPlatform(context.Background(), "抖音", "/douyin/hot")
	if snap.Status != StateError {
		t.Errorf("status = %q, want error for HTTP 404", snap.Status)
	}
}

// waitOK 轮询直到微博平台首轮刷新成功（走锁内访问器，禁止裸读 cache）。
func waitOK(t *testing.T, svc *Service) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if p, ok := svc.Get("微博"); ok && p.Status == StateOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for initial successful refresh")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
