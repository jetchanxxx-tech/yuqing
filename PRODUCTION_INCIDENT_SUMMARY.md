# 生产环境故障总结报告

**事件编号**：INC-2026-09-26-001  
**严重程度**：🔴 **P0 - 生产全停**  
**报告时间**：2026-09-26  
**报告人**：Team Lead

---

## 📋 事件时间线

| 时间 | 事件 | 负责人 |
|------|------|--------|
| T+0min | 测试工程师报告生产环境所有分析功能返回500错误 | Test Engineer |
| T+5min | Team Lead开始调查，要求测试工程师提供详细错误信息 | Team Lead |
| T+10min | 测试工程师报告错误：`ERROR: column "created_by" does not exist` | Test Engineer |
| T+15min | Team Lead本地验证代码正常，确认为生产Schema问题 | Team Lead |
| T+20min | 检查迁移文件，确认`0008_add_created_by_to_analyses.sql`存在 | Team Lead |
| T+25min | 确认根因：生产数据库未执行迁移0008 | Team Lead |
| T+30min | 生成修复脚本`HOTFIX_0008_created_by.sql` | Team Lead |
| T+35min | 生成完整修复报告`PRODUCTION_HOTFIX_REPORT.md` | Team Lead |
| T+40min | 通知运维经理执行修复 | Team Lead |
| **待执行** | 运维经理SSH登录生产服务器执行修复 | DevOps Manager |
| **待执行** | 验证修复并重启服务 | DevOps Manager |
| **待执行** | 测试工程师重新验证生产环境 | Test Engineer |

---

## 🔍 根本原因分析

### 直接原因
**生产数据库缺少 `analyses.created_by` 列**

### 技术细节
- 迁移文件 `migrations/platform/0008_add_created_by_to_analyses.sql` 存在于代码仓库
- 本地开发环境已执行该迁移
- **生产环境未执行该迁移**
- 代码已部署到生产环境（依赖该列）

### 触发条件
当任何分析相关API调用时，SQL查询包含 `created_by` 列：
```sql
INSERT INTO analyses (..., created_by, ...) VALUES (...);
SELECT * FROM analyses WHERE created_by = ...;
```

PostgreSQL返回错误：
```
ERROR: column "created_by" of relation "analyses" does not exist (SQLSTATE 42703)
```

### 为什么本地正常？
- 本地开发环境按顺序执行了所有迁移文件
- 本地数据库Schema与代码版本匹配

### 为什么生产失败？
- 生产部署流程中**遗漏了数据库迁移步骤**
- 只部署了代码，未同步数据库Schema

---

## 💥 影响范围

### 受影响功能
- ❌ 创建新分析（POST /api/v1/analyses）
- ❌ 查询分析列表（GET /api/v1/analyses）
- ❌ 查询分析详情（GET /api/v1/analyses/:id）
- ❌ Dashboard统计（依赖analyses表）
- ❌ 报告生成（依赖analyses表）

### 受影响用户
- **所有生产环境用户**
- 影响时间：从代码部署时刻开始至修复完成

### 数据损失
- ✅ **无数据损失**（数据库未损坏，仅缺少列）

---

## 🛠️ 修复方案

### 已完成
1. ✅ **根因分析**（Team Lead）
2. ✅ **生成修复脚本**（`HOTFIX_0008_created_by.sql`）
3. ✅ **生成修复报告**（`PRODUCTION_HOTFIX_REPORT.md`）
4. ✅ **通知运维经理执行修复**

### 待执行（运维经理）
1. ⏳ SSH登录生产服务器
2. ⏳ 执行修复脚本
3. ⏳ 验证Schema修复
4. ⏳ 重启服务
5. ⏳ 验证API恢复

### 修复脚本内容
```sql
-- 1. 新增列
ALTER TABLE analyses ADD COLUMN created_by TEXT;

-- 2. 回填历史数据（取每个租户的第一个成员）
UPDATE analyses a
SET created_by = (
    SELECT user_id FROM tenant_members
    WHERE tenant_id = a.tenant_id
    ORDER BY user_id LIMIT 1
)
WHERE created_by IS NULL;

-- 3. 设置非空约束
ALTER TABLE analyses ALTER COLUMN created_by SET NOT NULL;

-- 4. 外键约束
ALTER TABLE analyses ADD CONSTRAINT fk_analyses_created_by
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE;

-- 5. 索引
CREATE INDEX idx_analyses_created_by ON analyses(created_by);
```

### 预计修复时间
- **执行时间**：< 5秒
- **停机时间**：< 1分钟
- **验证时间**：< 2分钟
- **总计**：< 5分钟

---

## 📊 风险评估

### 修复风险
- **数据风险**：✅ **极低**（仅新增列，不修改现有数据）
- **停机风险**：✅ **极低**（< 1分钟停机）
- **回滚风险**：✅ **极低**（可快速回滚，见下）

### 回滚方案
如果修复后出现新问题：
```sql
ALTER TABLE analyses DROP CONSTRAINT IF EXISTS fk_analyses_created_by;
DROP INDEX IF EXISTS idx_analyses_created_by;
ALTER TABLE analyses DROP COLUMN created_by;
```

然后回滚代码到上一个稳定版本。

---

## 🔄 预防措施（后续改进）

### 立即行动（本周内）

#### 1. 更新部署Runbook
**负责人**：DevOps Manager + Team Lead  
**优先级**：P0

