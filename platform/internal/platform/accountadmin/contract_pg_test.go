package accountadmin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/api"
	"github.com/yuqing/platform/internal/api/v1"
	"github.com/yuqing/platform/internal/app"
	"github.com/yuqing/platform/internal/config"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/platform/auth"
)

const accountAdminJWTSecret = "account-admin-contract-cloud-secret-32-chars"

func init() { gin.SetMode(gin.TestMode) }

type pgAdminAccount struct {
	ID, TenantID, Email, Access, Refresh string
}

type pgAdminEnv struct {
	pool    *pgxpool.Pool
	cfg     *config.Config
	logger  *slog.Logger
	router  *gin.Engine
	deps    *v1.Services
	appName string
}

// The complete service graph uses the same disposable schema as the assertion
// pool. There are no mock handlers or store implementations in these tests.
func newPGAdminEnv(t *testing.T) *pgAdminEnv {
	t.Helper()
	pool := pgtest.Pool(t, "account_admin_http")
	dsn, err := url.Parse(os.Getenv(pgtest.EnvURL))
	if err != nil {
		t.Fatal(err)
	}
	appName := "accountadmin:" + t.Name()
	if len(appName) > 63 {
		appName = appName[:63]
	}
	params := dsn.Query()
	params.Set("search_path", pool.Config().ConnConfig.RuntimeParams["search_path"])
	params.Set("application_name", appName)
	dsn.RawQuery = params.Encode()
	// This current composition-root gate is restricted to an isolated test
	// schema. No test here creates a charged task or performs provider delivery.
	t.Setenv("YUQING_BETA_SKIP_CREDITS", "true")
	t.Setenv("YUQING_BOOTSTRAP_ADMIN_EMAIL", "")
	cfg := &config.Config{}
	cfg.Store.Driver = "postgres"
	cfg.Queue.Driver = "postgres"
	cfg.DB.Primary = dsn.String()
	cfg.Auth.JWTSecret = accountAdminJWTSecret
	cfg.Auth.AccessTTL = "15m"
	cfg.Auth.RefreshTTL = "720h"
	cfg.RateLimit.Enabled = false
	env := &pgAdminEnv{pool: pool, cfg: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), appName: appName}
	env.rebuild()
	t.Cleanup(func() { env.deps.PGPool.Close() })
	return env
}

func (e *pgAdminEnv) rebuild() {
	if e.deps != nil {
		e.deps.PGPool.Close()
	}
	e.deps = app.Build(e.cfg, e.logger)
	e.router = api.NewRouter(e.cfg, e.logger, e.deps)
}

func pgAdminRequest(router *gin.Engine, method, path, token, requestID string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func pgAdminBody(t *testing.T, w *httptest.ResponseRecorder, wantStatus int) map[string]any {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("HTTP status = %d, want %d; body: %s", w.Code, wantStatus, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("HTTP response is not a JSON object: %v; body: %s", err, w.Body.String())
	}
	if wantStatus >= 400 {
		for _, key := range []string{"code", "message", "request_id"} {
			if value, ok := body[key].(string); !ok || value == "" {
				t.Errorf("error envelope %s is missing: %v", key, body)
			}
		}
	}
	return body
}

func (e *pgAdminEnv) register(t *testing.T, email string, platformAdmin bool) pgAdminAccount {
	t.Helper()
	body := pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, "/api/v1/auth/register", "", "", map[string]any{
		"email": email, "password": "password-123456", "name": "隔离测试账号",
	}), http.StatusCreated)
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatal("registration has no user object")
	}
	account := pgAdminAccount{Email: email}
	account.ID, _ = user["user_id"].(string)
	account.TenantID, _ = user["tenant_id"].(string)
	account.Access, _ = body["access_token"].(string)
	account.Refresh, _ = body["refresh_token"].(string)
	if account.ID == "" || account.TenantID == "" || account.Access == "" || account.Refresh == "" {
		t.Fatalf("registration identity or token pair missing: %v", body)
	}
	if platformAdmin {
		// PostgreSQL public registration never grants a platform role. The
		// fixture emulates explicit provisioning by verified immutable user ID.
		e.exec(t, `INSERT INTO platform_user_roles(user_id,role) VALUES($1,'platform_admin')`, account.ID)
	}
	return account
}

