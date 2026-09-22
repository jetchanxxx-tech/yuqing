// Package trends provides hot-topic aggregation from multiple platforms via RSSHub.
// 纯快照零存储：进程内共享缓存 + 定时刷新，失败保留 last-good 三态（ok/stale/error）。
//
// 并发约定（审核修复）：cache 存 Platform 值而非指针 —— GetAll/Get 在锁内
// 按值拷贝返回，调用方（JSON 序列化、测试断言）永远不与刷新 goroutine
// 共享可变内存；map 的读写全部在锁内。
package trends

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Platform represents a single platform's hot topics snapshot.
type Platform struct {
	Name      string    `json:"name"`       // "微博" | "B站" | "知乎"
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
	Rank  int    `json:"rank"` // 1 起的排名
	Title string `json:"title"`
	URL   string `json:"url"`
	Hot   string `json:"hot,omitempty"` // 热度值（可选）
}

// hotPlatforms 是 V2 平台清单与 RSSHub 路由（顺序即前端 Tab 顺序，GetAll 依此排序）。
// 准入线（TRENDS_PAGE_PLAN.html §5）：免 Cookie、≥20 条、<15s，且在生产
// RSSHub 上实测连续 3 次可用（2026-09-22 实测：新浪科技/36氪 3 连发全过）。
// 抖音/小红书需 Puppeteer 且反爬严格，生产内存不足以承载 Chromium，
// 未过准入线暂不上 —— 路由保留在注释里，准入后加回即可。
var hotPlatforms = []struct {
	name  string
	route string
}{
	{"微博", "/weibo/search/hot"},
	{"B站", "/bilibili/hot-search"},
	{"知乎", "/zhihu/hot"},
	{"新浪科技", "/sina/rollnews"},
	{"36氪", "/36kr/newsflashes"},
	// {"抖音", "/douyin/hot"},            // 未过准入线：严格反爬+Puppeteer
	// {"小红书", "/xiaohongshu/board/homefeed_recommend"}, // 未过准入线：路由不稳
	// {"IT之家", "/ithome/rank"},         // 实测 503：路由在但上游抓取失败
}

// Service aggregates hot topics from RSSHub with in-memory caching.
type Service struct {
	rsshubBase    string
	refreshPeriod time.Duration
	logger        *slog.Logger

	mu       sync.RWMutex
	cache    map[string]Platform // 值语义：锁外永远不共享可变内存
	stopCh   chan struct{}
	cancel   context.CancelFunc
	closeOne sync.Once
	wg       sync.WaitGroup
}

// NewService creates a trends service and starts the refresh ticker.
// rsshubBase should be "http://127.0.0.1:1200" for local deployment.
//
// cache 先预填全部平台的 error 空快照：GetAll 永远返回完整 Tab 清单
// （首屏立即可渲染「加载中/暂不可用」），首轮刷新在后台异步完成 ——
// NewService 同步路径零网络 IO，RSSHub 挂了不能拖慢 server 启动。
// logger 传 nil 时退回 slog.Default。
func NewService(rsshubBase string, refreshPeriod time.Duration, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		rsshubBase:    strings.TrimRight(rsshubBase, "/"),
		refreshPeriod: refreshPeriod,
		logger:        logger,
		cache:         make(map[string]Platform),
		stopCh:        make(chan struct{}),
		cancel:        cancel,
	}

	for _, p := range hotPlatforms {
		s.cache[p.name] = Platform{Name: p.name, Status: StateError, Error: "正在加载", UpdatedAt: time.Now()}
	}

	s.wg.Add(1)
	go s.refreshLoop(ctx)

	return s
}

// GetAll returns snapshots for all platforms in Tab order (value copies).
func (s *Service) GetAll(ctx context.Context) []Platform {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Platform, 0, len(hotPlatforms))
	for _, hp := range hotPlatforms {
		if p, ok := s.cache[hp.name]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Get returns a value copy of one platform's snapshot (测试与排障用).
func (s *Service) Get(name string) (Platform, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.cache[name]
	return p, ok
}

// Close stops the refresh loop. 幂等；最坏等待在飞刷新完成（单平台 10s 超时）。
func (s *Service) Close() {
	s.closeOne.Do(func() {
		close(s.stopCh)
		s.cancel()
	})
	s.wg.Wait()
}

func (s *Service) refreshLoop(ctx context.Context) {
	defer s.wg.Done()
	// 首轮立即刷，之后按周期
	s.refreshAll(ctx)

	ticker := time.NewTicker(s.refreshPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.refreshAll(ctx)
		case <-s.stopCh:
			return
		}
	}
}

func (s *Service) refreshAll(ctx context.Context) {
	for _, p := range hotPlatforms {
		snapshot := s.fetchPlatform(ctx, p.name, p.route)

		s.mu.Lock()
		old, had := s.cache[p.name]
		switch {
		case snapshot.Status == StateOK:
			s.cache[p.name] = *snapshot
		case had && len(old.Items) > 0:
			// 有 last-good 数据 → 保留并标记 stale（更新时间不变，前端显示真实新鲜度）
			old.Status = StateStale
			s.cache[p.name] = old
		default:
			// 从未成功过：落入新 error 快照，让用户/运维看到真实失败原因
			s.cache[p.name] = *snapshot
		}
		s.mu.Unlock()
	}
}

func (s *Service) fetchPlatform(ctx context.Context, name, route string) *Platform {
	// ?format=json：RSSHub 支持 JSON Feed 输出，避免在 Go 里解析 XML；
	// limit=20 直接在源头截断。
	url := s.rsshubBase + route + "?format=json&limit=20"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return s.errorSnapshot(name, err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return s.errorSnapshot(name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return s.errorSnapshot(name, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body)))
	}

	var feed jsonFeed
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return s.errorSnapshot(name, fmt.Errorf("JSON decode: %w", err))
	}

	if len(feed.Items) == 0 {
		// 上游 200 + 空列表：按失败处理，防止以「ok + 0 条」清掉 last-good
		return s.errorSnapshot(name, fmt.Errorf("上游返回空列表"))
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

// errorSnapshot 构造失败快照并记 warn（运维排障线索）。
func (s *Service) errorSnapshot(name string, err error) *Platform {
	s.logger.Warn("trends: 平台热榜抓取失败", slog.String("platform", name), slog.String("err", err.Error()))
	return &Platform{
		Name:      name,
		Status:    StateError,
		Error:     err.Error(),
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
