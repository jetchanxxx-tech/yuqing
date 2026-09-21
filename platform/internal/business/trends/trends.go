// Package trends provides hot-topic aggregation from multiple platforms via RSSHub.
// 纯快照零存储：内存缓存 + 定时刷新，失败保留 last-good 三态（ok/stale/error）。
package trends

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Platform represents a single platform's hot topics snapshot.
type Platform struct {
	Name      string    `json:"name"`       // "微博" | "B站" | "知乎" | "抖音" | "小红书"
	Items     []Item    `json:"items"`      // 热榜条目
	Status    string    `json:"status"`     // "ok" | "stale" | "error"
	Error     string    `json:"error"`      // Status=error 时的错误描述
	UpdatedAt time.Time `json:"updated_at"` // 最后一次成功抓取时间（stale 时保留旧值）
}

const (
	StateOK    = "ok"
	StateStale = "stale"
	StateError = "error"
)

// Item is a single hot topic entry.
type Item struct {
	Rank  int    `json:"rank"`          // 1 起的排名
	Title string `json:"title"`
	URL   string `json:"url"`
	Hot   string `json:"hot,omitempty"` // 热度值（可选）
}

// Service aggregates hot topics from RSSHub with in-memory caching.
type Service struct {
	rsshubBase    string
	refreshPeriod time.Duration

	mu    sync.RWMutex
	cache map[string]*Platform // key = platform name

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// hotPlatforms 是 V1 平台清单与 RSSHub 路由（顺序即前端 Tab 顺序）。
var hotPlatforms = []struct {
	name  string
	route string
}{
	{"微博", "/weibo/search/hot"},
	{"B站", "/bilibili/ranking/0/3"},
	{"知乎", "/zhihu/hotlist"},
	{"抖音", "/douyin/hot"},
	{"小红书", "/xiaohongshu/board/homefeed_recommend"},
}

// NewService creates a trends service and starts the refresh ticker.
// rsshubBase should be "http://127.0.0.1:1200" for local deployment.
//
// cache 先预填全部平台的 error 空快照：GetAll 永远返回完整 Tab 清单
//（首屏立即可渲染「加载中/暂不可用」），首轮刷新在后台异步完成 ——
// NewService 绝不同步等待网络（RSSHub 挂了不能拖慢 server 启动）。
func NewService(rsshubBase string, refreshPeriod time.Duration) *Service {
	s := &Service{
		rsshubBase:    rsshubBase,
		refreshPeriod: refreshPeriod,
		cache:         make(map[string]*Platform),
		stopCh:        make(chan struct{}),
	}

	for _, p := range hotPlatforms {
		s.cache[p.name] = &Platform{Name: p.name, Status: StateError, Error: "正在加载"}
	}

	// 后台异步：首轮刷新 + ticker 循环
	s.wg.Add(1)
	go s.refreshLoop()

	return s
}

// GetAll returns cached snapshots for all platforms.
func (s *Service) GetAll(ctx context.Context) []*Platform {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Platform, 0, len(s.cache))
	for _, p := range s.cache {
		result = append(result, p)
	}
	return result
}

// Close stops the refresh ticker.
func (s *Service) Close() {
	close(s.stopCh)
	s.wg.Wait()
}

func (s *Service) refreshLoop() {
	defer s.wg.Done()
	// 首轮立即刷，之后按周期
	s.refreshAll(context.Background())

	ticker := time.NewTicker(s.refreshPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.refreshAll(context.Background())
		case <-s.stopCh:
			return
		}
	}
}

func (s *Service) refreshAll(ctx context.Context) {
	for _, p := range hotPlatforms {
		snapshot := s.fetchPlatform(ctx, p.name, p.route)

		s.mu.Lock()
		old := s.cache[p.name]
		if snapshot.Status == StateOK || old == nil {
			s.cache[p.name] = snapshot
		} else if len(old.Items) > 0 {
			// 有过 last-good 数据 → 保留并标记 stale
			old.Status = StateStale
			s.cache[p.name] = old
		}
		// else：从未成功过（预填 error 空快照），保持 error
		s.mu.Unlock()
	}
}

func (s *Service) fetchPlatform(ctx context.Context, name, route string) *Platform {
	// ?format=json：RSSHub 支持 JSON Feed 输出，避免在 Go 里解析 XML；
	// limit=20 直接在源头截断。
	url := s.rsshubBase + route + "?format=json&limit=20"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return &Platform{Name: name, Status: StateError, Error: err.Error()}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &Platform{Name: name, Status: StateError, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &Platform{
			Name:   name,
			Status: StateError,
			Error:  fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	var feed jsonFeed
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return &Platform{Name: name, Status: StateError, Error: "JSON decode: " + err.Error()}
	}

	items := make([]Item, 0, len(feed.Items))
	for _, it := range feed.Items {
		if len(items) >= 20 {
			break // 双保险：limit 参数失效时本地兜底
		}
		link := it.URL
		if link == "" {
			link = it.ID // JSON Feed 规范：url 缺省时 id 通常是链接
		}
		items = append(items, Item{
			Rank:  len(items) + 1,
			Title: strings.TrimSpace(it.Title),
			URL:   link,
			Hot:   stripHTML(it.Description), // 热度值通常在 description（HTML 片段）
		})
	}

	return &Platform{
		Name:      name,
		Items:     items,
		Status:    StateOK,
		UpdatedAt: time.Now(),
	}
}

// jsonFeed 是 RSSHub ?format=json 输出（JSON Feed 1.0）的最小解析结构。
type jsonFeed struct {
	Items []struct {
		ID          string `json:"id"`
		URL         string `json:"url"`
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"items"`
}

// stripHTML 清洗 description 里的 HTML 标签，保留纯文本热度值。
var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 32 {
		s = string([]rune(s)[:32]) + "…"
	}
	return s
}