func (e *pgAdminEnv) login(t *testing.T, account pgAdminAccount) pgAdminAccount {
	t.Helper()
	body := pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, "/api/v1/auth/login", "", "", map[string]any{
		"email": account.Email, "password": "password-123456",
	}), http.StatusOK)
	account.Access, _ = body["access_token"].(string)
	account.Refresh, _ = body["refresh_token"].(string)
	if account.Access == "" || account.Refresh == "" {
		t.Fatal("login has no token pair")
	}
	return account
}

func (e *pgAdminEnv) revoked(t *testing.T, account pgAdminAccount) {
	t.Helper()
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/auth/me", account.Access, "", nil), http.StatusUnauthorized)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, "/api/v1/auth/refresh", "", "", map[string]any{
		"refresh_token": account.Refresh,
	}), http.StatusUnauthorized)
}

func (e *pgAdminEnv) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("prepare isolated fixture: %v", err)
	}
}

func TestAccountAdminPGUserStatusCASRevocationAndAuditPersists(t *testing.T) {
	e := newPGAdminEnv(t)
	admin := e.register(t, "admin-status@example.com", true)
	target := e.register(t, "target-status@example.com", false)
	path := "/api/v1/admin/users/" + target.ID
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, path+"/disable", admin.Access, "status-disable-request", map[string]any{
		"reason": "客服核验停用", "expected_version": 0,
	}), http.StatusOK)
	var status, tenantStatus string
	var tokenVersion, rowVersion int64
	if err := e.pool.QueryRow(context.Background(), `SELECT u.status,u.token_version,u.row_version,t.status
		FROM users u JOIN tenant_members m ON m.user_id=u.id JOIN tenants t ON t.id=m.tenant_id WHERE u.id=$1`, target.ID).
		Scan(&status, &tokenVersion, &rowVersion, &tenantStatus); err != nil {
		t.Fatal(err)
	}
	if status != "disabled" || tokenVersion != 1 || rowVersion != 1 || tenantStatus != "active" {
		t.Fatalf("disabled account = %s/%d/%d, tenant=%s", status, tokenVersion, rowVersion, tenantStatus)
	}
	e.revoked(t, target)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, "/api/v1/auth/login", "", "", map[string]any{
		"email": target.Email, "password": "password-123456",
	}), http.StatusUnauthorized)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, path+"/enable", admin.Access, "status-stale-request", map[string]any{
		"reason": "旧版本误恢复", "expected_version": 0,
	}), http.StatusConflict)
	var actor, action, requestID, reason, beforeStatus, afterStatus string
	if err := e.pool.QueryRow(context.Background(), `SELECT actor_id,action,details_json->>'request_id',details_json->>'reason',
		details_json->'before'->>'status',details_json->'after'->>'status' FROM audit_logs ORDER BY id`).
		Scan(&actor, &action, &requestID, &reason, &beforeStatus, &afterStatus); err != nil {
		t.Fatal(err)
	}
	if actor != admin.ID || action != "user.disable" || requestID != "status-disable-request" || reason != "客服核验停用" || beforeStatus != "active" || afterStatus != "disabled" {
		t.Errorf("persistent audit = %q/%q/%q/%q/%q/%q", actor, action, requestID, reason, beforeStatus, afterStatus)
	}
	e.rebuild()
	detail := pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, path, admin.Access, "", nil), http.StatusOK)
	if detail["status"] != "disabled" || detail["row_version"] != float64(1) {
		t.Errorf("reconstructed service lost status/version: %v", detail)
	}
	if audits, ok := detail["audit_logs"].([]any); !ok || len(audits) != 1 {
		t.Errorf("reconstructed service lost audit or recorded stale write: %v", detail["audit_logs"])
	}
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, path+"/enable", admin.Access, "status-enable-request", map[string]any{
		"reason": "客服核验恢复", "expected_version": 1,
	}), http.StatusOK)
	e.revoked(t, target)
	fresh := e.login(t, target)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/auth/me", fresh.Access, "", nil), http.StatusOK)
}

