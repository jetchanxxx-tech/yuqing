# 本地开发

完整的本地开发环境搭建指南，包含 Go 平台层、Python 引擎层、React 前端的开发配置。

## 🎯 前置要求

| 工具 | 版本要求 | 用途 |
|------|---------|------|
| **Go** | 1.25+ | 平台层编译 |
| **Python** | 3.11+ | 引擎层运行 |
| **Node.js** | 18+ | 前端构建 |
| **PostgreSQL** | 15+ | 数据持久化（可选，默认内存模式） |
| **Redis** | 7+ | 缓存（可选） |
| **Git** | 2.x | 版本控制 |

## 📥 克隆仓库

```bash
git clone https://github.com/jetchanxxx-tech/yuqing.git
cd yuqing
```

## 🔧 Go 平台层开发

### 1. 安装依赖

```bash
cd platform

# 下载 Go 模块
go mod download

# 验证
go version
# 应显示 go1.25 或更高
```

### 2. 运行测试

```bash
# 全部测试（353 用例 / 20 包）
make test

# 不带 race detector（更快）
go test ./... -count=1

# 单个包
go test ./internal/platform/auth/ -v

# 单个测试
go test -run TestRegister ./internal/platform/auth/

# 覆盖率
make test-cover
```

### 3. 配置文件（可选）

```bash
# 复制示例配置
cp config.example.yaml config.yaml

# 编辑配置
vim config.yaml
```

**内存模式**（默认，无需 PostgreSQL/Redis）：
```yaml
store:
  driver: memory

server:
  port: 8080
```

**PostgreSQL 模式**：
```yaml
store:
  driver: postgres

database:
  host: localhost
  port: 5432
  database: yuqing_platform
  user: yuqing
  password: your-password
```

### 4. 启动开发服务器

```bash
# 方式 1：直接运行
go run ./cmd/server

# 方式 2：编译后运行
go build -o bin/yuqing-server ./cmd/server
./bin/yuqing-server

# 方式 3：使用 air 热重载
go install github.com/cosmtrek/air@latest
air
```

**服务启动日志**：
```
INFO: server starting on :8080
INFO: store driver: memory
INFO: engine configured: query_engine at http://127.0.0.1:8000
INFO: engine configured: insight_engine at http://127.0.0.1:8002
INFO: pipeline started
```

### 5. 测试 API

```bash
# 健康检查
curl http://localhost:8080/api/v1/health
# {"status":"ok"}

# 注册账号
curl -X POST http://localhost:8080/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "email": "dev@example.com",
    "password": "Dev12345!",
    "name": "Developer"
  }'

# 获取 token 后测试创建分析
curl -X POST http://localhost:8080/api/v1/analyses \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "测试分析",
    "keywords": ["测试"],
    "sources": ["weibo"]
  }'
```

## 🐍 Python 引擎层开发

### 1. 创建虚拟环境

```bash
cd engines
python3 -m venv venv

# 激活（Linux/Mac）
source venv/bin/activate

# 激活（Windows）
venv\Scripts\activate
```

### 2. 安装依赖

```bash
# 安装 Python 包
pip install -r requirements.txt

# 安装 Scrapling Chromium（首次 ~150MB）
scrapling install --chromium

# 验证
python3 -c "import scrapling; print(scrapling.__version__)"
```

### 3. 配置环境变量

```bash
# 创建 .env 文件
cat > .env <<'EOF'
LLM_API_KEY=your-llm-key
LLM_BASE_URL=https://open.bigmodel.cn/api/paas/v4
LLM_MODEL=glm-5.3-flash
BOCHA_API_KEY=your-bocha-key
EOF

# 加载环境变量
export $(cat .env | xargs)
```

### 4. 运行测试

```bash
# 全部测试（54 用例）
python3 -m pytest tests/ -v

# 单个文件
python3 -m pytest tests/test_scraper.py -v

# 覆盖率
python3 -m pytest tests/ --cov=. --cov-report=html
```

### 5. 启动引擎服务

**开发模式**（在各引擎目录下）：

```bash
# query_engine (端口 8000)
cd query_engine
uvicorn main:app --port 8000 --reload

# insight_engine (端口 8002)
cd insight_engine
uvicorn main:app --port 8002 --reload

# report_engine (端口 8003)
cd report_engine
uvicorn main:app --port 8003 --reload
```

