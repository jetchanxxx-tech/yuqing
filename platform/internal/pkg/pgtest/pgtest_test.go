package pgtest

import (
	"os"
	"strings"
	"testing"
)

// 未设置 YUGING_TEST_PG_URL 时（本地开发机、CI 无 PG）Pool 必须 t.Skip，
// 而不是连接失败 —— 默认 `go test ./...` 必须保持绿色。
func TestPool_skipsWithoutEnvVar(t *testing.T) {
	orig, had := os.LookupEnv(EnvURL)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(EnvURL, orig)
			return
		}
		_ = os.Unsetenv(EnvURL)
	})
	_ = os.Unsetenv(EnvURL)

	rec := &recordingT{}
	Pool(rec, "pgtest")
	if !rec.skipped {
		t.Error("Pool() without YUGING_TEST_PG_URL did not skip")
	}
}

func TestSchemaNameFor_sanitizesIdentifiers(t *testing.T) {
	tests := []struct {
		name     string
		pkg      string
		testName string
		want     string
	}{
		{"lowercases", "Auth", "TestX", "pgtest_auth_testx"},
		{"replaces path and slashes", "auth", "TestX/sub_case", "pgtest_auth_testx_sub_case"},
		{"replaces punctuation with underscores", "alert", "TestA-B.C", "pgtest_alert_testa_b_c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := schemaNameFor(tt.pkg, tt.testName); got != tt.want {
				t.Errorf("schemaNameFor(%q, %q) = %q, want %q", tt.pkg, tt.testName, got, tt.want)
			}
		})
	}
}

// PostgreSQL 标识符上限 63 字节：超长测试名必须被截断且不产生碰撞。
func TestSchemaNameFor_boundedLengthAndStable(t *testing.T) {
	long := strings.Repeat("VeryLongTestName_", 10)

	got := schemaNameFor("auth", long)
	if len(got) > maxIdentifierLen {
		t.Errorf("len(schemaNameFor()) = %d, want <= %d", len(got), maxIdentifierLen)
	}
	if again := schemaNameFor("auth", long); again != got {
		t.Errorf("schemaNameFor() is not deterministic: %q vs %q", got, again)
	}
	// 同名包 + 不同测试名不得撞车（截断后仍要能区分）。
	other := schemaNameFor("auth", long+"x")
	if other == got {
		t.Errorf("truncation collapsed two distinct test names into %q", got)
	}
}

func TestUpSection_keepsOnlyGooseUp(t *testing.T) {
	script := "-- +goose Up\nCREATE TABLE a (id TEXT);\n-- +goose Down\nDROP TABLE a;\n"
	got := upSection(script)

	if !strings.Contains(got, "CREATE TABLE a") {
		t.Errorf("Up section lost its statement: %q", got)
	}
	if strings.Contains(got, "DROP TABLE a") {
		t.Errorf("Up section leaked the Down statement: %q", got)
	}
}

func TestUpSection_withoutMarkersReturnsWholeScript(t *testing.T) {
	got := upSection("CREATE TABLE a (id TEXT);")
	if !strings.Contains(got, "CREATE TABLE a") {
		t.Errorf("marker-less script was mangled: %q", got)
	}
}

func TestSplitStatements_handlesCRLFCommentsAndBlank(t *testing.T) {
	script := "-- 说明行\r\n\r\nCREATE TABLE a (\r\n  id TEXT\r\n);\r\n-- 另一行说明\r\nCREATE INDEX i ON a(id);\r\n"

	got := splitStatements(script)
	if len(got) != 2 {
		t.Fatalf("splitStatements() = %d statements (%v), want 2", len(got), got)
	}
	if !strings.HasPrefix(got[0], "CREATE TABLE a") {
		t.Errorf("statement 0 = %q, want CREATE TABLE first", got[0])
	}
	if strings.Contains(got[0], "说明行") {
		t.Errorf("comment leaked into statement: %q", got[0])
	}
	if !strings.HasPrefix(got[1], "CREATE INDEX") {
		t.Errorf("statement 1 = %q, want CREATE INDEX", got[1])
	}
}

func TestSplitStatements_ignoresTrailingWhitespaceOnly(t *testing.T) {
	if got := splitStatements("   \n\n\t\n"); len(got) != 0 {
		t.Errorf("splitStatements(blank) = %v, want none", got)
	}
}

// recordingT 是 testing.TB 的最小替身：只记录是否调用了 Skip。
// 用真实 *testing.T 之外的类型可以避免 Skip 真的终止测试。
type recordingT struct {
	testing.TB
	skipped bool
}

func (r *recordingT) Skip(args ...any)                  { r.skipped = true }
func (r *recordingT) Skipf(format string, args ...any)  { r.skipped = true }
func (r *recordingT) Helper()                           {}
func (r *recordingT) Fatalf(format string, args ...any) { r.skipped = true }
func (r *recordingT) Name() string                      { return "recording" }
