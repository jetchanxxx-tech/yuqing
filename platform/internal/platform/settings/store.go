// Package settings provides platform-level configuration that platform
// administrators can edit through the admin panel (API keys, feature flags, etc.).
package settings

import (
	"context"
	"sync"
)

// Store persists platform-scoped key-value settings. Every read returns the
// current value; writes are immediately visible to all subsequent reads.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
	All(ctx context.Context) (map[string]string, error)
}

// MemoryStore is an in-memory implementation for MVP (restart = reset).
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]string
}

// NewMemoryStore creates a store pre-populated with environment defaults.
func NewMemoryStore(envDefaults map[string]string) *MemoryStore {
	m := &MemoryStore{data: make(map[string]string)}
	for k, v := range envDefaults {
		m.data[k] = v
	}
	return m
}

func (s *MemoryStore) Get(ctx context.Context, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key], nil
}

func (s *MemoryStore) Set(ctx context.Context, key string, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *MemoryStore) All(ctx context.Context) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out, nil
}