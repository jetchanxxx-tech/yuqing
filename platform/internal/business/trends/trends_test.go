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

// newFakeRSSHub 起一个罐头 RSSHub：/weibo/search/hot 返回有效 feed，
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
	svc := NewService("http://127.0.0.1:1", 1*time.Hour)
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

func TestRefreshAll_parsesFeedAndTruncatesAt20(t *testing.T) {
	ts := newFakeRSSHub()
	defer ts.Close()

	svc := NewService(ts.URL, 1*time.Hour)
	defer svc.Close()

	// 等待后台首轮刷新完成
	deadline := time.Now().Add(2 * time.Second)
	for {
		weibo := svc.cache["微博"]
		if weibo != nil && weibo.Status == StateOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for initial refresh")
		}
		time.Sleep(10 * time.Millisecond)
	}

	weibo := svc.cache["微博"]
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
	svc := NewService(ts.URL, 1*time.Hour)

	// 等首轮成功
	deadline := time.Now().Add(2 * time.Second)
	for svc.cache["微博"] == nil || svc.cache["微博"].Status != StateOK {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for first successful refresh")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ts.Close() // 关掉 fake server，后续刷新必失败

	svc.refreshAll(context.Background())

	weibo := svc.cache["微博"]
	if weibo.Status != StateStale {
		t.Errorf("status = %q, want stale（失败必须保留 last-good）", weibo.Status)
	}
	if len(weibo.Items) != 20 {
		t.Errorf("last-good items lost: %d", len(weibo.Items))
	}
}

func TestRefreshAll_neverSucceededStaysError(t *testing.T) {
	svc := NewService("http://127.0.0.1:1", 1*time.Hour)
	defer svc.Close()

	svc.refreshAll(context.Background())

	weibo := svc.cache["微博"]
	if weibo.Status != StateError {
		t.Errorf("status = %q, want error（从未成功过不能伪造 stale）", weibo.Status)
	}
	if len(weibo.Items) != 0 {
		t.Errorf("items = %d, want 0", len(weibo.Items))
	}
}

func TestFetchPlatform_route404IsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	svc := NewService(ts.URL, 1*time.Hour)
	defer svc.Close()

	snap := svc.fetchPlatform(context.Background(), "抖音", "/douyin/hot")
	if snap.Status != StateError {
		t.Errorf("status = %q, want error for HTTP 404", snap.Status)
	}
}