**生产模式**（从 engines 根目录）：
```bash
# 设置 PYTHONPATH
export PYTHONPATH=/path/to/yuqing/engines

# 启动
uvicorn engines.query_engine.main:app --port 8000
uvicorn engines.insight_engine.main:app --port 8002
uvicorn engines.report_engine.main:app --port 8003
```

### 6. 测试引擎端点

```bash
# query_engine /search
curl -X POST http://localhost:8000/search \
  -H "Content-Type: application/json" \
  -d '{
    "keywords": ["测试"],
    "sources": ["weibo"],
    "max_results": 10
  }'

# insight_engine /analyze
curl -X POST http://localhost:8002/analyze \
  -H "Content-Type: application/json" \
  -d '{
    "documents": [...],
    "mode": "full"
  }'
```

## ⚛️ React 前端开发

### 1. 安装依赖

```bash
cd web
npm ci

# 验证
npm list react
# 应显示 react@19.x.x
```

### 2. 开发服务器

```bash
# 启动 Vite dev server
npm run dev

# 访问
# http://localhost:5173
```

**自动代理配置**（vite.config.ts）：
```typescript
export default defineConfig({
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
});
```

前端请求 `/api/v1/xxx` 自动代理到 Go server。

### 3. 类型检查

```bash
# TypeScript 编译检查
npx tsc -b

# 增量编译（更快）
npx tsc -b --watch
```

### 4. Linting

```bash
# oxlint（快速）
npm run lint

# ESLint（完整）
npx eslint src/
```

### 5. 构建生产版本

```bash
npm run build
# → dist/

# 预览生产构建
npm run preview
```

### 6. 前端开发规范

**目录结构**：
```
web/src/
├── api/          # API 客户端（axios）
├── components/   # 通用组件
├── pages/        # 页面组件
├── stores/       # Zustand 状态管理
├── types/        # TypeScript 类型定义
└── utils/        # 工具函数
```

**API 调用**：
```typescript
// web/src/api/analyses.ts
import { apiClient } from './client';

export const createAnalysis = async (data: CreateAnalysisRequest) => {
  const response = await apiClient.post('/analyses', data);
  return response.data;
};

// 使用
import { createAnalysis } from '@/api/analyses';

const result = await createAnalysis({
  name: '测试',
  keywords: ['测试'],
  sources: ['weibo'],
});
```

**错误处理**：
```typescript
// web/src/api/client.ts
apiClient.interceptors.response.use(
  response => response,
  async error => {
    if (error.response?.status === 401) {
      // 自动刷新 token
      await refreshToken();
      return apiClient.request(error.config);
    }
    return Promise.reject(error);
  }
);
```

## 🧪 E2E 测试

### 1. 安装 Playwright

```bash
cd web
npm install -D @playwright/test

# 安装浏览器
npx playwright install
```

### 2. 运行测试

```bash
# 本地 dev server 冒烟测试
npx playwright test --config e2e/playwright.config.ts

# 打生产站点（需环境变量）
export E2E_EMAIL=test@example.com
export E2E_PASSWORD=Test123!
export E2E_BASE_URL=https://yuqing2.pangu-cloud.com

npx playwright test --config e2e/production.config.ts

# 调试模式
npx playwright test --debug

# 指定浏览器
npx playwright test --project=chromium
```

### 3. 编写测试

```typescript
// web/e2e/analyses.spec.ts
import { test, expect } from '@playwright/test';

test('创建分析任务', async ({ page }) => {
  // 登录
  await page.goto('/login');
  await page.fill('input[name="email"]', 'test@example.com');
  await page.fill('input[name="password"]', 'Test123!');
  await page.click('button[type="submit"]');
  
  // 新建分析
  await page.goto('/analyses/new');
  await page.fill('input[name="name"]', 'E2E 测试');
  await page.fill('textarea[name="keywords"]', '测试');
  await page.check('input[value="weibo"]');
  await page.click('button:has-text("提交")');
  
  // 验证跳转
  await expect(page).toHaveURL(/\/analyses\/\w+/);
  await expect(page.locator('text=E2E 测试')).toBeVisible();
});
```

## 🔧 开发工具推荐

### VS Code 扩展

**Go 开发**：
- Go (golang.go)
- Go Test Explorer
- REST Client（测试 API）

**Python 开发**：
- Python (ms-python.python)
- Pylance
- Ruff（Linting）

