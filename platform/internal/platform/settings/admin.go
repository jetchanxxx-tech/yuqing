package settings

import (
	"context"
	"encoding/json"
	"io"
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
	return strings.Contains(k, "secret") || strings.Contains(k, "password") || strings.Contains(k, "private_key") || strings.Contains(k, "certificate") || strings.HasSuffix(k, "api_key") || strings.HasSuffix(k, "access_key_id") || strings.HasSuffix(k, "token") || strings.HasSuffix(k, "cert_pfx") || k == "api_v3_key" || k == "alipay_public_key"
}
func decodeConfig(v string) (map[string]any, bool) {
	d := json.NewDecoder(strings.NewReader(v))
	d.UseNumber()
	var obj map[string]any
	if d.Decode(&obj) != nil || obj == nil {
		return nil, false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, false
	}
	return obj, true
}
func redactJSON(k string, v any) any {
	if secretKey(k) && v != nil && v != "" {
		return SecretMask
	}
	switch x := v.(type) {
	case map[string]any:
		for key, value := range x {
			x[key] = redactJSON(key, value)
		}
	case []any:
		for i, value := range x {
			x[i] = redactJSON("", value)
		}
	}
	return v
}
func redactValue(k, v string) string {
	if secretKey(k) && v != "" {
		return SecretMask
	}
	if strings.HasPrefix(k, "payment_") {
		if obj, ok := decodeConfig(v); ok {
			b, _ := json.Marshal(redactJSON("", obj))
			return string(b)
		}
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
func preserveJSON(next, old any) any {
	if text, ok := next.(string); ok && text == SecretMask {
		return old
	}
	switch n := next.(type) {
	case map[string]any:
		o, _ := old.(map[string]any)
		for k, v := range n {
			n[k] = preserveJSON(v, o[k])
		}
	case []any:
		o, _ := old.([]any)
		for i, v := range n {
			var prior any
			if i < len(o) {
				prior = o[i]
			}
			n[i] = preserveJSON(v, prior)
		}
	}
	return next
}
func preserveSecret(k, v, old string) string {
	if v == SecretMask {
		return old
	}
	if strings.HasPrefix(k, "payment_") {
		if next, ok := decodeConfig(v); ok {
			previous, _ := decodeConfig(old)
			b, _ := json.Marshal(preserveJSON(next, previous))
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
