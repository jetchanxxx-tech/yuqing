package auth

import (
	"context"
	"sort"
	"sync"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
	"github.com/yuqing/platform/internal/pkg/storage"
	"github.com/yuqing/platform/internal/platform/accountclosure"
	"github.com/yuqing/platform/internal/platform/billing"
)

// Administration's outer boundary precedes identity, tenant, verification and
// credit locks. Memory stores cannot coordinate separate CLI/server processes.
type ClosureMemoryBoundary func(context.Context, string, bool, func(func([]string)) error) error
type MemoryClosureStore struct {
	mu           sync.Mutex
	users        *SharedTenantStore
	verification *MemoryVerificationStore
	boundary     ClosureMemoryBoundary
	inspect      func(context.Context, string) (string, []string, error)
	revoke       func(string, []string, bool)
	avatars      accountclosure.AvatarLifecycle
	now          func() time.Time
	records      map[string]*memoryClosure
}
type memoryClosure struct {
	status  accountclosure.Status
	tenants []accountclosure.TenantImpact
	cursor  int64
}

func NewMemoryClosureStore(users *SharedTenantStore, v *MemoryVerificationStore, boundary ClosureMemoryBoundary, inspect func(context.Context, string) (string, []string, error), revoke func(string, []string, bool), avatars accountclosure.AvatarLifecycle) *MemoryClosureStore {
	return &MemoryClosureStore{users: users, verification: v, boundary: boundary, inspect: inspect, revoke: revoke, avatars: avatars, now: time.Now, records: map[string]*memoryClosure{}}
}
func (m *MemoryClosureStore) locked(ctx context.Context, uid string, completion bool, fn func(func([]string)) error) error {
	if m.boundary == nil || m.users == nil || ctx.Err() != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	return m.boundary(ctx, uid, completion, func(anon func([]string)) error {
		m.users.mu.Lock()
		defer m.users.mu.Unlock()
		m.mu.Lock()
		defer m.mu.Unlock()
		return fn(anon)
	})
}
func (m *MemoryClosureStore) user(actor accountclosure.Actor, status string) (*User, error) {
	u := m.users.usersByID[actor.UserID]
	if u == nil {
		return nil, pkgerrors.ErrNotFound
	}
	if u.Status != status || u.TokenVersion != actor.Version || actor.PasswordHash != "" && u.PasswordHash != actor.PasswordHash {
		return nil, pkgerrors.ErrConflict
	}
	return u, nil
}
func (m *MemoryClosureStore) preview(ctx context.Context, uid string, pending bool) (*accountclosure.Preview, error) {
	p := &accountclosure.Preview{WithdrawalDays: 7, Tenants: []accountclosure.TenantImpact{}, Blockers: []string{}}
	admins := 0
	for id, roles := range m.users.platformRolesByUser {
		for _, r := range roles {
			if r == rolePlatformAdmin && m.users.usersByID[id].Status == "active" {
				admins++
			}
		}
	}
	for _, r := range m.users.platformRolesByUser[uid] {
		if !pending && r == rolePlatformAdmin && admins < 2 {
			p.Blockers = append(p.Blockers, "LAST_PLATFORM_ADMIN")
		}
	}
	for key, role := range m.users.roleByMember {
		if key.userID != uid {
			continue
		}
		t, err := m.users.tenants.Get(ctx, key.tenantID)
		if err != nil {
			return nil, err
		}
		x := accountclosure.TenantImpact{ID: t.ID, Name: t.Name, Status: string(t.Status), Role: role}
		otherAdmins := 0
		for k, r := range m.users.roleByMember {
			if k.tenantID == t.ID {
				x.Members++
				if k.userID != uid && r == "tenant_admin" && m.users.usersByID[k.userID].Status == "active" {
					otherAdmins++
				}
			}
		}
		plan, blocks, err := m.inspect(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		x.PlanCode = plan
		if catalog := billing.DefaultPlans()[plan]; catalog != nil {
			d := catalog.RetentionDays
			x.RetentionDays = &d
		}
		if x.Members > 1 && role == "tenant_admin" && otherAdmins == 0 {
			p.Blockers = append(p.Blockers, "TEAM_ADMIN_HANDOFF:"+t.ID)
		}
		if x.Members == 1 {
			p.Blockers = append(p.Blockers, blocks...)
		}
		p.Tenants = append(p.Tenants, x)
	}
	sort.Slice(p.Tenants, func(i, j int) bool { return p.Tenants[i].ID < p.Tenants[j].ID })
	return p, nil
}
func (m *MemoryClosureStore) Preview(ctx context.Context, uid string, version int64) (out *accountclosure.Preview, err error) {
	err = m.locked(ctx, uid, false, func(_ func([]string)) error {
		if _, e := m.user(accountclosure.Actor{UserID: uid, Version: version}, "active"); e != nil {
			return e
		}
		var e error
		out, e = m.preview(ctx, uid, false)
		return e
	})
	return
}
func (m *MemoryClosureStore) Request(ctx context.Context, actor accountclosure.Actor, confirmed []string) (out *accountclosure.Status, err error) {
	err = m.locked(ctx, actor.UserID, false, func(_ func([]string)) error {
		u, e := m.user(actor, "active")
		if e != nil {
			return e
		}
		p, e := m.preview(ctx, actor.UserID, false)
		if e != nil {
			return e
		}
		if len(p.Blockers) > 0 {
			return pkgerrors.WithDetails(pkgerrors.ErrConflict, map[string]any{"blockers": p.Blockers})
		}
		sole := []string{}
		for _, t := range p.Tenants {
			if t.Members == 1 {
				sole = append(sole, t.ID)
			}
		}
		sort.Strings(confirmed)
		if len(sole) != len(confirmed) {
			return pkgerrors.ErrConflict
		}
		for i := range sole {
			if sole[i] != confirmed[i] {
				return pkgerrors.ErrConflict
			}
		}
		now := m.now().UTC().Truncate(time.Microsecond)
		r := &memoryClosure{status: accountclosure.Status{ID: id.New(), State: "pending", RequestedAt: now, WithdrawUntil: now.Add(168 * time.Hour), CleanupStatus: "pending"}, tenants: p.Tenants}
		m.records[u.ID] = r
		u.Status = "closure_pending"
		u.TokenVersion++
		u.RowVersion++
		m.verification.mu.Lock()
		m.verification.invalidateLocked(u.ID, now)
		for k, c := range m.verification.credentials {
			if c.IssuerUserID == u.ID {
				c.UsedAt = &now
				m.verification.credentials[k] = c
			}
		}
		for k, n := range m.verification.notices {
			if c := m.verification.credentials[k]; c.UserID == u.ID && n.State != "processing" {
				n.State = "cancelled"
				n.Recipient = ""
				m.verification.notices[k] = n
			}
		}
		m.verification.mu.Unlock()
		m.revoke(u.ID, sole, false)
		cp := r.status
		out = &cp
		return nil
	})
	return
}
func (m *MemoryClosureStore) Cancel(ctx context.Context, actor accountclosure.Actor) (out *accountclosure.Status, err error) {
	err = m.locked(ctx, actor.UserID, false, func(_ func([]string)) error {
		u, e := m.user(actor, "closure_pending")
		if e != nil {
			return e
		}
		r := m.records[u.ID]
		if r == nil {
			return pkgerrors.ErrNotFound
		}
		if r.status.State != "pending" || !m.now().Before(r.status.WithdrawUntil) {
			return pkgerrors.ErrConflict
		}
		u.Status = "active"
		u.TokenVersion++
		u.RowVersion++
		r.status.State = "cancelled"
		r.status.CleanupStatus = "cancelled"
		cp := r.status
		out = &cp
		return nil
	})
	return
}
func (m *MemoryClosureStore) Status(ctx context.Context, uid string) (out *accountclosure.Status, err error) {
	err = m.locked(ctx, uid, false, func(_ func([]string)) error {
		r := m.records[uid]
		if r == nil {
			return pkgerrors.ErrNotFound
		}
		cp := r.status
		out = &cp
		return nil
	})
	return
}
func (m *MemoryClosureStore) Process(ctx context.Context, uid string, limit int) (out *accountclosure.Status, err error) {
	if limit < 1 || limit > 1000 {
		return nil, pkgerrors.ErrBadRequest
	}
	err = m.locked(ctx, uid, true, func(anon func([]string)) error {
		r := m.records[uid]
		if r == nil {
			return pkgerrors.ErrNotFound
		}
		cp := r.status
		out = &cp
		if r.status.State == "completed" || r.status.State == "cancelled" || m.now().Before(r.status.WithdrawUntil) {
			return nil
		}
		u := m.users.usersByID[uid]
		if u == nil || u.Status != "closure_pending" {
			return pkgerrors.ErrConflict
		}
		p, e := m.preview(ctx, uid, true)
		if e != nil {
			return e
		}
		if len(p.Blockers) > 0 {
			r.status.LastError = p.Blockers[0]
			*out = r.status
			return nil
		}
		if len(p.Tenants) != len(r.tenants) {
			r.status.LastError = "TEAM_IMPACT_CHANGED"
			*out = r.status
			return nil
		}
		for i, t := range p.Tenants {
			if t.ID != r.tenants[i].ID || (t.Members == 1) != (r.tenants[i].Members == 1) {
				r.status.LastError = "TEAM_IMPACT_CHANGED"
				*out = r.status
				return nil
			}
		}
		if u.AvatarURL != "" && !storage.OwnsAvatarReference(uid, u.AvatarURL) {
			r.status.LastError = "AVATAR_REFERENCE_UNVERIFIED"
			*out = r.status
			return nil
		}
		if r.status.State == "pending" {
			u.TokenVersion++
			u.RowVersion++
			r.status.State = "finalizing"
		}
		r.status.Attempts++
		r.status.CleanupStatus = "avatars"
		if m.avatars == nil {
			r.status.LastError = "AVATAR_STORAGE_UNAVAILABLE"
			*out = r.status
			return nil
		}
		if e = m.avatars.Seal(ctx, uid); e != nil {
			r.status.LastError = "AVATAR_CLEANUP_FAILED"
			*out = r.status
			return nil
		}
		next, done, n, e := m.avatars.Reconcile(ctx, uid, r.cursor, limit)
		if e != nil {
			r.status.LastError = "AVATAR_CLEANUP_FAILED"
			*out = r.status
			return nil
		}
		r.cursor = next
		r.status.AvatarDeleted += int64(n)
		if !done {
			*out = r.status
			return nil
		}
		sole := []string{}
		for _, t := range r.tenants {
			if t.Members == 1 {
				sole = append(sole, t.ID)
			}
		}
		return m.users.tenants.CloseSoleTeams(ctx, sole, func() {
			m.verification.mu.Lock()
			defer m.verification.mu.Unlock()
			for k, c := range m.verification.credentials {
				if c.UserID == uid || c.IssuerUserID == uid {
					c.Target = ""
					c.Hash = ""
					now := m.now()
					c.UsedAt = &now
					m.verification.credentials[k] = c
					if n, ok := m.verification.notices[k]; ok {
						n.State = "cancelled"
						n.Recipient = ""
						m.verification.notices[k] = n
					}
				}
			}
			anon(sole)
			m.revoke(uid, sole, true)
			delete(m.users.usersByEmail, u.Email)
			u.Email = id.New() + ".closed@invalid"
			m.users.usersByEmail[u.Email] = uid
			u.Status = "closed"
			u.Name = "已注销用户"
			u.PasswordHash = ""
			u.Phone = ""
			u.AvatarURL = ""
			u.EmailVerifiedAt = nil
			u.PhoneVerifiedAt = nil
			u.LastLoginAt = nil
			u.PasswordChangedAt = nil
			u.Timezone = DefaultTimezone
			u.TokenVersion++
			u.RowVersion++
			delete(m.users.platformRolesByUser, uid)
			delete(m.users.tenantOfUser, uid)
			for k := range m.users.roleByMember {
				if k.userID == uid {
					delete(m.users.roleByMember, k)
					delete(m.users.memberVersions, k)
				}
			}
			now := m.now().UTC()
			r.status.State = "completed"
			r.status.CompletedAt = &now
			r.status.CleanupStatus = "pending_cleanup"
			r.status.LastError = "MEMORY_BUSINESS_RETENTION_UNVERIFIED"
			*out = r.status
		})
	})
	return
}