func TestAccountAdminPGPlatformRoleRevocationKeepsMembershipAndCannotBootstrapAgain(t *testing.T) {
	e := newPGAdminEnv(t)
	admin := e.register(t, "admin-role@example.com", true)
	target := e.register(t, "target-role@example.com", false)
	path := "/api/v1/admin/users/" + target.ID + "/platform-role"
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPut, path, admin.Access, "role-grant", map[string]any{
		"platform_admin": true, "reason": "运营角色交接", "expected_version": 0,
	}), http.StatusOK)
	e.revoked(t, target)
	fresh := e.login(t, target)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users", fresh.Access, "", nil), http.StatusOK)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPut, path, admin.Access, "role-revoke", map[string]any{
		"platform_admin": false, "reason": "运营职责结束", "expected_version": 1,
	}), http.StatusOK)
	e.revoked(t, fresh)
	e.rebuild()
	e.deps.Auth.SetBootstrapAdminEmail(target.Email)
	fresh = e.login(t, target)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users", fresh.Access, "", nil), http.StatusForbidden)
	var role string
	var memberVersion, userVersion, tokenVersion int64
	var platformRoles, audits int
	if err := e.pool.QueryRow(context.Background(), `SELECT m.role,m.row_version,u.row_version,u.token_version,
		(SELECT COUNT(*) FROM platform_user_roles WHERE user_id=u.id),
		(SELECT COUNT(*) FROM audit_logs WHERE action='user.platform_role')
		FROM tenant_members m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=$1 AND m.user_id=$2`, target.TenantID, target.ID).
		Scan(&role, &memberVersion, &userVersion, &tokenVersion, &platformRoles, &audits); err != nil {
		t.Fatal(err)
	}
	if role != "tenant_admin" || memberVersion != 0 || userVersion != 2 || tokenVersion != 2 || platformRoles != 0 || audits != 2 {
		t.Errorf("platform/member independence or revocation lost: %s/%d/%d/%d roles=%d audits=%d", role, memberVersion, userVersion, tokenVersion, platformRoles, audits)
	}
}

// Holding the shared production advisory lock lets both HTTP requests finish
// authentication against the original roles before either mutation commits.
// This makes the concurrent last-admin assertion independent of request timing.
func (e *pgAdminEnv) holdPlatformLock(t *testing.T) func() {
	t.Helper()
	conn, err := e.pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, auth.PlatformAdminLockID); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, auth.PlatformAdminLockID); err != nil {
			t.Error(err)
		}
		conn.Release()
	}
	t.Cleanup(release)
	return release
}

