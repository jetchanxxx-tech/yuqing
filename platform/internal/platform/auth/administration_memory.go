package auth

import (
	"context"
	"time"
)

// AdministrationUser omits credentials and profile data outside the K2 read
// model. Transactions can commit only status and authorization versions.
type AdministrationUser struct {
	ID, Email, Name, Phone, Status                string
	CreatedAt                                     time.Time
	LastLoginAt, EmailVerifiedAt, PhoneVerifiedAt *time.Time
	TokenVersion, RowVersion                      int64
}

// AdministrationMember identifies one existing membership, including its CAS
// version. These transaction rows are shared with current authentication.
type AdministrationMember struct {
	TenantID, UserID, Role string
	RowVersion             int64
}
type AdministrationState struct {
	Users         map[string]AdministrationUser
	PlatformRoles map[string][]string
	Members       []AdministrationMember
}

func (m *MemoryStore) administrationStateLocked() *AdministrationState {
	s := &AdministrationState{Users: map[string]AdministrationUser{}, PlatformRoles: map[string][]string{}, Members: make([]AdministrationMember, 0, len(m.roleByMember))}
	for id, u := range m.usersByID {
		s.Users[id] = AdministrationUser{ID: u.ID, Email: u.Email, Name: u.Name, Phone: u.Phone, Status: u.Status, CreatedAt: u.CreatedAt,
			LastLoginAt: administrationTime(u.LastLoginAt), EmailVerifiedAt: administrationTime(u.EmailVerifiedAt), PhoneVerifiedAt: administrationTime(u.PhoneVerifiedAt), TokenVersion: u.TokenVersion, RowVersion: u.RowVersion}
	}
	for id, roles := range m.platformRolesByUser {
		s.PlatformRoles[id] = append([]string{}, roles...)
	}
	for key, role := range m.roleByMember {
		s.Members = append(s.Members, AdministrationMember{key.tenantID, key.userID, role, m.memberVersions[key]})
	}
	return s
}

func administrationTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

// ReadAdministration supplies detached rows while holding the same identity
// lock as registration, password changes and authorization resolution.
func (m *MemoryStore) ReadAdministration(_ context.Context, read func(*AdministrationState) error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return read(m.administrationStateLocked())
}

// UpdateAdministration stages mutations under the identity lock and commits
// only after the production administrative callback succeeds. It deliberately
// updates existing identities/memberships only; it cannot create membership.
func (m *MemoryStore) UpdateAdministration(_ context.Context, change func(*AdministrationState) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.administrationStateLocked()
	if err := change(s); err != nil {
		return err
	}
	for id := range m.usersByID {
		u, exists := s.Users[id]
		if !exists {
			continue
		}
		m.usersByID[id].Status = u.Status
		m.usersByID[id].Name = u.Name
		m.usersByID[id].RowVersion = u.RowVersion
		m.usersByID[id].TokenVersion = u.TokenVersion
		m.platformRolesByUser[id] = append([]string{}, s.PlatformRoles[id]...)
	}
	for _, member := range s.Members {
		key := memberKey{member.TenantID, member.UserID}
		if _, exists := m.roleByMember[key]; exists {
			m.roleByMember[key] = member.Role
			m.memberVersions[key] = member.RowVersion
		}
	}
	return nil
}
