// Package pgtest 是 pgx store 的 PostgreSQL 测试夹具。
//
// 全部 helper 以环境变量 YUGING_TEST_PG_URL 为闸门：未设置时 t.Skip，
// 因此本地开发机（无 PostgreSQL）与未配置数据库的 CI 上 `go test ./...`
// 依然全绿，真实行为验证在装了 PG 的服务器上跑。
//
// # 隔离模型
//
// 每个测试函数拿到一个独占 schema（名字由包名 + 测试名派生），
// 连接串的 search_path 指向它；测试开始前 DROP + CREATE，结束后 DROP。
// 这样：
//   - 同一个数据库可容纳多个包的测试并行跑（go test 默认跨包并行），不会互相清表；
//   - 单个测试函数的失败/中断不会污染其它测试；
//   - 测试用的表结构与生产迁移文件逐字一致（直接执行 migrations/*.sql，
//     而不是维护一份手写的建表 SQL）。
//
// 注意 search_path 里保留 public：citext 等扩展装在 public，
// 去掉它会让 `email CITEXT` 无法解析。
package pgtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvURL 指向一个**已存在**的 PostgreSQL 数据库（测试自建 schema，不建库）。
// 例：postgres://yuqing:secret@127.0.0.1:5432/yuqing_test?sslmode=disable
const EnvURL = "YUGING_TEST_PG_URL"

// 迁移目录名（platform/migrations 下的子目录）。
const (
	PlatformMigrations = "platform" // tenants/users/tenant_members/api_keys/usage_events/...
	TenantMigrations   = "tenant"   // 租户库模板：analyses/documents/alerts/...
)

// maxIdentifierLen 是 PostgreSQL 标识符上限（字节）。
const maxIdentifierLen = 63

// Pool 返回一个已连上 YUGING_TEST_PG_URL、且已跑完 migrations 的池。
//
// migrations 省略时只跑平台库迁移（PlatformMigrations）；
// 租户库表（alerts 等）传 TenantMigrations。
//
// 未设置 YUGING_TEST_PG_URL 时 t.Skip —— 调用方无需自己判断。
func Pool(t testing.TB, name string, migrations ...string) *pgxpool.Pool {
	t.Helper()

	url := strings.TrimSpace(os.Getenv(EnvURL))
	if url == "" {
		t.Skipf("跳过：需要真实 PostgreSQL（设置 %s 指向测试库，例如 postgres://user:pass@127.0.0.1:5432/yuqing_test?sslmode=disable）", EnvURL)
		// testing.T 的 Skipf 会终止本测试；替身实现不会，因此显式返回。
		return nil
	}
	if len(migrations) == 0 {
		migrations = []string{PlatformMigrations}
	}

	ctx := context.Background()
	schema := schemaNameFor(name, t.Name())

	// 1) 管理连接：准备 schema 与扩展。用默认 search_path，
	//    否则 schema 尚不存在时 CREATE TABLE 会落到 public。
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgtest: 连接 %s 失败: %v", EnvURL, err)
	}
	if _, err := admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
		admin.Close()
		t.Fatalf("pgtest: 清理 schema %s 失败: %v", schema, err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("pgtest: 创建 schema %s 失败: %v", schema, err)
	}
	ensureCitext(t, ctx, admin)
	admin.Close()

	// 2) 业务连接：search_path 指向独占 schema。
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("pgtest: 解析 %s 失败: %v", EnvURL, err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgtest: 建立连接池失败: %v", err)
	}

	for _, dir := range migrations {
		applyMigrations(t, ctx, pool, dir)
	}

	t.Cleanup(func() {
		// 先删 schema（连带 DROP 掉测试数据），再关池。
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
			t.Errorf("pgtest: 清理 schema %s 失败: %v", schema, err)
		}
		pool.Close()
	})
	return pool
}

// Truncate 清空若干张表并重置自增序列（同一个测试函数内想要干净起点时用；
// 每个测试函数本就有独立 schema，多数场景不需要）。
func Truncate(t testing.TB, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	stmt := "TRUNCATE TABLE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := pool.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("pgtest: TRUNCATE 失败: %v", err)
	}
}