func (e *pgAdminEnv) waitPlatformLockContenders(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := e.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid
			WHERE l.locktype='advisory' AND l.objid::bigint=$1 AND NOT l.granted AND a.application_name=$2`, auth.PlatformAdminLockID, e.appName).
			Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("two authorized mutations did not reach the shared platform-admin transaction lock")
}

func TestAccountAdminPGConcurrentLastPlatformAdminProtection(t *testing.T) {
	for _, operation := range []string{"revoke", "disable", "mixed"} {
		t.Run(operation, func(t *testing.T) {
			e := newPGAdminEnv(t)
			first := e.register(t, "first-admin@example.com", true)
			second := e.register(t, "second-admin@example.com", false)
			e.exec(t, `INSERT INTO platform_user_roles(user_id,role,granted_by) VALUES($1,'platform_admin',$2)`, second.ID, first.ID)
			pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users", first.Access, "", nil), http.StatusOK)
			unlock := e.holdPlatformLock(t)
			results := make(chan *httptest.ResponseRecorder, 2)
			start := make(chan struct{})
			for i, actor := range []pgAdminAccount{first, second} {
				target := second
				if i == 1 {
					target = first
				}
				method, suffix := http.MethodPut, "/platform-role"
				body := map[string]any{"platform_admin": false, "reason": "并发管理员职责撤销", "expected_version": 0}
				if operation == "disable" || operation == "mixed" && i == 0 {
					method, suffix = http.MethodPost, "/disable"
					body = map[string]any{"reason": "并发管理员停用", "expected_version": 0}
				}
				go func(actor, target pgAdminAccount, method, suffix string, body map[string]any) {
					<-start
					results <- pgAdminRequest(e.router, method, "/api/v1/admin/users/"+target.ID+suffix, actor.Access, "concurrent-platform-admin", body)
				}(actor, target, method, suffix, body)
			}
			close(start)
			e.waitPlatformLockContenders(t, 2)
			unlock()
			statuses := map[int]int{}
			for i := 0; i < 2; i++ {
				select {
				case response := <-results:
					statuses[response.Code]++
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent administrator mutation did not finish")
				}
			}
			if statuses[http.StatusOK] != 1 || statuses[http.StatusConflict] != 1 {
				t.Fatalf("concurrent last-admin statuses = %v, want one 200 and one 409", statuses)
			}
			var activeAdmins, audits int
			var tokenVersions, rowVersions int64
			if err := e.pool.QueryRow(context.Background(), `SELECT
				(SELECT COUNT(*) FROM users u JOIN platform_user_roles r ON r.user_id=u.id WHERE u.status='active' AND r.role='platform_admin'),
				(SELECT COUNT(*) FROM audit_logs), (SELECT SUM(token_version) FROM users), (SELECT SUM(row_version) FROM users)`).
				Scan(&activeAdmins, &audits, &tokenVersions, &rowVersions); err != nil {
				t.Fatal(err)
			}
			if activeAdmins != 1 || audits != 1 || tokenVersions != 1 || rowVersions != 1 {
				t.Errorf("last-admin transaction: active=%d audits=%d token=%d row=%d", activeAdmins, audits, tokenVersions, rowVersions)
			}
		})
	}
}

func TestAccountAdminPGConcurrentLastTenantAdminProtection(t *testing.T) {
	e := newPGAdminEnv(t)
	admin := e.register(t, "platform-member-admin@example.com", true)
	first := e.register(t, "first-member-admin@example.com", false)
	second := e.register(t, "second-member-admin@example.com", false)
	e.exec(t, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,$2,'tenant_admin')`, first.TenantID, second.ID)
	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for _, member := range []pgAdminAccount{first, second} {
		go func(member pgAdminAccount) {
			<-start
			results <- pgAdminRequest(e.router, http.MethodPut, "/api/v1/admin/tenants/"+first.TenantID+"/members/"+member.ID+"/role", admin.Access, "concurrent-tenant-admin", map[string]any{
				"role": "viewer", "reason": "团队职责并发调整", "expected_version": 0,
			})
		}(member)
	}
	close(start)
	statuses := map[int]int{}
	for i := 0; i < 2; i++ {
		select {
		case response := <-results:
			statuses[response.Code]++
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent tenant role mutation did not finish")
		}
	}
	if statuses[http.StatusOK] != 1 || statuses[http.StatusConflict] != 1 {
		t.Fatalf("concurrent last-tenant-admin statuses = %v, want one 200 and one 409", statuses)
	}
	var tenantAdmins, viewers, audits int
	var memberVersions, tokenVersions int64
	if err := e.pool.QueryRow(context.Background(), `SELECT COUNT(*) FILTER(WHERE role='tenant_admin'),COUNT(*) FILTER(WHERE role='viewer'),SUM(row_version),
		(SELECT SUM(token_version) FROM users WHERE id IN ($2,$3)),(SELECT COUNT(*) FROM audit_logs WHERE action='member.role')
		FROM tenant_members WHERE tenant_id=$1`, first.TenantID, first.ID, second.ID).
		Scan(&tenantAdmins, &viewers, &memberVersions, &tokenVersions, &audits); err != nil {
		t.Fatal(err)
	}
	if tenantAdmins != 1 || viewers != 1 || memberVersions != 1 || tokenVersions != 1 || audits != 1 {
		t.Errorf("last-tenant-admin result: admins=%d viewers=%d memberVersion=%d tokenVersion=%d audits=%d", tenantAdmins, viewers, memberVersions, tokenVersions, audits)
	}
	var otherRole string
	var otherVersion int64
	if err := e.pool.QueryRow(context.Background(), `SELECT role,row_version FROM tenant_members WHERE tenant_id=$1 AND user_id=$2`, second.TenantID, second.ID).
		Scan(&otherRole, &otherVersion); err != nil {
		t.Fatal(err)
	}
	if otherRole != "tenant_admin" || otherVersion != 0 {
		t.Errorf("concurrent mutation changed another team: %s/%d", otherRole, otherVersion)
	}
}

