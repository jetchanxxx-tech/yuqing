package accountnotify

import (
	"context"
	"sync"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/notification"
	"github.com/yuqing/platform/internal/platform/auth"
	"github.com/yuqing/platform/internal/platform/tenant"
)

type memoryEvent struct {
	ID, UserID, TenantID, AnalysisID, State string
	Epoch                                   int64
	Attempt                                 int
	Next                                    time.Time
	Receipt                                 notification.Receipt
}
type MemoryStore struct {
	mu         sync.Mutex
	accounts   *auth.SharedTenantStore
	tenants    *tenant.MemoryStore
	prefs      map[string]Preferences
	epochs     map[string]int64
	sealed     map[string]bool
	processing map[string]int
	events     map[string]memoryEvent
}

func NewMemoryStore(accounts *auth.SharedTenantStore, tenants *tenant.MemoryStore) *MemoryStore {
	return &MemoryStore{accounts: accounts, tenants: tenants, prefs: map[string]Preferences{}, epochs: map[string]int64{}, sealed: map[string]bool{}, processing: map[string]int{}, events: map[string]memoryEvent{}}
}
func (m *MemoryStore) readActor(ctx context.Context, actor auth.Principal, change func() error) error {
	return m.accounts.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
		u, ok := state.Users[actor.UserID]
		if !ok || u.Status != "active" || u.TokenVersion != actor.TokenVersion {
			return pkgerrors.ErrConflict
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		return change()
	})
}
func (m *MemoryStore) preferences(uid string) Preferences {
	p, ok := m.prefs[uid]
	if !ok {
		return Preferences{TaskCompleted: true}
	}
	return p
}
func (m *MemoryStore) Preferences(ctx context.Context, actor auth.Principal) (prefs Preferences, err error) {
	err = m.readActor(ctx, actor, func() error { prefs = m.preferences(actor.UserID); return nil })
	return
}
func (m *MemoryStore) SetPreferences(ctx context.Context, actor auth.Principal, prefs Preferences) error {
	return m.accounts.UpdateAdministration(ctx, func(state *auth.AdministrationState) error {
		u, ok := state.Users[actor.UserID]
		if !ok || u.Status != "active" || u.TokenVersion != actor.TokenVersion {
			return pkgerrors.ErrConflict
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.prefs[actor.UserID] = prefs
		u.RowVersion++
		state.Users[actor.UserID] = u
		return nil
	})
}

// Enqueue receives only immutable completion facts while the analysis mutation
// lock is held. It never reads users, sends, or inverts users -> analysis locks.
func (m *MemoryStore) Enqueue(uid, tid, aid, key string) {
	if uid == "" || tid == "" || aid == "" || key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed[uid] {
		return
	}
	if _, ok := m.events[key]; ok {
		return
	}
	m.events[key] = memoryEvent{ID: key, UserID: uid, TenantID: tid, AnalysisID: aid, State: "pending", Epoch: m.epochs[uid], Next: time.Now()}
}
func (m *MemoryStore) CancelPending(uid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.epochs[uid]++
	m.sealed[uid] = true
}
func (m *MemoryStore) Resume(uid string) { m.mu.Lock(); defer m.mu.Unlock(); delete(m.sealed, uid) }
func (m *MemoryStore) CanClose(uid string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processing[uid] == 0
}
func (m *MemoryStore) Anonymize(uid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.prefs, uid)
}
func (m *MemoryStore) Claim(ctx context.Context) (result *Intent, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	err = m.accounts.ReadAdministration(ctx, func(state *auth.AdministrationState) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		for key, n := range m.events {
			if (n.State != "pending" && n.State != "failed" && n.State != "processing") || time.Now().Before(n.Next) {
				continue
			}
			u, ok := state.Users[n.UserID]
			allowed := ok && u.Status == "active" && u.EmailVerifiedAt != nil && m.preferences(n.UserID).TaskCompleted && n.Epoch == m.epochs[n.UserID]
			member := false
			for _, link := range state.Members {
				if link.UserID == n.UserID && link.TenantID == n.TenantID {
					member = true
					break
				}
			}
			t, e := m.tenants.Get(ctx, n.TenantID)
			allowed = allowed && member && e == nil && t.Status == tenant.StatusActive
			if !allowed {
				if n.State == "processing" {
					continue
				}
				n.State = "cancelled"
				m.events[key] = n
				result = &Intent{ID: key, Cancelled: true}
				return nil
			}
			if n.State != "processing" {
				m.processing[n.UserID]++
			}
			n.State = "processing"
			n.Attempt++
			n.Next = time.Now().Add(2 * time.Minute)
			m.events[key] = n
			result = &Intent{ID: key, Recipient: u.Email, Attempt: n.Attempt}
			return nil
		}
		return nil
	})
	return
}
func (m *MemoryStore) Complete(_ context.Context, n Intent, receipt notification.Receipt) error {
	r, err := notification.Normalize(receipt, "task_completed")
	if err != nil {
		return pkgerrors.ErrBadRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[n.ID]
	if !ok || e.State != "processing" || e.Attempt != n.Attempt {
		return pkgerrors.ErrConflict
	}
	m.processing[e.UserID]--
	e.State = "failed"
	e.Next = time.Now().Add(time.Minute)
	e.Receipt = r
	if r.State == "accepted" {
		e.State = "accepted"
	}
	m.events[n.ID] = e
	return nil
}
