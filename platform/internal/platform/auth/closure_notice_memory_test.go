package auth

import (
	"context"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/notification"
)

func TestIdentityMemoryNoticeClaimPreservesDisabledSecurityButRejectsClosure(t *testing.T) {
	for _, status := range []string{"active", "disabled", "closure_pending", "closed"} {
		for _, issuer := range []bool{false, true} {
			t.Run(status+map[bool]string{false: "/owner", true: "/issuer"}[issuer], func(t *testing.T) {
				users := NewMemoryStore()
				users.usersByID["owner"] = &User{ID: "owner", Status: "active"}
				users.usersByID["issuer"] = &User{ID: "issuer", Status: "active"}
				uid := "owner"
				if issuer {
					uid = "issuer"
				}
				users.usersByID[uid].Status = status
				v := NewMemoryVerificationStore()
				v.users = users
				v.credentials["notice"] = VerificationCredential{ID: "notice", UserID: "owner", IssuerUserID: "issuer"}
				v.notices["notice"] = memoryIdentityNotice{IdentityNotice: IdentityNotice{ID: "notice", Recipient: "old@example.invalid"}, State: "failed", Next: time.Now().Add(-time.Second)}
				ctx := context.Background()
				n, err := v.ClaimIdentityNotice(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if status == "closure_pending" || status == "closed" {
					if n != nil || v.notices["notice"].State != "failed" {
						t.Fatalf("closure identity was newly dispatched: %+v", n)
					}
					return
				}
				if n == nil || n.Recipient != "old@example.invalid" {
					t.Fatalf("required security notice suppressed for %s: %+v", status, n)
				}
				if err := v.CompleteIdentityNotice(ctx, *n, notification.Accepted("custom", "sandbox-security-notice")); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
