package auth

import (
	"context"
	"testing"
	"time"

	"github.com/yuqing/platform/internal/pkg/pgtest"
)

func TestIdentityPGEmailChangePersistsOldAddressNotice(t *testing.T) {
	pool := pgtest.Pool(t, "identity_notice")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "notice-owner", "old@example.invalid")
	v := NewPGVerificationStore(pool)
	c := verificationFixture("notice-owner", EmailChange, "new@example.invalid", "sandbox-email-change", time.Minute)
	if err := v.Issue(ctx, c); err != nil { t.Fatal(err) }
	if err := v.RecordDelivery(ctx, c.ID, true); err != nil { t.Fatal(err) }
	zero := int64(0)
	u, err := v.Consume(ctx, VerificationAttempt{Purpose: EmailChange, Hash: c.Hash, UserID: c.UserID, ExpectedVersion: &zero})
	if err != nil { t.Fatal(err) }
	if u.Email != "new@example.invalid" || u.TokenVersion != 1 { t.Fatal("email change did not commit") }
	var state, target string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(to_jsonb(v)->>'notice_state',''),COALESCE(to_jsonb(v)->>'notice_target','') FROM verification_tokens v WHERE id=$1`, c.ID).Scan(&state, &target); err != nil { t.Fatal(err) }
	if state != "pending" || target != "old@example.invalid" {
		t.Fatal("committed email change lost durable old-address notification intent")
	}
}