func TestAccountAdminPGMemberRoleScopeCASRevocationAndAudit(t *testing.T) {
	e := newPGAdminEnv(t)
	admin := e.register(t, "admin-member-scope@example.com", true)
	target := e.register(t, "target-member-scope@example.com", false)
	e.exec(t, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,$2,'analyst')`, admin.TenantID, target.ID)
	path := "/api/v1/admin/tenants/" + admin.TenantID + "/members/" + target.ID + "/role"
	for _, role := range []string{"platform_admin", "api_service", "owner"} {
		pgAdminBody(t, pgAdminRequest(e.router, http.MethodPut, path, admin.Access, "invalid-member-role", map[string]any{
			"role": role, "reason": "不允许角色提升", "expected_version": 0,
		}), http.StatusBadRequest)
	}
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPut, "/api/v1/admin/tenants/"+target.TenantID+"/members/"+admin.ID+"/role", admin.Access, "missing-member", map[string]any{
		"role": "viewer", "reason": "不允许隐式加入团队", "expected_version": 0,
	}), http.StatusNotFound)
	var missingMembers int
	if err := e.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM tenant_members WHERE tenant_id=$1 AND user_id=$2`, target.TenantID, admin.ID).Scan(&missingMembers); err != nil {
		t.Fatal(err)
	}
	if missingMembers != 0 {
		t.Fatal("missing membership was silently created")
	}
	body := pgAdminBody(t, pgAdminRequest(e.router, http.MethodPut, path, admin.Access, "member-role-request", map[string]any{
		"role": "viewer", "reason": "调整为只读成员", "expected_version": 0,
	}), http.StatusOK)
	if body["tenant_id"] != admin.TenantID || body["user_id"] != target.ID || body["role"] != "viewer" || body["row_version"] != float64(1) {
		t.Errorf("member response = %v", body)
	}
	e.revoked(t, target)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPut, path, admin.Access, "member-stale-request", map[string]any{
		"role": "analyst", "reason": "重放旧成员版本", "expected_version": 0,
	}), http.StatusConflict)
	var memberRole, otherRole string
	var memberVersion, userVersion, tokenVersion int64
	if err := e.pool.QueryRow(context.Background(), `SELECT m.role,m.row_version,u.row_version,u.token_version,
		(SELECT role FROM tenant_members WHERE tenant_id=$3 AND user_id=$2)
		FROM tenant_members m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=$1 AND m.user_id=$2`, admin.TenantID, target.ID, target.TenantID).
		Scan(&memberRole, &memberVersion, &userVersion, &tokenVersion, &otherRole); err != nil {
		t.Fatal(err)
	}
	if memberRole != "viewer" || memberVersion != 1 || userVersion != 1 || tokenVersion != 1 || otherRole != "tenant_admin" {
		t.Errorf("member transaction changed wrong state: %s/%d user=%d token=%d other=%s", memberRole, memberVersion, userVersion, tokenVersion, otherRole)
	}
	var tenantID, actorID, beforeRole, afterRole, requestID string
	var audits int
	if err := e.pool.QueryRow(context.Background(), `SELECT tenant_id,actor_id,details_json->'before'->>'role',details_json->'after'->>'role',
		details_json->>'request_id',(SELECT COUNT(*) FROM audit_logs) FROM audit_logs WHERE action='member.role'`).
		Scan(&tenantID, &actorID, &beforeRole, &afterRole, &requestID, &audits); err != nil {
		t.Fatal(err)
	}
	if tenantID != admin.TenantID || actorID != admin.ID || beforeRole != "analyst" || afterRole != "viewer" || requestID != "member-role-request" || audits != 1 {
		t.Errorf("member audit scope = %s/%s %s→%s request=%s audits=%d", tenantID, actorID, beforeRole, afterRole, requestID, audits)
	}
	pair, err := auth.GenerateTokenPair(auth.Principal{UserID: target.ID, TenantID: admin.TenantID, TokenVersion: tokenVersion}, accountAdminJWTSecret, "15m", "720h")
	if err != nil {
		t.Fatal(err)
	}
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/analyses", pair.AccessToken, "", nil), http.StatusOK)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, "/api/v1/analyses", pair.AccessToken, "", map[string]any{"topic": "只读角色不得写入"}), http.StatusForbidden)
	e.rebuild()
	detail := pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users/"+target.ID, admin.Access, "", nil), http.StatusOK)
	if detail["tenant_count"] != float64(2) {
		t.Errorf("reconstructed user detail must count every membership: %v", detail)
	}
	if members, ok := detail["memberships"].([]any); !ok || len(members) != 2 {
		t.Errorf("reconstructed detail lost multi-tenant membership: %v", detail)
	}
	if logs, ok := detail["audit_logs"].([]any); !ok || len(logs) != 1 {
		t.Errorf("reconstructed member audit = %v", detail["audit_logs"])
	}
}

