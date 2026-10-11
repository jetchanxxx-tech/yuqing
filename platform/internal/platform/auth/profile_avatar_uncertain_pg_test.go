package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/storage"
)

// The fault is at the transport/store boundary, not the database: a real
// autocommit UPDATE remains blocked in PostgreSQL after the caller sees error.
type lateAvatarWrite struct {
	*PGStore
	writer        *PGStore
	observations  *pgxpool.Pool
	application   string
	writeContext  context.Context
	finished      chan error
	fault         error
	cancelRequest context.CancelFunc
	staged        string
}

func (s *lateAvatarWrite) ReplaceOwnAvatar(_ context.Context, actor Principal, previous, next string) error {
	s.staged = next
	go func() { s.finished <- s.writer.ReplaceOwnAvatar(s.writeContext, actor, previous, next) }()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var blocked bool
		if err := s.observations.QueryRow(s.writeContext, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, s.application).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			break
		}
		select {
		case <-tick.C:
		case <-s.writeContext.Done():
			return s.writeContext.Err()
		}
	}
	if s.cancelRequest != nil {
		s.cancelRequest()
	}
	return s.fault
}

func TestProfileAvatarPGAmbiguousLateCommitKeepsCurrentFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fault  error
		cancel bool
	}{{"transport", io.ErrUnexpectedEOF, false}, {"cancellation", context.Canceled, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			pool := pgtest.Pool(t, "avatar_late_commit")
			users := NewPGStore(pool)
			seedUCUserOnUserStore(t, users, "late-avatar", "late-avatar@example.invalid")
			files := storage.NewLocalAvatar(t.TempDir())
			svc := NewService(users, testSecret, "15m", "720h")
			svc.EnableUserCenter(users, nil, nil, nil, "")
			svc.EnableAvatarStorage(files)
			actor := Principal{UserID: "late-avatar", TokenVersion: 0}
			content := avatarFixture(t)
			if err := svc.UpdateAvatar(ctx, actor, bytes.NewReader(content)); err != nil {
				t.Fatal(err)
			}
			original, err := users.GetByID(ctx, actor.UserID)
			if err != nil {
				t.Fatal(err)
			}
			config := pool.Config()
			application := "k8-late-avatar-" + tc.name
			config.ConnConfig.RuntimeParams["application_name"] = application
			writerPool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer writerPool.Close()
			hold, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			requestContext, cancelRequest := context.WithCancel(ctx)
			defer cancelRequest()
			faultStore := &lateAvatarWrite{PGStore: users, writer: NewPGStore(writerPool), observations: pool, application: application, writeContext: ctx, finished: make(chan error, 1), fault: tc.fault}
			if tc.cancel {
				faultStore.cancelRequest = cancelRequest
			}
			joined := false
			defer func() {
				_ = hold.Rollback(context.Background())
				if !joined {
					cancel()
					select {
					case <-faultStore.finished:
					case <-time.After(time.Second):
					}
				}
			}()
			if _, err = hold.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, actor.UserID); err != nil {
				t.Fatal(err)
			}
			svc.EnableUserCenter(faultStore, nil, nil, nil, "")
			if err = svc.UpdateAvatar(requestContext, actor, bytes.NewReader(content)); !errors.Is(err, tc.fault) {
				t.Fatalf("fault not returned while UPDATE pending: %v", err)
			}
			before, err := users.GetByID(ctx, actor.UserID)
			if err != nil || before.AvatarURL != original.AvatarURL {
				t.Fatal("fixture did not expose old committed reference before release")
			}
			oldFile, _, _, err := files.Open(ctx, original.AvatarURL)
			if err != nil {
				t.Fatal("old avatar removed before write outcome")
			}
			oldFile.Close()
			if err = hold.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-faultStore.finished; err != nil {
				t.Fatalf("late real UPDATE did not commit: %v", err)
			}
			joined = true
			current, err := users.GetByID(ctx, actor.UserID)
			if err != nil || current.AvatarURL != faultStore.staged || current.AvatarURL == original.AvatarURL {
				t.Fatal("late commit did not install staged reference")
			}
			actual, _, _, openErr := files.Open(ctx, current.AvatarURL)
			t.Logf("old reference readable while original UPDATE blocked; late UPDATE committed; current object readable=%v", openErr == nil)
			if openErr != nil {
				t.Fatalf("ambiguous cleanup deleted late-committed current image: %v", openErr)
			}
			actual.Close()
		})
	}
}
