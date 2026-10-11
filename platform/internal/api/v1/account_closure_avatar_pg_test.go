package v1_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/platform/auth"
	"image"
	"image/png"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func closureImage(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func closureAvatarEnv(t *testing.T) (*billingActorPGEnv, string, map[string]any) {
	e := newBillingActorPGEnv(t)
	e.cfg.Storage.AvatarRoot = t.TempDir()
	e.rebuild()
	access, _, u := mustRegister(t, e.router, "closure-avatar@example.invalid", "Avatar owner")
	adminContractResponse(t, avatarHTTP(t, e, access, "avatar.png", closureImage(t)), 200)
	return e, access, u
}
func closureDue(t *testing.T, e *billingActorPGEnv, uid string) {
	k9Exec(t, e, `UPDATE account_closures SET requested_at=now()-interval '168 hours'-interval '1 second',withdraw_until=now()-interval '1 second' WHERE user_id=$1`, uid)
}
func closureImagesGone(t *testing.T, e *billingActorPGEnv) {
	t.Helper()
	files, err := os.ReadDir(e.cfg.Storage.AvatarRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".png") {
			t.Fatal("owned profile PII survived completed cleanup")
		}
	}
}

type closureLateReference struct {
	*auth.PGStore
	pool     *pgxpool.Pool
	ctx      context.Context
	pid      int
	finished chan error
}

func (s *closureLateReference) ReplaceOwnAvatar(_ context.Context, p auth.Principal, previous, next string) error {
	go func() { s.finished <- s.PGStore.ReplaceOwnAvatar(s.ctx, p, previous, next) }()
	for {
		var blocked bool
		if err := s.pool.QueryRow(s.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, s.pid).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return errors.New("fixture lost database response")
		}
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestClosurePGAmbiguousLateAvatarCommitIsFencedBeforeReconciliation(t *testing.T) {
	e, access, u := closureAvatarEnv(t)
	uid, tid := u["user_id"].(string), u["tenant_id"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := e.deps.Auth.Authenticate(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(context.Background())
	if _, err = gate.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, uid); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err = gate.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	store := &closureLateReference{PGStore: auth.NewPGStore(e.pool), pool: e.pool, ctx: ctx, pid: pid, finished: make(chan error, 1)}
	e.deps.Auth.EnableUserCenter(store, auth.NewPGVerificationStore(e.pool), nil, nil, "")
	if err = e.deps.Auth.UpdateAvatar(ctx, *p, bytes.NewReader(closureImage(t))); err == nil {
		t.Fatal("ambiguous transport failure disappeared")
	}
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		out <- doReq(t, e.router, "POST", "/api/v1/user/account-closure", access, map[string]any{"password": "password-123456", "confirmed": true, "close_tenant_ids": []string{tid}})
	}()
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-store.finished; err != nil {
		t.Fatal(err)
	}
	adminContractResponse(t, <-out, 202)
	var reference string
	if err = e.pool.QueryRow(ctx, `SELECT avatar_ref FROM account_closures WHERE user_id=$1`, uid).Scan(&reference); err != nil || reference == "" {
		t.Fatal("closure missed the eventual committed avatar reference")
	}
	closureDue(t, e, uid)
	r, err := e.deps.Closure.Process(ctx, uid, 100)
	if err != nil || r.State != "completed" {
		t.Fatalf("late reference cleanup: %+v %v", r, err)
	}
	closureImagesGone(t, e)
}

type closureWaitingReader struct {
	io.Reader
	entered, release chan struct{}
	once             bool
}

func (r *closureWaitingReader) Read(p []byte) (int, error) {
	if !r.once {
		r.once = true
		close(r.entered)
		<-r.release
	}
	return r.Reader.Read(p)
}
func TestClosurePGPreviouslyAdmittedAvatarReaderCannotWriteAfterCompletion(t *testing.T) {
	e, access, u := closureAvatarEnv(t)
	uid, tid := u["user_id"].(string), u["tenant_id"].(string)
	ctx := context.Background()
	p, err := e.deps.Auth.Authenticate(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	reader := &closureWaitingReader{Reader: bytes.NewReader(closureImage(t)), entered: make(chan struct{}), release: make(chan struct{})}
	out := make(chan error, 1)
	go func() { out <- e.deps.Auth.UpdateAvatar(ctx, *p, reader) }()
	<-reader.entered
	adminContractResponse(t, doReq(t, e.router, "POST", "/api/v1/user/account-closure", access, map[string]any{"password": "password-123456", "confirmed": true, "close_tenant_ids": []string{tid}}), 202)
	closureDue(t, e, uid)
	r, err := e.deps.Closure.Process(ctx, uid, 100)
	close(reader.release)
	writeErr := <-out
	if err != nil || r.State != "completed" || writeErr == nil {
		t.Fatalf("late avatar write survived: %+v %v %v", r, err, writeErr)
	}
	closureImagesGone(t, e)
}
