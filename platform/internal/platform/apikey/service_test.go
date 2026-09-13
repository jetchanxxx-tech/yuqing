// Package apikey provides tenant-scoped machine credentials (pangu_ keys).
package apikey

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

func newTestService() *Service { return NewService(NewMemoryStore()) }

// --- CreateKey ---------------------------------------------------------------

func TestAPIKey_CreateKey_returnsRawOnceWithPrefix(t *testing.T) {
	svc := newTestService()
	key, raw, err := svc.CreateKey(context.Background(), "t1", "ci-bot", []string{"analyses:create"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if !strings.HasPrefix(raw, KeyPrefix) {
		t.Errorf("raw key = %q, want %q prefix", raw, KeyPrefix)
	}
	if len(raw) != len(KeyPrefix)+26 {
		t.Errorf("len(raw) = %d, want %d (pangu_ + ULID)", len(raw), len(KeyPrefix)+26)
	}
	if key.ID == "" {
		t.Error("returned key metadata has empty ID")
	}
	if key.TenantID != "t1" || key.Name != "ci-bot" {
		t.Errorf("metadata = %+v, want tenant t1 name ci-bot", key)
	}
	if len(key.Scopes) != 1 || key.Scopes[0] != "analyses:create" {
		t.Errorf("scopes = %v, want [analyses:create]", key.Scopes)
	}
	if key.RevokedAt != nil {
		t.Errorf("fresh key must not be revoked: %v", key.RevokedAt)
	}
	if key.Prefix == "" || !strings.HasPrefix(raw, key.Prefix) {
		t.Errorf("display prefix %q does not match raw key %q", key.Prefix, raw)
	}
	// The raw key must never appear in serialized metadata.
	b, _ := json.Marshal(key)
	if strings.Contains(string(b), raw) || strings.Contains(string(b), "key_hash") {
		t.Errorf("serialized metadata leaks secret: %s", b)
	}
}

func TestAPIKey_CreateKey_validatesInput(t *testing.T) {
	svc := newTestService()
	cases := []struct {
		name    string
		tenant  string
		keyName string
	}{
		{"missing tenant", "", "n"},
		{"missing name", "t1", "  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := svc.CreateKey(context.Background(), tc.tenant, tc.keyName, nil); err == nil {
				t.Error("CreateKey() error = nil, want validation error")
			}
		})
	}
}

func TestAPIKey_CreateKey_unique(t *testing.T) {
	svc := newTestService()
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		_, raw, err := svc.CreateKey(context.Background(), "t1", "k", nil)
		if err != nil {
			t.Fatalf("CreateKey %d: %v", i, err)
		}
		if seen[raw] {
			t.Fatalf("duplicate key generated: %s", raw)
		}
		seen[raw] = true
	}
}

// --- ValidateKey ---------------------------------------------------------------

func TestAPIKey_ValidateKey_roundTrip(t *testing.T) {
	ctx := context.Background()
	svc := newTestService()
	created, raw, err := svc.CreateKey(ctx, "t1", "ci", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ValidateKey(ctx, raw)
	if err != nil {
		t.Fatalf("ValidateKey: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("validated id = %q, want %q", got.ID, created.ID)
	}
	if got.LastUsedAt == nil {
		t.Error("LastUsedAt not touched on validation")
	}

	t.Run("repeat validation succeeds", func(t *testing.T) {
		if _, err := svc.ValidateKey(ctx, raw); err != nil {
			t.Fatalf("second ValidateKey: %v", err)
		}
	})
}

func TestAPIKey_ValidateKey_rejections(t *testing.T) {
	ctx := context.Background()
	svc := newTestService()
	_, raw, err := svc.CreateKey(ctx, "t1", "ci", nil)
	if err != nil {
		t.Fatal(err)
	}
	revoked, revokedRaw, err := svc.CreateKey(ctx, "t1", "old", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeKey(ctx, "t1", revoked.ID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	cases := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"wrong scheme prefix", "sk-" + raw[6:]},
		{"prefix only", KeyPrefix},
		{"unknown but well-formed", KeyPrefix + "0123456789ABCDEFGHJKMNPQRS"},
		{"revoked key", revokedRaw},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.ValidateKey(ctx, tc.key); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
				t.Errorf("ValidateKey(%q) err = %v, want UNAUTHORIZED", tc.key, err)
			}
		})
	}
}

