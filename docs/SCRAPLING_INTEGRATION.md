# 盘古舆情 — Scrapling 数据采集融入设计

## Scrapling 概述

Scrapling (https://github.com/D4Vinci/Scrapling) 是 Python 自适应 Web Scraping 框架：
- **许可证**: BSD-3-Clause（商用友好，无 GPL 感染风险）
- **Python 要求**: 3.10+（我们引擎用 3.11 ✅）
- **社区**: 80.4k stars, 8.1k forks, 1619 commits
- **质量**: 92% test coverage, full type hints

## 核心能力 vs 我们需求

| Scrapling 能力 | 盘古舆情需求 | 匹配度 |
|---------------|-------------|--------|
| `Fetcher.get(url)` 静态 HTTP 抓取 | 新闻站点、公众号文章 | ✅ |
| `DynamicFetcher` (Playwright) | JavaScript 渲染页面(微博/小红书/B站) | ✅ |
| `StealthyFetcher` 反爬绕过 | 绕过 Cloudflare/验证码 | ✅ |
| `solve_cloudflare=True` | 处理 Turnstile 挑战 | ✅ |
| 自适应选择器 `adaptive=True` | 页面改版后自动重新定位元素 | ✅ |
| `auto_save=True` | 下次页面改版自动恢复 | ✅ |
| Spider API + 会话管理 | 多页面联动爬取 | ✅ |
| `robots_txt_obey=True` | 合规爬取 | ✅ |
| `AutoThrottle` | 避免被封 | ✅ |
| `ProxyRotator` | 分布式爬取 | 可选 |

## 融入架构

```
Go 平台 (不变)
  │
  ├── engine/contract.go         接口定义不变
  │   type CrawlerEngine interface { Crawl(ctx, req) error }
  │
  └── engine/fake.go             删除 → 替换为 HTTP transport

Python engines/ (新增真实实现)
  │
  ├── query_engine/main.py       搜索入口 → Bocha API + Scrapling
  │   └── /search 端点
  │       1. Bocha API 搜索关键词 → 获取 URL 列表
  │       2. Scrapling Fetcher/DynamicFetcher 抓取正文
  │       3. 去重 → 清洗 → 返回 Document 列表
  │
  └── common/
      ├── scraper.py        [新增] Scrapling 封装类
      │   class ScraperEngine:
      │     - fetch_static(url)   → Fetcher
      │     - fetch_dynamic(url)  → DynamicFetcher (headless=True)
      │     - fetch_stealth(url)   → StealthyFetcher(solve_cloudflare=True)
      │     - adaptive=True + auto_save=True（保存选择器状态）
      │
      └── requirements.txt   [更新]
           scrapling[fetchers]  # 新增

Go 侧 engine/httpx/client.go
  └── HTTP transport 调 Python /search 端点（原 FakeCrawlerEngine 替换）
```

## MVP 数据采集流程

```
用户创建分析 "雅阁后排"
  ↓
Go Analysis Service: state=queued → acquiring_budget → fetching
  ↓
HTTP POST http://query-engine:8000/search
  body: {keywords:["雅阁后排"], sources:["weibo","news","xiaohongshu","bilibili"]}
  ↓
Python query_engine:
  1. Bocha API search("雅阁后排") → URL 列表(去重)
  2. 对每个 URL:
     - news source → Scrapling Fetcher.get(url).css('article::text').getall()
     - weibo source → Scrapling DynamicFetcher(headless=True).fetch(url)
     - xiaohongshu → StealthyFetcher(solve_cloudflare=True).fetch(url)
  3. 提取: title, content, author, published_at, url
  4. content_hash = sha256(content)[:16] → 去重
  5. 返回 Document 列表
  ↓
Go 侧: 接收 JSON → 写入 raw_documents(内存 store) → 推进 analyzing 状态
```

## Bocha API 集成计划

Bocha AI (https://open.bochaai.com):
- 当前免费注册即用
- API: `POST https://api.bochaai.com/v1/ai/search`
- OpenAI 兼容格式
- 返回: title, url, snippet, date

集成在 Python `query_engine/main.py`:
```python
# 搜索阶段
async def search_bocha(keyword: str, api_key: str):
    resp = httpx.post(
        "https://api.bochaai.com/v1/ai/search",
        headers={"Authorization": f"Bearer {api_key}"},
        json={"query": keyword, "count": 20}
    )
    return [{"title": r["title"], "url": r["url"]} for r in resp.json()["webpages"]]
```

## 部署影响

- Python 引擎需要: `scrapling[fetchers]` + Playwright Chromium
- 首次部署: `scrapling install`（安装浏览器二进制）
- deploy.sh 更新: 加 Python 引擎真实部署步骤

## 对比：Scrapling vs 之前的所有方案

| 方案 | 开发量 | 反爬能力 | MVP 可用性 | 维护负担 |
|------|--------|---------|-----------|---------|
| 自研 Playwright | 高 (3000+ 行) | 需自建 | 慢 (6-8 周) | 高 |
| 纯 API 采购 | 低 | N/A | 快 | 按量付费 |
| 纯 Scrapling | **极低** | **内置** | **极快** (1-2 天) | **低 (80k 社区维护)** |
| **Bocha + Scrapling（推荐）** | 低 | 内置 | **最快** | **最低** |