// ensureCitext 保证 citext 扩展可用（users.email 是 CITEXT 列）。
// 扩展是库级对象、需要超级用户安装，因此单独处理并给出可操作的报错。
func ensureCitext(t testing.TB, ctx context.Context, admin *pgxpool.Pool) {
	t.Helper()

	// 已装则直接过；未装则尝试安装（失败不代表一定不可用，故不在此报错）。
	var installed bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citext')").Scan(&installed); err == nil && installed {
		return
	}
	_, _ = admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS citext")

	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citext')").Scan(&installed); err != nil {
		t.Fatalf("pgtest: 检查 citext 扩展失败: %v", err)
	}
	if !installed {
		t.Fatalf("pgtest: 测试库缺少 citext 扩展（users.email 是 CITEXT 列）。" +
			"请以超级用户执行：CREATE EXTENSION citext;")
	}
}

// applyMigrations 按文件名顺序执行 migrations/<dir>/*.sql 的 Up 段。
func applyMigrations(t testing.TB, ctx context.Context, pool *pgxpool.Pool, dir string) {
	t.Helper()

	root := migrationsRoot(t)
	files, err := filepath.Glob(filepath.Join(root, dir, "*.sql"))
	if err != nil {
		t.Fatalf("pgtest: 列举 %s 迁移失败: %v", dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("pgtest: %s 下没有迁移文件（root=%s）", dir, root)
	}
	sort.Strings(files)

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("pgtest: 读取迁移 %s 失败: %v", file, err)
		}
		for _, stmt := range splitStatements(upSection(string(raw))) {
			// 扩展是库级对象：ensureCitext 已在管理连接上处理过，
			// 这里重复执行只会报 "already exists"，忽略即可。
			if hasPrefixFold(stmt, "CREATE EXTENSION") {
				continue
			}
			if _, err := pool.Exec(ctx, stmt); err != nil {
				t.Fatalf("pgtest: 迁移 %s 执行失败: %v\nSQL: %s", filepath.Base(file), err, stmt)
			}
		}
	}
}

// migrationsRoot 从当前工作目录（go test 设为包目录）向上寻找 migrations 目录，
// 因此无论从仓库根还是从 platform/ 下跑测试都能定位到同一批迁移文件。
func migrationsRoot(t testing.TB) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("pgtest: 取工作目录失败: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidates := []string{
			filepath.Join(dir, "migrations"),
			filepath.Join(dir, "platform", "migrations"),
		}
		for _, candidate := range candidates {
			if st, err := os.Stat(filepath.Join(candidate, PlatformMigrations)); err == nil && st.IsDir() {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("pgtest: 从 %s 向上找不到 migrations/<platform|tenant> 目录", dir)
	return ""
}

// upSection 截取 goose 迁移脚本的 Up 段（无标记则返回全文）。
func upSection(script string) string {
	const (
		upMarker   = "-- +goose Up"
		downMarker = "-- +goose Down"
	)
	script = strings.ReplaceAll(script, "\r\n", "\n")

	if i := strings.Index(script, upMarker); i >= 0 {
		script = script[i+len(upMarker):]
	}
	if i := strings.Index(script, downMarker); i >= 0 {
		script = script[:i]
	}
	return script
}

// splitStatements 把脚本拆成一条条可单独 Exec 的语句，并丢掉纯注释行。
//
// 这些迁移文件里没有 dollar-quoted 函数体、也没有字符串字面量中的分号，
// 因此按 ";" 切分是安全的；出现上述写法时需要换成真正的 SQL 解析。
func splitStatements(script string) []string {
	var out []string
	for _, chunk := range strings.Split(script, ";") {
		var lines []string
		for _, line := range strings.Split(chunk, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			lines = append(lines, line)
		}
		stmt := strings.TrimSpace(strings.Join(lines, "\n"))
		if stmt == "" {
			continue
		}
		out = append(out, stmt)
	}
	return out
}

// schemaNameFor 由包名与测试名派生一个合法的、唯一的 schema 名。
// 超过 63 字节时截断并附哈希后缀，保证不同测试名不会撞到同一个 schema。
func schemaNameFor(name, testName string) string {
	s := sanitizeIdent("pgtest_" + name + "_" + testName)
	if len(s) <= maxIdentifierLen {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return s[:maxIdentifierLen-9] + "_" + hex.EncodeToString(sum[:4])
}

// sanitizeIdent 只保留 [a-z0-9_]，其余字符替换为下划线。
func sanitizeIdent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + ('a' - 'A'))
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
