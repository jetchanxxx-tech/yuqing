package settings

import (
	"context"
	"encoding/json"
	"strings"
)

const SecretMask = "********"

// AdminStore retains the original authenticated version through its read/write
// transaction. Internal adapters continue to use the unredacted Store methods.
type AdminStore interface {
	AdminAll(context.Context, string, int64) (map[string]string, error)
	AdminPatch(context.Context, string, int64, map[string]string) error
}

func secretKey(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "secret") || strings.Contains(k, "password") || strings.Contains(k, "private_key") || strings.Contains(k, "certificate") || strings.HasSuffix(k, "api_key") || strings.HasSuffix(k, "access_key_id") || strings.HasSuffix(k, "token") || k == "cert_pfx"
}
func redactValue(k, v string) string {
	if secretKey(k) && v != "" {
		return SecretMask
	}
	if strings.HasPrefix(k, "payment_") {
		var obj map[string]string
		if json.Unmarshal([]byte(v), &obj) == nil {
			for key, value := range obj {
				obj[key] = redactValue(key, value)
			}
			b, _ := json.Marshal(obj)
			return string(b)
		}
		// Malformed provider configuration is never echoed to clients.
		if v != "" {
			return SecretMask
		}
	}
	return v
}
func Redact(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = redactValue(k, v)
	}
	return out
}
func preserveSecret(k, v, old string) string {
	if v == SecretMask {
		return old
	}
	if strings.HasPrefix(k, "payment_") {
		var next, previous map[string]string
		if json.Unmarshal([]byte(v), &next) == nil {
			_ = json.Unmarshal([]byte(old), &previous)
			for key, value := range next {
				if value == SecretMask {
					next[key] = previous[key]
				}
			}
			b, _ := json.Marshal(next)
			return string(b)
		}
	}
	return v
}

// Patch is for a memory store while the caller holds its account authority lock.
func (s *MemoryStore) Patch(values map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range values {
		s.data[k] = preserveSecret(k, v, s.data[k])
	}
}