**前端开发**：
- ESLint
- Prettier
- Volar（Vue/React）
- TypeScript Vue Plugin

**通用**：
- GitLens
- Thunder Client（API 测试）
- Error Lens（错误提示）

### IDE 配置

**.vscode/settings.json**：
```json
{
  "go.lintTool": "golangci-lint",
  "go.testFlags": ["-v", "-count=1"],
  "python.linting.enabled": true,
  "python.linting.ruffEnabled": true,
  "editor.formatOnSave": true,
  "editor.codeActionsOnSave": {
    "source.organizeImports": true
  }
}
```

**.vscode/launch.json**（调试配置）：
```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "Go Server",
      "type": "go",
      "request": "launch",
      "mode": "auto",
      "program": "${workspaceFolder}/platform/cmd/server",
      "env": {
        "YUQING_CONFIG": "${workspaceFolder}/platform/config.yaml"
      }
    },
    {
      "name": "Python Engine",
      "type": "python",
      "request": "launch",
      "module": "uvicorn",
      "args": ["engines.query_engine.main:app", "--port", "8000"],
      "cwd": "${workspaceFolder}/engines"
    }
  ]
}
```

## 🐛 常见问题

### 1. Go 编译错误

**问题**：`package xxx is not in GOROOT`

**解决**：
```bash
go mod tidy
go mod download
```

### 2. Python 导入错误

**问题**：`ModuleNotFoundError: No module named 'engines'`

**解决**：
```bash
# 设置 PYTHONPATH
export PYTHONPATH=/path/to/yuqing/engines

# 或在 .env 文件
echo "PYTHONPATH=/path/to/yuqing/engines" >> .env
```

### 3. Scrapling Chromium 下载失败

**问题**：网络超时

**解决**：
```bash
# 使用国内镜像
export PLAYWRIGHT_DOWNLOAD_HOST=https://registry.npmmirror.com/-/binary/playwright
scrapling install --chromium
```

### 4. 前端 CORS 错误

**问题**：本地开发跨域

**解决**：Vite 已配置代理，确保 Go server 在 8080 端口运行。

### 5. 测试数据库污染

**问题**：PostgreSQL 模式下测试互相影响

**解决**：
```bash
# 每次测试前清空数据库
export YUQING_TEST_PG_URL=postgresql://yuqing:pass@localhost:5432/yuqing_test
go test ./... -count=1

# 或使用内存模式测试（推荐）
unset YUQING_TEST_PG_URL
go test ./... -count=1
```

### 6. LLM API 配额用尽

**问题**：测试频繁调用 LLM API

**解决**：
```bash
# 使用 FakeLLMProvider
# engines/tests/test_insight.py
llm_client = FakeLLM()  # 返回预定义响应，不消耗配额
```

## 📊 性能优化

### Go 层

```bash
# 性能分析
go test -bench=. -benchmem ./internal/business/analysis/

# CPU profile
go test -cpuprofile=cpu.prof -bench=. ./...
go tool pprof cpu.prof

# 内存 profile
go test -memprofile=mem.prof -bench=. ./...
go tool pprof mem.prof
```

### Python 层

```bash
# 性能分析
python3 -m cProfile -o profile.stats engines/query_engine/main.py

# 查看分析结果
python3 -m pstats profile.stats
```

### 前端

```bash
# Vite 构建分析
npm run build -- --mode analyze

# Lighthouse 审计
npx lighthouse http://localhost:5173 --view
```

## 🔄 Git 工作流

```bash
# 1. 创建功能分支
git checkout -b feature/user-profile

# 2. 开发并提交
git add .
git commit -m "feat: 添加用户个人资料页面"

# 3. 运行测试
make test          # Go
pytest tests/      # Python
npm run build      # 前端

# 4. 推送
git push origin feature/user-profile

# 5. 创建 PR
gh pr create --title "添加用户个人资料功能" --body "实现 F23 用户中心"
```

**提交消息规范**（Conventional Commits）：
- `feat:` 新功能
- `fix:` 修复 bug
- `docs:` 文档更新
- `test:` 测试相关
- `refactor:` 重构
- `chore:` 构建/工具链

## 📖 相关文档

- [[快速开始|Quick-Start]] - 5 分钟上手
- [[架构设计|Architecture]] - 理解代码结构
- [[测试指南|Testing-Guide]] - TDD 实践
- [[API 参考|API-Reference]] - REST API 文档