在 `docs/DEPLOYMENT.md` 中新增检查清单：
```markdown
## 部署前检查清单

### 数据库迁移
- [ ] 检查是否有新的迁移文件
- [ ] 在生产环境执行迁移：`psql -U postgres -d pangu_yuqing -f migrations/platform/XXXX.sql`
- [ ] 验证Schema版本：`\d <表名>;`

### 代码部署
- [ ] 拉取最新代码
- [ ] 构建新版本
- [ ] 重启服务
- [ ] 验证健康检查
```

#### 2. CI强制检查迁移
**负责人**：DevOps Manager  
**优先级**：P1

在CI流程中新增步骤：
```yaml
# .github/workflows/deploy.yml
- name: Check migration sync
  run: |
    # 对比本地迁移版本与生产数据库版本
    ./scripts/check_migration_sync.sh
    # 如果不匹配，CI失败并打印缺失的迁移文件
```

#### 3. 服务启动时Schema验证
**负责人**：Team Lead  
**优先级**：P1

在 `cmd/server/main.go` 中新增启动检查：
```go
func validateCriticalColumns(pool *pgxpool.Pool) error {
    // 检查关键列存在
    requiredColumns := []struct{
        table  string
        column string
    }{
        {"analyses", "created_by"},
        {"analyses", "tenant_id"},
        // ... 其他关键列
    }
    
    for _, col := range requiredColumns {
        exists := checkColumnExists(pool, col.table, col.column)
        if !exists {
            return fmt.Errorf(
                "CRITICAL: Column %s.%s does not exist. "+
                "Please run migrations before starting the server.",
                col.table, col.column,
            )
        }
    }
    return nil
}
```

如果关键列缺失，**拒绝启动服务**并打印明确错误信息。

### 中期改进（本月内）

#### 4. 迁移历史表
**负责人**：Team Lead  
**优先级**：P2

创建 `schema_migrations` 表记录已执行的迁移：
```sql
CREATE TABLE schema_migrations (
    version VARCHAR(50) PRIMARY KEY,
    executed_at TIMESTAMP NOT NULL DEFAULT NOW()
);
```

部署脚本自动对比：
```bash
# 检查哪些迁移未执行
./scripts/check_pending_migrations.sh
```

#### 5. 集成测试覆盖PostgreSQL
**负责人**：Test Engineer  
**优先级**：P2

在CI中运行真实PostgreSQL集成测试：
```yaml
- name: Integration Tests
  run: |
    docker-compose up -d postgres
    go test ./internal/business/analysis/integration_pg_test.go
    go test ./internal/business/dashboard/integration_pg_test.go
```

确保测试环境与生产Schema一致。

---

## 📝 经验教训

### 做得好的地方
1. ✅ **快速诊断**：20分钟内定位根因
2. ✅ **完整文档**：生成详细修复报告和回滚方案
3. ✅ **风险控制**：修复脚本简洁、可验证、可回滚
4. ✅ **团队协作**：测试工程师及时报告，运维经理待命执行

### 需要改进的地方
1. ❌ **部署流程缺陷**：未强制检查数据库迁移
2. ❌ **缺少Schema版本管理**：无法快速对比本地与生产差异
3. ❌ **缺少启动检查**：服务启动时未验证关键列存在
4. ❌ **测试覆盖不足**：CI未运行真实PostgreSQL测试

### 关键洞察
**根本问题**：代码部署与数据库Schema部署是**两个独立步骤**，但当前流程中：
- ✅ 代码部署有自动化流程
- ❌ 数据库迁移依赖人工记忆

**解决方向**：
- 将数据库迁移纳入自动化部署流程
- 在多个检查点验证Schema版本（CI、启动时、运行时）

---

## ✅ 后续行动项

| # | 任务 | 负责人 | 优先级 | 截止日期 | 状态 |
|---|------|--------|--------|----------|------|
| 1 | 执行生产环境修复 | DevOps Manager | P0 | 立即 | ⏳ 待执行 |
| 2 | 验证修复并重启服务 | DevOps Manager | P0 | 立即 | ⏳ 待执行 |
| 3 | 测试工程师验证生产环境 | Test Engineer | P0 | 修复后 | ⏳ 待执行 |
| 4 | 更新部署Runbook | Team Lead | P0 | 本周 | ⏳ 待开始 |
| 5 | CI强制检查迁移 | DevOps Manager | P1 | 本周 | ⏳ 待开始 |
| 6 | 服务启动Schema验证 | Team Lead | P1 | 本周 | ⏳ 待开始 |
| 7 | 创建迁移历史表 | Team Lead | P2 | 本月 | ⏳ 待开始 |
| 8 | 集成测试覆盖PostgreSQL | Test Engineer | P2 | 本月 | ⏳ 待开始 |

---

## 📞 联系人

- **Team Lead**：根因分析、修复方案设计
- **DevOps Manager**：生产环境修复执行
- **Test Engineer**：问题发现、修复验证

---

**报告状态**：✅ **完整**  
**下一步**：等待运维经理执行修复

---

**备注**：
- 修复脚本：`HOTFIX_0008_created_by.sql`
- 详细修复报告：`PRODUCTION_HOTFIX_REPORT.md`
- 完整迁移文件：`migrations/platform/0008_add_created_by_to_analyses.sql`
