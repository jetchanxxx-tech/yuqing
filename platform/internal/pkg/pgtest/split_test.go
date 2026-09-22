package pgtest

import (
	"os"
	"strings"
	"testing"
)

// 验证 splitStatements 对迁移 0007 的 PL/pgSQL $$ 块的处理：
// CREATE FUNCTION 必须作为一条完整语句（$$ 内部的分号不切分）。
func TestSplitStatements_dollarQuotedBlock(t *testing.T) {
	raw, err := os.ReadFile("../../../migrations/platform/0007_user_center_p0.sql")
	if err != nil {
		t.Skipf("迁移文件不可读（不在仓库环境）: %v", err)
	}
	up := upSection(string(raw))

	var funcStmt string
	for _, stmt := range splitStatements(up) {
		if strings.Contains(stmt, "CREATE OR REPLACE FUNCTION") {
			funcStmt = stmt
			break
		}
	}
	if funcStmt == "" {
		t.Fatal("未找到 CREATE FUNCTION 语句")
	}
	for _, want := range []string{"BEGIN", "RETURN NEW;", "END;", "$$ LANGUAGE plpgsql"} {
		if !strings.Contains(funcStmt, want) {
			t.Errorf("函数体语句缺少 %q——语句被截断：\n%s", want, funcStmt)
		}
	}
}