func TestAccountAdminPGAuditFailureRollsBackMutationAndVersions(t *testing.T) {
	for _, operation := range []string{"user", "member", "tenant"} {
		t.Run(operation, func(t *testing.T) {
			e := newPGAdminEnv(t)
			admin := e.register(t, "admin-audit-failure@example.com", true)
			target := e.register(t, "target-audit-failure@example.com", false)
			e.exec(t, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,$2,'analyst')`, admin.TenantID, target.ID)
			// Fail the actual persistence boundary, after validation, so a
			// separately committed role/status or version update is observable.
			e.exec(t, `ALTER TABLE audit_logs ADD CONSTRAINT reject_admin_audit CHECK (action NOT IN ('user.disable','member.role','tenant.suspend'))`)
			method, path := http.MethodPost, "/api/v1/admin/users/"+target.ID+"/disable"
			body := map[string]any{"reason": "审计写入必须同时提交", "expected_version": 0}
			if operation == "member" {
				method, path = http.MethodPut, "/api/v1/admin/tenants/"+admin.TenantID+"/members/"+target.ID+"/role"
				body["role"] = "viewer"
			} else if operation == "tenant" {
				path = "/api/v1/admin/tenants/" + target.TenantID + "/suspend"
			}
			response := pgAdminRequest(e.router, method, path, admin.Access, "audit-must-rollback", body)
			pgAdminBody(t, response, http.StatusInternalServerError)
			if strings.Contains(response.Body.String(), "reject_admin_audit") || strings.Contains(response.Body.String(), "SQLSTATE") {
				t.Error("failed audit leaked database details")
			}
			var userStatus, role, tenantStatus string
			var tokenVersion, userVersion, memberVersion, tenantVersion int64
			var audits int
			if err := e.pool.QueryRow(context.Background(), `SELECT u.status,u.token_version,u.row_version,m.role,m.row_version,t.status,t.row_version,
				(SELECT COUNT(*) FROM audit_logs) FROM users u JOIN tenant_members m ON m.user_id=u.id AND m.tenant_id=$2
				JOIN tenants t ON t.id=$3 WHERE u.id=$1`, target.ID, admin.TenantID, target.TenantID).
				Scan(&userStatus, &tokenVersion, &userVersion, &role, &memberVersion, &tenantStatus, &tenantVersion, &audits); err != nil {
				t.Fatal(err)
			}
			if userStatus != "active" || role != "analyst" || tenantStatus != "active" || tokenVersion != 0 || userVersion != 0 || memberVersion != 0 || tenantVersion != 0 || audits != 0 {
				t.Errorf("audit failure did not roll back: user=%s/%d/%d member=%s/%d tenant=%s/%d audits=%d", userStatus, tokenVersion, userVersion, role, memberVersion, tenantStatus, tenantVersion, audits)
			}
			pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/auth/me", target.Access, "", nil), http.StatusOK)
		})
	}
}

func TestAccountAdminPGTenantPaginationCountsPlanAndStatusCAS(t *testing.T) {
	e := newPGAdminEnv(t)
	admin := e.register(t, "admin-tenant-list@example.com", true)
	member := e.register(t, "member-tenant-list@example.com", false)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tenantID := range []string{"t_page_c", "t_page_a", "t_page_b"} {
		e.exec(t, `INSERT INTO tenants(id,name,slug,db_name,status,plan_code,created_at) VALUES($1,'Page tenant',$1,$1,'active','free',$2)`, tenantID, created)
	}
	e.exec(t, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('t_page_a',$1,'tenant_admin'),('t_page_a',$2,'viewer')`, admin.ID, member.ID)
	e.exec(t, `INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('t_page_a',17,'pro')`)
	e.exec(t, `INSERT INTO orders(id,tenant_id,sku_code,kind,credits,amount_cents,channel,state,expires_at)
		VALUES('o_real_pending','t_page_a','pro','plan',17,100,'alipay','pending',now()+interval '1 hour')`)
	for page, expectedID := range []string{"t_page_a", "t_page_b", "t_page_c"} {
		path := "/api/v1/admin/tenants?q=Page&page=" + strconv.Itoa(page+1) + "&page_size=1"
		body := pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, path, admin.Access, "", nil), http.StatusOK)
		items, ok := body["items"].([]any)
		if !ok || len(items) != 1 || body["total"] != float64(3) || body["page"] != float64(page+1) || body["page_size"] != float64(1) {
			t.Fatalf("tenant pagination = %v", body)
		}
		if !reflect.DeepEqual(items, body["tenants"]) {
			t.Errorf("legacy tenants does not match page: %v", body)
		}
		row := items[0].(map[string]any)
		if row["id"] != expectedID || row["row_version"] != float64(0) {
			t.Errorf("equal-time page %d = %v, want %s", page+1, row, expectedID)
		}
		createdAt, ok := row["created_at"].(string)
		if !ok {
			t.Fatalf("tenant created_at must be a string: %v", row)
		}
		parsed, err := time.Parse(time.RFC3339, createdAt)
		if err != nil || !parsed.Equal(created) {
			t.Errorf("tenant created_at must retain database instant: %q (%v)", createdAt, err)
		}
		if expectedID == "t_page_a" && (row["user_count"] != float64(2) || row["effective_plan_code"] != "pro" || row["plan_source"] != "report_credits") {
			t.Errorf("tenant must show real member count and credit authority: %v", row)
		}
		if expectedID != "t_page_a" && (row["user_count"] != float64(0) || row["effective_plan_code"] != "" || row["plan_source"] != "unknown") {
			t.Errorf("missing credit plan must be explicit and match report restrictions: %v", row)
		}
	}
	detail := pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/tenants/t_page_a", admin.Access, "", nil), http.StatusOK)
	if members, ok := detail["members"].([]any); !ok || len(members) != 2 {
		t.Errorf("tenant detail members = %v", detail)
	}
	if credit, ok := detail["credit"].(map[string]any); !ok || credit["balance"] != float64(17) || credit["plan_code"] != "pro" {
		t.Errorf("tenant detail credit = %v", detail)
	}
	orders, ok := detail["orders"].([]any)
	if !ok || len(orders) != 1 || orders[0].(map[string]any)["id"] != "o_real_pending" || orders[0].(map[string]any)["state"] != "pending" {
		t.Errorf("tenant detail must return actual unmodified orders: %v", detail["orders"])
	}
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, "/api/v1/admin/tenants/t_page_a/suspend", admin.Access, "tenant-suspend", map[string]any{
		"reason": "租户风险控制", "expected_version": 0,
	}), http.StatusOK)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, "/api/v1/admin/tenants/t_page_a/resume", admin.Access, "tenant-stale-resume", map[string]any{
		"reason": "旧版本恢复", "expected_version": 0,
	}), http.StatusConflict)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPost, "/api/v1/admin/tenants/t_page_a/resume", admin.Access, "tenant-resume", map[string]any{
		"reason": "租户风险复核", "expected_version": 1,
	}), http.StatusOK)
	var status string
	var version, userVersions int64
	var members, activeUsers, audits int
	if err := e.pool.QueryRow(context.Background(), `SELECT status,row_version,(SELECT SUM(row_version) FROM users),
		(SELECT COUNT(*) FROM tenant_members WHERE tenant_id='t_page_a'),(SELECT COUNT(*) FROM users WHERE status='active'),
		(SELECT COUNT(*) FROM audit_logs) FROM tenants WHERE id='t_page_a'`).
		Scan(&status, &version, &userVersions, &members, &activeUsers, &audits); err != nil {
		t.Fatal(err)
	}
	if status != "active" || version != 2 || userVersions != 0 || members != 2 || activeUsers != 2 || audits != 2 {
		t.Errorf("tenant CAS changed accounts/members or audited conflict: status=%s version=%d userVersions=%d members=%d activeUsers=%d audits=%d", status, version, userVersions, members, activeUsers, audits)
	}
	e.rebuild()
	detail = pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/tenants/t_page_a", admin.Access, "", nil), http.StatusOK)
	if detail["row_version"] != float64(2) || detail["status"] != "active" {
		t.Errorf("reconstructed tenant status = %v", detail)
	}
}

