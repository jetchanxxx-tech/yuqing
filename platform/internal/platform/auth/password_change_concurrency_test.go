package auth

import (
	"context"
	"testing"
	"time"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"github.com/yuqing/platform/internal/pkg/pgtest"
)

// passwordSnapshotBarrier holds both requests after the real store returned
// their old credential snapshot. It does not replace password verification or
// password updates, so the test exposes a missing CAS in the production store.
type passwordSnapshotBarrier struct {
	UserStore
	read    chan *User
	release chan struct{}
}

func (s *passwordSnapshotBarrier) GetByID(ctx context.Context, userID string) (*User, error) {
	u, err := s.UserStore.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	select {
	case s.read <- u:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-s.release:
		return u, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestPasswordChangeConcurrentPGOnlyOneOldPasswordRequestSucceeds(t *testing.T) {
	for _, driver := range []string{"memory", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var store Store = NewMemoryStore()
			if driver == "postgres" {
				store = NewPGStore(pgtest.Pool(t, "password_change_concurrent"))
			}
			svc := NewService(store, testSecret, "15m", "720h")
			p, oldPair := mustRegister(t, svc, "concurrent-password@example.com", testPassword, "ConcurrentPassword")
			users := store.(UserStore)
			barrier := &passwordSnapshotBarrier{
				UserStore: users, read: make(chan *User, 2), release: make(chan struct{}),
			}
			svc.EnableUserCenter(barrier, NewMemoryVerificationStore(), nil, nil, "https://test.example.com")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			type changeResult struct {
				password string
				err      error
			}
			results := make(chan changeResult, 2)
			for _, password := range []string{"first-password-1", "second-password-2"} {
				go func(password string) {
					results <- changeResult{password: password, err: svc.ChangePassword(ctx, p.UserID, testPassword, password)}
				}(password)
			}
			for range 2 {
				select {
				case snapshot := <-barrier.read:
					if snapshot.TokenVersion != 0 || snapshot.Status != "active" || !VerifyPassword(snapshot.PasswordHash, testPassword) {
						close(barrier.release)
						t.Fatalf("request did not read the original active credential: status=%s token_version=%d", snapshot.Status, snapshot.TokenVersion)
					}
				case <-ctx.Done():
					close(barrier.release)
					t.Fatalf("both password requests must read before either writes: %v", ctx.Err())
				}
			}
			close(barrier.release)
			var successes []string
			conflicts := 0
			for range 2 {
				select {
				case result := <-results:
					if result.err == nil {
						successes = append(successes, result.password)
					} else if pkgerrors.Is(result.err, pkgerrors.ErrConflict) {
						conflicts++
					} else {
						t.Errorf("concurrent password update must return a version conflict: %v", result.err)
					}
				case <-ctx.Done():
					t.Fatalf("concurrent password requests did not finish: %v", ctx.Err())
				}
			}
			if len(successes) != 1 || conflicts != 1 {
				t.Errorf("two requests verified the same old password: successes=%d conflicts=%d, want exactly one of each", len(successes), conflicts)
			}
			current, err := users.GetByID(ctx, p.UserID)
			if err != nil {
				t.Fatal(err)
			}
			if current.TokenVersion != 1 || current.RowVersion != 1 {
				t.Errorf("one password change must increment versions once: token=%d row=%d", current.TokenVersion, current.RowVersion)
			}
			if _, err := svc.Authenticate(ctx, oldPair.AccessToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
				t.Errorf("old access token must be revoked: %v", err)
			}
			if _, err := svc.Refresh(ctx, oldPair.RefreshToken); !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
				t.Errorf("old refresh token must be revoked: %v", err)
			}
			if len(successes) == 1 {
				if !VerifyPassword(current.PasswordHash, successes[0]) {
					t.Error("stored password does not match the sole successful request")
				}
				_, fresh, err := svc.Login(ctx, p.Email, successes[0])
				if err != nil {
					t.Fatalf("sole successful password must permit login: %v", err)
				}
				if _, err := svc.Authenticate(ctx, fresh.AccessToken); err != nil {
					t.Fatalf("new token must authenticate: %v", err)
				}
			}
		})
	}
}

func TestPasswordChangeConcurrentPGDisabledAfterSnapshotCannotUpdate(t *testing.T) {
	for _, driver := range []string{"memory", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var store Store = NewMemoryStore()
			if driver == "postgres" {
				store = NewPGStore(pgtest.Pool(t, "password_change_disabled"))
			}
			svc := NewService(store, testSecret, "15m", "720h")
			p, _ := mustRegister(t, svc, "disabled-after-snapshot@example.com", testPassword, "DisabledAfterSnapshot")
			users := store.(UserStore)
			barrier := &passwordSnapshotBarrier{
				UserStore: users, read: make(chan *User, 1), release: make(chan struct{}),
			}
			svc.EnableUserCenter(barrier, NewMemoryVerificationStore(), nil, nil, "https://test.example.com")
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- svc.ChangePassword(ctx, p.UserID, testPassword, "disabled-password-2") }()
			select {
			case <-barrier.read:
			case <-ctx.Done():
				close(barrier.release)
				t.Fatalf("password request did not read its original snapshot: %v", ctx.Err())
			}
			if pgStore, ok := store.(*PGStore); ok {
				if _, err := pgStore.pool.Exec(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, p.UserID); err != nil {
					close(barrier.release)
					t.Fatal(err)
				}
			} else {
				memory := store.(*MemoryStore)
				memory.mu.Lock()
				memory.usersByID[p.UserID].Status = "disabled"
				memory.mu.Unlock()
			}
			close(barrier.release)
			select {
			case err := <-result:
				if !pkgerrors.Is(err, pkgerrors.ErrUnauthorized) {
					t.Errorf("disabled account must reject an in-flight password change: %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("password request did not finish: %v", ctx.Err())
			}
			current, err := users.GetByID(ctx, p.UserID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status != "disabled" || current.TokenVersion != 0 || current.RowVersion != 0 || current.PasswordChangedAt != nil || !VerifyPassword(current.PasswordHash, testPassword) {
				t.Errorf("disabled account's password or version changed: status=%s token=%d row=%d", current.Status, current.TokenVersion, current.RowVersion)
			}
		})
	}
}