// --- ListKeys ---------------------------------------------------------------

func TestAPIKey_ListKeys_scopedAndSafe(t *testing.T) {
	ctx := context.Background()
	svc := newTestService()
	_, rawA, _ := svc.CreateKey(ctx, "t1", "a", nil)
	_, rawB, _ := svc.CreateKey(ctx, "t1", "b", nil)
	_, rawC, _ := svc.CreateKey(ctx, "t2", "c", nil)

	keys, err := svc.ListKeys(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("ListKeys(t1) len = %d, want 2 (cross-tenant leak if 3)", len(keys))
	}
	b, _ := json.Marshal(keys)
	for _, raw := range []string{rawA, rawB, rawC} {
		if strings.Contains(string(b), raw) {
			t.Errorf("list output leaks raw key: %s", b)
		}
	}
	if strings.Contains(string(b), "key_hash") {
		t.Errorf("list output leaks hash field: %s", b)
	}
}

func TestAPIKey_ListKeys_includesRevokedWithTimestamp(t *testing.T) {
	ctx := context.Background()
	svc := newTestService()
	k, _, _ := svc.CreateKey(ctx, "t1", "gone", nil)
	if err := svc.RevokeKey(ctx, "t1", k.ID); err != nil {
		t.Fatal(err)
	}
	keys, _ := svc.ListKeys(ctx, "t1")
	if len(keys) != 1 {
		t.Fatalf("len = %d, want 1 (revoked keys stay visible)", len(keys))
	}
	if keys[0].RevokedAt == nil {
		t.Error("revoked key missing revoked_at in listing")
	}
}

// --- RevokeKey ---------------------------------------------------------------

func TestAPIKey_RevokeKey(t *testing.T) {
	ctx := context.Background()
	svc := newTestService()
	k1, _, _ := svc.CreateKey(ctx, "t1", "mine", nil)
	k2, _, _ := svc.CreateKey(ctx, "t2", "theirs", nil)

	t.Run("unknown id is not found", func(t *testing.T) {
		if err := svc.RevokeKey(ctx, "t1", "nope"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want NOT_FOUND", err)
		}
	})
	t.Run("other tenant's key is not found", func(t *testing.T) {
		if err := svc.RevokeKey(ctx, "t1", k2.ID); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
			t.Errorf("err = %v, want NOT_FOUND", err)
		}
	})
	t.Run("owner revokes", func(t *testing.T) {
		if err := svc.RevokeKey(ctx, "t1", k1.ID); err != nil {
			t.Fatalf("RevokeKey: %v", err)
		}
	})
	t.Run("revoke is idempotent", func(t *testing.T) {
		if err := svc.RevokeKey(ctx, "t1", k1.ID); err != nil {
			t.Fatalf("second RevokeKey: %v", err)
		}
	})
	t.Run("revoked key fails validation", func(t *testing.T) {
		k3, raw3, err := svc.CreateKey(ctx, "t1", "doomed", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ValidateKey(ctx, raw3); err != nil {
			t.Fatalf("pre-revoke ValidateKey: %v", err)
		}
		if err := svc.RevokeKey(ctx, "t1", k3.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ValidateKey(ctx, raw3); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
			t.Errorf("post-revoke ValidateKey err = %v, want UNAUTHORIZED", err)
		}
	})
}

func TestAPIKey_StoreGetByHash_notFound(t *testing.T) {
	st := NewMemoryStore()
	if _, err := st.GetByHash(context.Background(), "deadbeef"); !pkgerrors.Is(err, pkgerrors.ErrNotFound) {
		t.Errorf("err = %v, want NOT_FOUND", err)
	}
}
