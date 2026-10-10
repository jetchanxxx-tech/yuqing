// Package notification contains only safe acceptance metadata. Payloads and
// recipients never belong in durable receipts or application errors.
package notification

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

type Receipt struct {
	Provider   string    `json:"provider"`
	Purpose    string    `json:"purpose"`
	ProviderID string    `json:"provider_id"`
	State      string    `json:"state"`
	RecordedAt time.Time `json:"recorded_at"`
	AcceptedAt time.Time `json:"accepted_at"`
}

// Supplier IDs are always hashed: an otherwise syntactically valid ID could
// echo a code or recipient. Correlation stays stable without preserving echoes.
func Accepted(provider, id string) Receipt {
	sum := sha256.Sum256([]byte(id))
	return Receipt{Provider: provider, ProviderID: "sha256:" + hex.EncodeToString(sum[:]), State: "accepted", AcceptedAt: time.Now().UTC()}
}
func Normalize(r Receipt, purpose string) (Receipt, error) {
	switch r.Provider {
	case "resend", "aliyun", "tencent", "smtp", "custom":
	default:
		return Receipt{}, fmt.Errorf("invalid notification provider")
	}
	if r.State != "accepted" && r.State != "rejected" {
		return Receipt{}, fmt.Errorf("invalid notification state")
	}
	if r.State == "accepted" && r.AcceptedAt.IsZero() {
		return Receipt{}, fmt.Errorf("missing acceptance time")
	}
	if r.ProviderID != "" {
		if len(r.ProviderID) != 71 || r.ProviderID[:7] != "sha256:" {
			return Receipt{}, fmt.Errorf("invalid receipt reference")
		}
		if _, err := hex.DecodeString(r.ProviderID[7:]); err != nil {
			return Receipt{}, fmt.Errorf("invalid receipt reference")
		}
	}
	r.RecordedAt = time.Now().UTC()
	r.Purpose = purpose
	return r, nil
}