func TestAccountAdminPGUsersPaginationPrivacyAndReadFilters(t *testing.T) {
	e := newPGAdminEnv(t)
	admin := e.register(t, "admin-user-list@example.com", true)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, userID := range []string{"u_page_c", "u_page_a", "u_page_b"} {
		phone := map[string]string{"u_page_a": "13800000011", "u_page_b": "13800000012", "u_page_c": "13800000013"}[userID]
		e.exec(t, `INSERT INTO users(id,email,password_hash,name,status,phone,created_at,email_verified_at)
			VALUES($1,$2,'private-password-hash','Page account','active',$4,$3,$3)`, userID, userID+"@example.com", created, phone)
		e.exec(t, `INSERT INTO tenant_members(tenant_id,user_id,role) VALUES($1,$2,'viewer')`, admin.TenantID, userID)
	}
	for page, expectedID := range []string{"u_page_a", "u_page_b", "u_page_c"} {
		path := "/api/v1/admin/users?q=Page&verified=email&page=" + strconv.Itoa(page+1) + "&page_size=1"
		response := pgAdminRequest(e.router, http.MethodGet, path, admin.Access, "", nil)
		body := pgAdminBody(t, response, http.StatusOK)
		items, ok := body["items"].([]any)
		if !ok || len(items) != 1 || body["total"] != float64(3) {
			t.Fatalf("user pagination = %v", body)
		}
		row := items[0].(map[string]any)
		if row["id"] != expectedID || row["tenant_count"] != float64(1) || row["email_verified"] != true || row["phone_verified"] != false || row["last_login_at"] != nil {
			t.Errorf("equal-time user page = %v", row)
		}
		for _, sensitive := range []string{"private-password-hash", "13800000011", "13800000012", "13800000013"} {
			if strings.Contains(response.Body.String(), sensitive) {
				t.Errorf("account read exposed sensitive value %q", sensitive)
			}
		}
	}
	response := pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users/u_page_a", admin.Access, "", nil)
	detail := pgAdminBody(t, response, http.StatusOK)
	if detail["phone_masked"] != "138****0011" || strings.Contains(response.Body.String(), "13800000011") || strings.Contains(response.Body.String(), "private-password-hash") {
		t.Errorf("user detail must retain privacy: %v", detail)
	}
	e.exec(t, `UPDATE users SET status='disabled' WHERE id='u_page_b'`)
	filtered := pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users?q=Page&status=disabled", admin.Access, "", nil), http.StatusOK)
	if items, ok := filtered["items"].([]any); !ok || len(items) != 1 || items[0].(map[string]any)["id"] != "u_page_b" {
		t.Errorf("status filter returned wrong accounts: %v", filtered)
	}
	filtered = pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users?platform_role=platform_admin", admin.Access, "", nil), http.StatusOK)
	if items, ok := filtered["items"].([]any); !ok || len(items) != 1 || items[0].(map[string]any)["id"] != admin.ID {
		t.Errorf("platform role filter confused member roles: %v", filtered)
	}
	filtered = pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users?q=Page&verified=phone", admin.Access, "", nil), http.StatusOK)
	if items, ok := filtered["items"].([]any); !ok || len(items) != 0 || filtered["total"] != float64(0) {
		t.Errorf("empty verified-phone response must be []: %v", filtered)
	}
}

