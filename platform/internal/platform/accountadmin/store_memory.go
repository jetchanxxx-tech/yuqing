package accountadmin

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/credit"
	"github.com/yuqing/platform/internal/platform/payment"
	"github.com/yuqing/platform/internal/platform/tenant"
	"github.com/yuqing/platform/internal/pkg/id"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

// MemoryStore adapts the exact identity and tenant stores used by the service
// graph. It keeps only audit history; account and membership state stays in auth.
type MemoryStore struct {
	mu       sync.Mutex
	accounts *auth.SharedTenantStore
	tenants  *tenant.MemoryStore
	credits  *credit.Service
	payments *payment.Service
	audits   []Audit
}

func NewMemoryStore(accounts *auth.SharedTenantStore, tenants *tenant.MemoryStore, credits *credit.Service, payments *payment.Service) *MemoryStore {
	return &MemoryStore{accounts: accounts, tenants: tenants, credits: credits, payments: payments, audits: []Audit{}}
}

var _ Store = (*MemoryStore)(nil)

func (s *MemoryStore) CreatePending(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	uid, tid := id.New(), id.New()
	name := req.Name
	if req.TenantName != "" { name = req.TenantName }
	err := s.accounts.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
		u, ok := state.Users[req.ActorID]
		if !ok || u.Status != "active" || u.TokenVersion != req.ActorTokenVersion || !hasAdmin(state.PlatformRoles[req.ActorID]) { return conflict() }
		return nil
	})
	if err != nil { return nil, err }
	err = s.accounts.RegisterAccount(ctx, auth.User{ID:uid,Email:req.Email,Name:req.Name,PasswordHash:"$pending$"+uid,Status:"pending_activation",CreatedAt:time.Now().UTC()}, auth.Tenant{ID:tid,Name:name,Slug:"t-"+strings.ToLower(tid),DBName:"yuqing_"+strings.ToLower(tid),Status:"active",PlanCode:"free"}, auth.Member{TenantID:tid,UserID:uid,Role:"tenant_admin"}, false)
	if err != nil { return nil, err }
	if s.credits != nil { if err := s.credits.GrantTrial(ctx, tid, credit.TrialCredits); err != nil { return nil, pkgerrors.Wrap(pkgerrors.ErrInternal, "trial grant failed") } }
	s.audits = append(s.audits, Audit{ID:int64(len(s.audits)+1),ActorID:req.ActorID,Action:"account.create",TargetType:"user",TargetID:uid,TenantID:tid,Reason:"administrator account creation",CreatedAt:time.Now().UTC(),Before:map[string]any{},After:map[string]any{"status":"pending_activation","tenant_id":tid}})
	return &CreateResult{UserID:uid,TenantID:tid,Status:"pending_activation"}, nil
}

func memoryUser(u auth.AdministrationUser, state *auth.AdministrationState) UserRow {
	count := 0
	for _, m := range state.Members {
		if m.UserID == u.ID {
			count++
		}
	}
	return UserRow{ID: u.ID, Name: u.Name, Email: u.Email, PhoneMasked: MaskPhone(u.Phone), Status: u.Status,
		EmailVerified: u.EmailVerifiedAt != nil, PhoneVerified: u.PhoneVerifiedAt != nil, PlatformRoles: append([]string{}, state.PlatformRoles[u.ID]...),
		TenantCount: count, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt, RowVersion: u.RowVersion}
}

