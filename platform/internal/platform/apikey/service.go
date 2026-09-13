// Package apikey provides tenant-scoped machine credentials: opaque
// pangu_-prefixed API keys whose SHA-256 hash (never the raw key) is stored.
// The raw key is revealed exactly once, at creation.
package apikey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/id"
)

// KeyPrefix identifies a platform API key inside an Authorization header.
const KeyPrefix = "pangu_"

// displayPrefixLen is how many leading raw-key characters are safe to show
// in listings ("pangu_" + 6, enough to tell keys apart without leaking).
const displayPrefixLen = len(KeyPrefix) + 6

// APIKey is one machine credential. The secret itself is never held on the
// struct — only its hash (unexported, never serialized) and a display prefix.
type APIKey struct {
	ID         string     `json:"id"`
	TenantID   string     `json:"tenant_id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`

	keyHash string // SHA-256 hex of the raw key; never exposed via JSON
}

// IsRevoked reports whether the key has been revoked.
func (k *APIKey) IsRevoked() bool { return k.RevokedAt != nil }

// Store persists API key rows (platform database: api_keys table).
type Store interface {
	// Create inserts the key; duplicate IDs conflict.
	Create(ctx context.Context, k *APIKey) error
	// List returns the tenant's keys in creation order.
	List(ctx context.Context, tenantID string) ([]*APIKey, error)
	// Revoke stamps revoked_at on the tenant's key; unknown or foreign ids
	// answer ErrNotFound so cross-tenant probes cannot distinguish 403/404.
	Revoke(ctx context.Context, tenantID, id string) error
	// GetByHash returns the key whose hash matches (any tenant), or ErrNotFound.
	GetByHash(ctx context.Context, hash string) (*APIKey, error)
	// Touch records a last-use timestamp on the key.
	Touch(ctx context.Context, id string, at time.Time) error
}

// Service implements the API key lifecycle.
type Service struct {
	store Store
}

// NewService creates an API key service over the given store.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// hashKey is the lookup transform: keys are high-entropy (ULID-based), so a
// plain salted-free SHA-256 is sufficient (same trade-off as GitHub/OpenAI).
func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CreateKey mints a new API key and returns its metadata plus the RAW KEY —
// the only moment the secret is ever available. The store holds just the hash.
func (s *Service) CreateKey(ctx context.Context, tenantID, name string, scopes []string) (*APIKey, string, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, "", fmt.Errorf("apikey: tenant_id is required")
	}
	if strings.TrimSpace(name) == "" {
		return nil, "", fmt.Errorf("apikey: name is required")
	}

	raw := KeyPrefix + id.New()
	now := time.Now().UTC()
	key := &APIKey{
		ID:        id.New(),
		TenantID:  tenantID,
		Name:      name,
		Scopes:    scopes,
		Prefix:    raw[:displayPrefixLen],
		CreatedAt: now,
		keyHash:   hashKey(raw),
	}
	if err := s.store.Create(ctx, key); err != nil {
		return nil, "", err
	}
	cp := *key
	return &cp, raw, nil
}

// ValidateKey resolves a raw key to its metadata. Fail-closed: unknown,
// malformed or revoked keys all answer ErrUnauthorized (revoked carries a
// distinct message because the key demonstrably existed).
func (s *Service) ValidateKey(ctx context.Context, raw string) (*APIKey, error) {
	if raw == "" || !strings.HasPrefix(raw, KeyPrefix) {
		return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid api key")
	}
	key, err := s.store.GetByHash(ctx, hashKey(raw))
	if err != nil {
		if pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "invalid api key")
		}
		return nil, err
	}
	if key.IsRevoked() {
		return nil, pkgerrors.Wrap(pkgerrors.ErrUnauthorized, "api key has been revoked")
	}
	now := time.Now().UTC()
	if terr := s.store.Touch(ctx, key.ID, now); terr != nil {
		// A failed bookkeeping write must not lock the owner out.
		// TODO: surface as a metric once observ instrumentation lands.
		_ = terr
	}
	cp := *key
	cp.LastUsedAt = &now // reflect this validation even if Touch was best-effort
	return &cp, nil
}

// ListKeys returns the tenant's key metadata (never raw keys or hashes).
func (s *Service) ListKeys(ctx context.Context, tenantID string) ([]*APIKey, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, fmt.Errorf("apikey: tenant_id is required")
	}
	return s.store.List(ctx, tenantID)
}

// Revoke permanently disables a key. Idempotent: revoking a revoked key is a
// success (DELETE semantics).
func (s *Service) RevokeKey(ctx context.Context, tenantID, keyID string) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(keyID) == "" {
		return pkgerrors.Wrap(pkgerrors.ErrNotFound, "api key not found")
	}
	return s.store.Revoke(ctx, tenantID, keyID)
}