func TestAccountAdminPGAdminPermissionsExcludeMemberRolesAndAPIKeys(t *testing.T) {
	e := newPGAdminEnv(t)
	admin := e.register(t, "admin-permissions@example.com", true)
	member := e.register(t, "member-permissions@example.com", false)
	for _, role := range []string{"tenant_admin", "analyst", "viewer"} {
		e.exec(t, `UPDATE tenant_members SET role=$3 WHERE tenant_id=$1 AND user_id=$2`, member.TenantID, member.ID, role)
		for _, tc := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/admin/users"},
			{http.MethodGet, "/api/v1/admin/tenants/" + member.TenantID},
			{http.MethodPost, "/api/v1/admin/users/" + admin.ID + "/disable"},
			{http.MethodPut, "/api/v1/admin/users/" + member.ID + "/platform-role"},
			{http.MethodPut, "/api/v1/admin/tenants/" + member.TenantID + "/members/" + member.ID + "/role"},
		} {
			pgAdminBody(t, pgAdminRequest(e.router, tc.method, tc.path, member.Access, "permission-boundary", map[string]any{
				"reason": "团队成员不得取得平台管理权限", "expected_version": 0, "platform_admin": true, "role": "tenant_admin",
			}), http.StatusForbidden)
		}
	}
	_, key, err := e.deps.APIKey.CreateKey(context.Background(), member.TenantID, "admin-boundary", nil)
	if err != nil {
		t.Fatal(err)
	}
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodGet, "/api/v1/admin/users", key, "api-key-admin-denied", nil), http.StatusForbidden)
	pgAdminBody(t, pgAdminRequest(e.router, http.MethodPut, "/api/v1/admin/tenants/"+member.TenantID+"/members/"+member.ID+"/role", key, "api-key-role-denied", map[string]any{
		"reason": "机器密钥不得管理成员", "expected_version": 0, "role": "tenant_admin",
	}), http.StatusForbidden)
	var roles, audits int
	var versions int64
	if err := e.pool.QueryRow(context.Background(), `SELECT
		(SELECT COUNT(*) FROM platform_user_roles),(SELECT COUNT(*) FROM audit_logs),(SELECT SUM(row_version) FROM users)`).
		Scan(&roles, &audits, &versions); err != nil {
		t.Fatal(err)
	}
	if roles != 1 || audits != 0 || versions != 0 {
		t.Errorf("denied admin request changed role/audit/version: roles=%d audits=%d versions=%d", roles, audits, versions)
	}
}