func (s *MemoryStore) ListUsers(ctx context.Context, q Query) ([]UserRow, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []UserRow{}
	err := s.accounts.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
		for _, u := range state.Users {
			row := memoryUser(u, state)
			if userMatches(row, q) {
				items = append(items, row)
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	total := len(items)
	start, end := pageBounds(total, q)
	return items[start:end], total, nil
}

func (s *MemoryStore) auditRows(targetType, targetID, tenantID string) []Audit {
	result := []Audit{}
	for _, a := range s.audits {
		if a.TargetType == targetType && a.TargetID == targetID || tenantID != "" && a.TenantID == tenantID {
			// Details returned to callers must not alias stored audit maps.
			b, _ := json.Marshal(a)
			var cp Audit
			_ = json.Unmarshal(b, &cp)
			result = append(result, cp)
		}
	}
	return result
}

func (s *MemoryStore) User(ctx context.Context, id string) (*UserDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := &UserDetail{Memberships: []Membership{}, AuditLogs: s.auditRows("user", id, "")}
	err := s.accounts.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
		u, ok := state.Users[id]
		if !ok {
			return missing()
		}
		result.UserRow = memoryUser(u, state)
		for _, m := range state.Members {
			if m.UserID != id {
				continue
			}
			t, err := s.tenants.Get(ctx, m.TenantID)
			if err != nil {
				return err
			}
			result.Memberships = append(result.Memberships, Membership{t.ID, t.Name, string(t.Status), m.Role, m.RowVersion})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result.Memberships, func(i, j int) bool { return result.Memberships[i].TenantID < result.Memberships[j].TenantID })
	return result, nil
}

func (s *MemoryStore) tenantRow(ctx context.Context, t tenant.Tenant, state *auth.AdministrationState) (TenantRow, *credit.Snapshot, error) {
	row := TenantRow{ID: t.ID, Name: t.Name, Slug: t.Slug, PlanCode: t.PlanCode, Status: string(t.Status), CreatedAt: t.CreatedAt, RowVersion: t.RowVersion, PlanSource: "unknown"}
	for _, m := range state.Members {
		if m.TenantID == t.ID {
			row.UserCount++
		}
	}
	pool, err := s.credits.Snapshot(ctx, t.ID)
	if err != nil {
		return row, nil, internal(err)
	}
	if pool != nil {
		row.EffectivePlanCode = pool.PlanCode
		row.PlanSource = "report_credits"
	}
	return row, pool, nil
}

func (s *MemoryStore) ListTenants(ctx context.Context, q Query) ([]TenantRow, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []TenantRow{}
	err := s.accounts.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
		tenants, err := s.tenants.List(ctx)
		if err != nil {
			return err
		}
		for _, t := range tenants {
			row, _, err := s.tenantRow(ctx, t, state)
			if err != nil {
				return err
			}
			if tenantMatches(row, q) {
				items = append(items, row)
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	total := len(items)
	start, end := pageBounds(total, q)
	return items[start:end], total, nil
}

func (s *MemoryStore) Tenant(ctx context.Context, id string) (*TenantDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := &TenantDetail{Members: []TenantMember{}, Orders: []*payment.Order{}, AuditLogs: s.auditRows("tenant", id, id)}
	err := s.accounts.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
		t, err := s.tenants.Get(ctx, id)
		if err != nil {
			return err
		}
		result.TenantRow, result.Credit, err = s.tenantRow(ctx, *t, state)
		if err != nil {
			return err
		}
		for _, m := range state.Members {
			if m.TenantID == id {
				u, ok := state.Users[m.UserID]
				if ok {
					result.Members = append(result.Members, TenantMember{u.ID, u.Name, u.Email, m.Role, m.RowVersion})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result.Members, func(i, j int) bool { return result.Members[i].UserID < result.Members[j].UserID })
	if s.payments != nil {
		orders, err := s.payments.List(ctx, id, int(^uint(0)>>1))
		if err != nil {
			return nil, internal(err)
		}
		if orders != nil {
			result.Orders = orders
		}
	}
	return result, nil
}

func (s *MemoryStore) Change(ctx context.Context, m Mutation) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := &Result{}
	audit := Audit{ID: int64(len(s.audits) + 1), ActorID: m.ActorID, Action: m.Action, TargetType: "user", TargetID: m.TargetID, TenantID: m.TenantID, Reason: m.Reason, RequestID: m.RequestID, CreatedAt: time.Now().UTC()}
	// Keep actor validation protected by the real identity lock through both
	// account and tenant commits. Identity precedes tenant, matching K1 reads
	// and registration; placing this check inside the tenant callback would
	// invert that order. The adapter mutex serializes administrative mutations.
	err := s.accounts.UpdateAdministration(ctx, func(state *auth.AdministrationState) error {
		actor, exists := state.Users[m.ActorID]
		if !exists || actor.Status != "active" || actor.TokenVersion != m.ActorTokenVersion || !hasAdmin(state.PlatformRoles[m.ActorID]) {
			return conflict()
		}
		if m.Action == "tenant.suspend" || m.Action == "tenant.resume" {
			audit.TargetType = "tenant"
			audit.TenantID = m.TargetID
			return s.tenants.ChangeAdministration(ctx, m.TargetID, func(t *tenant.Tenant) error {
				status := tenant.StatusSuspended
				from := tenant.StatusActive
				if m.Action == "tenant.resume" {
					status = tenant.StatusActive
					from = tenant.StatusSuspended
				}
				if t.RowVersion != m.ExpectedVersion || t.Status != from {
					return conflict()
				}
				audit.Before = map[string]any{"status": t.Status, "row_version": t.RowVersion}
				t.Status = status
				t.RowVersion++
				audit.After = map[string]any{"status": t.Status, "row_version": t.RowVersion}
				result.ID = t.ID
				result.Status = string(t.Status)
				result.RowVersion = t.RowVersion
				return nil
			})
		} else {
			u, ok := state.Users[m.TargetID]
			if !ok {
				return missing()
			}
			switch m.Action {
			case "user.disable", "user.enable":
				from, to := "active", "disabled"
				if m.Action == "user.enable" {
					from, to = "disabled", "active"
				}
				if u.RowVersion != m.ExpectedVersion || u.Status != from {
					return conflict()
				}
				audit.Before = map[string]any{"status": u.Status, "row_version": u.RowVersion}
				u.Status = to
				u.RowVersion++
				u.TokenVersion++
				audit.After = map[string]any{"status": u.Status, "row_version": u.RowVersion}
				result.ID = u.ID
				result.Status = u.Status
				result.RowVersion = u.RowVersion
			case "user.nickname":
				if u.RowVersion != m.ExpectedVersion { return conflict() }
				audit.Before = map[string]any{"name":u.Name,"row_version":u.RowVersion}
				u.Name = m.Role
				u.RowVersion++
				audit.After = map[string]any{"name":u.Name,"row_version":u.RowVersion}
				result.ID=u.ID; result.Status=u.Status; result.RowVersion=u.RowVersion
			case "user.platform_role":
				roles := state.PlatformRoles[u.ID]
				if u.RowVersion != m.ExpectedVersion || hasAdmin(roles) == m.PlatformAdmin {
					return conflict()
				}
				audit.Before = map[string]any{"platform_roles": append([]string{}, roles...), "row_version": u.RowVersion}
				roles = []string{}
				if m.PlatformAdmin {
					roles = append(roles, "platform_admin")
				}
				state.PlatformRoles[u.ID] = roles
				u.RowVersion++
				u.TokenVersion++
				audit.After = map[string]any{"platform_roles": roles, "row_version": u.RowVersion}
				result.ID = u.ID
				result.PlatformRoles = &roles
				result.RowVersion = u.RowVersion
			case "member.role":
				found := -1
				admins := 0
				for i, member := range state.Members {
					if member.TenantID == m.TenantID {
						if member.Role == "tenant_admin" {
							admins++
						}
						if member.UserID == m.TargetID {
							found = i
						}
					}
				}
				if found < 0 {
					return missing()
				}
				member := &state.Members[found]
				if member.RowVersion != m.ExpectedVersion || member.Role == m.Role || member.Role == "tenant_admin" && m.Role != "tenant_admin" && admins <= 1 {
					return conflict()
				}
				audit.Before = map[string]any{"role": member.Role, "row_version": member.RowVersion}
				member.Role = m.Role
				member.RowVersion++
				if m.Role == "tenant_admin" {
					admins++
				}
				if audit.Before["role"] == "tenant_admin" {
					admins--
				}
				if admins < 1 {
					return conflict()
				}
				u.RowVersion++
				u.TokenVersion++
				audit.After = map[string]any{"role": member.Role, "row_version": member.RowVersion}
				result.TenantID = m.TenantID
				result.UserID = u.ID
				result.Role = member.Role
				result.RowVersion = member.RowVersion
			default:
				return conflict()
			}
			state.Users[u.ID] = u
			if m.Action != "member.role" {
				admins := 0
				for id, account := range state.Users {
					if account.Status == "active" && hasAdmin(state.PlatformRoles[id]) {
						admins++
					}
				}
				if admins < 1 {
					return conflict()
				}
			}
			return nil
		}
	})
	if err != nil {
		return nil, err
	}
	s.audits = append(s.audits, audit)
	return result, nil
}
