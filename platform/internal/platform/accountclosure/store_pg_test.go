package accountclosure

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/storage"
)

type completionPort interface {
	Process(context.Context, string, int) (*Status, error)
}

type avatarCompletionPort = interface {
	Seal(context.Context, string) error
	Reconcile(context.Context, string, int64, int) (int64, bool, int, error)
}
type configuredCompletionPort interface {
	completionPort
	SetAvatarStorage(avatarCompletionPort)
}

func TestClosurePGResumesAnonymizationAndRetainsFinancialAndSharedAssets(t *testing.T) {
	pool := pgtest.Pool(t, "closure_completion")
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,name,phone) VALUES('closing','closing@example.invalid','original','Private Owner','13900006999'),('other','other@example.invalid','other','Other Owner',NULL);
INSERT INTO tenants(id,name,slug,db_name,status) VALUES('sole','Private Team','sole','sole','active'),('shared','Shared Team','shared','shared','active');
INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('sole','closing','tenant_admin'),('shared','closing','analyst'),('shared','other','tenant_admin');
INSERT INTO report_credits(tenant_id,balance,plan_code) VALUES('sole',3,'free'),('shared',8,'pro');
INSERT INTO credit_transactions(id,tenant_id,delta,reason,balance_after,reason_detail,actor_id,idempotency_key,expected_version,version) VALUES('financial-fact','sole',3,'admin_adjust',3,'Owner closing@example.invalid','closing','private-key',0,1);
INSERT INTO audit_logs(actor_id,tenant_id,action,resource,details_json) VALUES('closing','sole','credit.adjust','sole','{"reason":"closing@example.invalid","before":{"balance":0},"after":{"balance":3},"delta":3}');
INSERT INTO verification_tokens(id,user_id,type,purpose,target,expires_at,used_at,notice_target,notice_state,notice_next_attempt) VALUES('old-address','closing','email_change','email_change','new@example.invalid',now(),now(),'closing@example.invalid','failed',now());
INSERT INTO account_notification_attempts(id,user_id,actor_id,actor_version,purpose,state) VALUES('attempt','closing','other',0,'password_reset','pending')`)
	if err != nil {
		t.Fatal(err)
	}
	avatar := storage.NewLocalAvatar(t.TempDir())
	var pngBytes bytes.Buffer
	if err = png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	ref, err := avatar.Put(ctx, "closing", bytes.NewReader(pngBytes.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	otherRef, err := avatar.Put(ctx, "other", bytes.NewReader(pngBytes.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET avatar_url=$1 WHERE id='closing'`, ref); err != nil {
		t.Fatal(err)
	}
	s := NewPGStore(pool)
	if _, err = s.Request(ctx, Actor{UserID: "closing", PasswordHash: "original"}, []string{"sole"}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	p, ok := any(s).(configuredCompletionPort)
	if !ok {
		t.Fatal("configured resumable closure execution is unavailable")
	}
	real, ok := any(avatar).(avatarCompletionPort)
	if !ok {
		t.Fatal("avatar cleanup unavailable")
	}
	p.SetAvatarStorage(real)
	blocked, blockErr := p.Process(ctx, "closing", 1)
	if blockErr != nil || blocked.State != "pending" || blocked.LastError != "NOTIFICATION_DELIVERY_UNCONFIRMED" {
		t.Fatalf("unconfirmed dispatch was discarded: %+v %v", blocked, blockErr)
	}
	if _, err = pool.Exec(ctx, `UPDATE account_notification_attempts SET state='failed' WHERE id='attempt'`); err != nil {
		t.Fatal(err)
	}
	p.SetAvatarStorage(&brokenClosureStorage{})
	r, err := p.Process(ctx, "closing", 1)
	if err != nil || r.State != "finalizing" || r.LastError != "AVATAR_CLEANUP_FAILED" {
		t.Fatalf("failed cleanup claimed completion: %+v %v", r, err)
	}
	var email string
	if err = pool.QueryRow(ctx, `SELECT email FROM users WHERE id='closing'`).Scan(&email); err != nil || email != "closing@example.invalid" {
		t.Fatalf("identity released before cleanup: %s %v", email, err)
	}
	p.SetAvatarStorage(real)
	for i := 0; i < 100; i++ {
		r, err = p.Process(ctx, "closing", 1)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "completed" {
			break
		}
	}
	if r.State != "completed" {
		t.Fatalf("retry did not finish: %+v", r)
	}
	var status, name string
	var phone *string
	if err = pool.QueryRow(ctx, `SELECT email,status,name,phone FROM users WHERE id='closing'`).Scan(&email, &status, &name, &phone); err != nil || email == "closing@example.invalid" || status != "closed" || name != "已注销用户" || phone != nil {
		t.Fatalf("identity not anonymized: %s %s %s %v %v", email, status, name, phone, err)
	}
	var details string
	var delta, balance int
	if err = pool.QueryRow(ctx, `SELECT delta,balance_after,reason_detail FROM credit_transactions WHERE id='financial-fact'`).Scan(&delta, &balance, &details); err != nil || delta != 3 || balance != 3 || strings.Contains(details, "@") {
		t.Fatalf("financial facts/privacy: %d %d %s %v", delta, balance, details, err)
	}
	if err = pool.QueryRow(ctx, `SELECT status,name FROM tenants WHERE id='shared'`).Scan(&status, &name); err != nil || status != "active" || name != "Shared Team" {
		t.Fatal("other members' tenant changed")
	}
	f, _, _, err := avatar.Open(ctx, otherRef)
	if err != nil {
		t.Fatal("other avatar lost")
	}
	f.Close()
	var targets int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM verification_tokens WHERE user_id='closing' AND (target<>'' OR notice_target<>'' OR notice_state IN ('pending','failed','processing'))`).Scan(&targets); err != nil || targets != 0 {
		t.Fatal("closed identity retained outbound recipients")
	}
	if _, err = p.Process(ctx, "closing", 1); err != nil {
		t.Fatal("completed retry failed")
	}
}

type brokenClosureStorage struct{}

func (*brokenClosureStorage) Seal(context.Context, string) error {
	return errors.New("fixture disk failure")
}
func (*brokenClosureStorage) Reconcile(context.Context, string, int64, int) (int64, bool, int, error) {
	return 0, false, 0, errors.New("fixture disk failure")
}

func TestClosurePGExecutionWaitsAndRechecksFreshBlockers(t *testing.T) {
	pool := pgtest.Pool(t, "accountclosure")
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,name) VALUES('closing','closing@example.invalid','original','Owner');
INSERT INTO tenants(id,name,slug,db_name,status) VALUES('sole','Sole','sole','sole','active');
INSERT INTO tenant_members(tenant_id,user_id,role) VALUES('sole','closing','tenant_admin');
INSERT INTO orders(id,tenant_id,sku_code,kind,credits,amount_cents,channel,state,expires_at) VALUES('old-order','sole','free','plan',1,1,'test','closed',now()+interval '1 hour')`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewPGStore(pool)
	if _, err = s.Request(ctx, Actor{UserID: "closing", PasswordHash: "original"}, []string{"sole"}); err != nil {
		t.Fatal(err)
	}
	p, ok := any(s).(completionPort)
	if !ok {
		t.Fatal("resumable closure execution is unavailable")
	}
	r, err := p.Process(ctx, "closing", 1)
	if err != nil || r.State != "pending" {
		t.Fatalf("early execution: %+v %v", r, err)
	}
	_, err = pool.Exec(ctx, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second'; UPDATE orders SET state='refund_needed' WHERE id='old-order'`)
	if err != nil {
		t.Fatal(err)
	}
	r, err = p.Process(ctx, "closing", 1)
	if err != nil || r.State != "pending" || r.LastError != "UNSETTLED_ORDERS" {
		t.Fatalf("fresh blocker: %+v %v", r, err)
	}
	var status string
	if err = pool.QueryRow(ctx, `SELECT status FROM users WHERE id='closing'`).Scan(&status); err != nil || status != "closure_pending" {
		t.Fatalf("blocked identity: %s %v", status, err)
	}
	if _, err = s.Cancel(ctx, Actor{UserID: "closing", Version: 1, PasswordHash: "original"}); err == nil {
		t.Fatal("expired withdrawal succeeded")
	}
}
