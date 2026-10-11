package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yuqing/platform/internal/pkg/pgtest"
	"github.com/yuqing/platform/internal/pkg/storage"
)

// Persistent ownership locks are control records, not avatar content. Ignore
// only this exact owner's empty regular lock file; unexpected files still count.
func avatarImageEntries(root, uid string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(uid))
	name := fmt.Sprintf(".closure-%x.lock", hash)
	out := entries[:0]
	for _, entry := range entries {
		if entry.Name() == name {
			info, e := entry.Info()
			if e == nil && info.Mode().IsRegular() && info.Size() == 0 {
				continue
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

func avatarFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 5, 7))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestProfileAvatarPGReferenceRollbackAndPersistence(t *testing.T) {
	pool := pgtest.Pool(t, "avatar_reference")
	users := NewPGStore(pool)
	ctx := context.Background()
	seedUCUserOnUserStore(t, users, "avatar-owner", "avatar-owner@example.invalid")
	root := t.TempDir()
	files := storage.NewLocalAvatar(root)
	s := NewService(users, testSecret, "15m", "720h")
	s.EnableUserCenter(users, nil, nil, nil, "")
	s.EnableAvatarStorage(files)
	actor := Principal{UserID: "avatar-owner", TokenVersion: 0}
	if err := s.UpdateAvatar(ctx, actor, bytes.NewReader(avatarFixture(t))); err != nil {
		t.Fatal(err)
	}
	old, _ := users.GetByID(ctx, actor.UserID)
	if old.AvatarURL == "" {
		t.Fatal("reference absent")
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE users ADD CONSTRAINT freeze_avatar CHECK (row_version<=1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAvatar(ctx, actor, bytes.NewReader(avatarFixture(t))); err == nil {
		t.Fatal("database failure accepted")
	}
	after, _ := NewPGStore(pool).GetByID(ctx, actor.UserID)
	if after.AvatarURL != old.AvatarURL {
		t.Fatal("failed DB write replaced reference")
	}
	objects, err := avatarImageEntries(root, actor.UserID)
	if err != nil || len(objects) != 1 {
		t.Fatalf("new file cleanup=%d err=%v", len(objects), err)
	}
	r, _, _, err := files.Open(ctx, old.AvatarURL)
	if err != nil {
		t.Fatal("old avatar lost")
	}
	r.Close()
	if _, err = pool.Exec(ctx, `ALTER TABLE users DROP CONSTRAINT freeze_avatar`); err != nil {
		t.Fatal(err)
	}
	// File write failure must also preserve the committed reference and image.
	blocked := filepath.Join(t.TempDir(), "regular-file")
	_ = os.WriteFile(blocked, []byte("keep"), 0600)
	s.EnableAvatarStorage(storage.NewLocalAvatar(blocked))
	if err = s.UpdateAvatar(ctx, actor, bytes.NewReader(avatarFixture(t))); err == nil {
		t.Fatal("file failure accepted")
	}
	after, _ = users.GetByID(ctx, actor.UserID)
	if after.AvatarURL != old.AvatarURL {
		t.Fatal("file failure changed reference")
	}
	s.EnableAvatarStorage(files)
	if err = s.UpdateAvatar(ctx, actor, bytes.NewReader(avatarFixture(t))); err != nil {
		t.Fatal(err)
	}
	if r, _, _, err := files.Open(ctx, old.AvatarURL); err == nil {
		r.Close()
		t.Fatal("successful replacement did not reclaim old image")
	}
	objects, _ = avatarImageEntries(root, actor.UserID)
	if len(objects) != 1 {
		t.Fatal("successful replacement lost current image")
	}
	// Reconstruction proves both the persistent DB reference and object storage.
	s = NewService(NewPGStore(pool), testSecret, "15m", "720h")
	s.EnableUserCenter(NewPGStore(pool), nil, nil, nil, "")
	s.EnableAvatarStorage(storage.NewLocalAvatar(root))
	if err = s.RemoveAvatar(ctx, actor); err != nil {
		t.Fatal(err)
	}
	after, _ = users.GetByID(ctx, actor.UserID)
	if after.AvatarURL != "" {
		t.Fatal("explicit deletion skipped empty string")
	}
	objects, _ = avatarImageEntries(root, actor.UserID)
	if len(objects) != 0 {
		t.Fatal("old object was not reclaimed")
	}
}
func TestProfileAvatarPGQueuedInvalidation(t *testing.T) {
	for _, action := range []string{"password", "disabled", "closure_pending"} {
		for _, mutation := range []string{"upload", "remove", "profile"} {
			t.Run(action+"/"+mutation, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				pool := pgtest.Pool(t, "avatar_queued")
				users := NewPGStore(pool)
				seedUCUserOnUserStore(t, users, "avatar-queued", "avatar-queued@example.invalid")
				cfg := pool.Config()
				cfg.ConnConfig.RuntimeParams["application_name"] = "k8-queued-" + action + mutation
				queued, err := pgxpool.NewWithConfig(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer queued.Close()
				root := t.TempDir()
				files := storage.NewLocalAvatar(root)
				s := NewService(NewPGStore(queued), testSecret, "15m", "720h")
				s.EnableUserCenter(NewPGStore(queued), nil, nil, nil, "")
				s.EnableAvatarStorage(files)
				actor := Principal{UserID: "avatar-queued", TokenVersion: 0}
				content := avatarFixture(t)
				if err = s.UpdateAvatar(ctx, actor, bytes.NewReader(content)); err != nil {
					t.Fatal(err)
				}
				old, _ := users.GetByID(ctx, actor.UserID)
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(context.Background())
				if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, actor.UserID); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					switch mutation {
					case "upload":
						done <- s.UpdateAvatar(ctx, actor, bytes.NewReader(content))
					case "remove":
						done <- s.RemoveAvatar(ctx, actor)
					case "profile":
						done <- s.UpdateProfile(ctx, actor.UserID, "不可更新", "America/New_York", actor.TokenVersion)
					}
				}()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var blocked bool
					if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, cfg.ConnConfig.RuntimeParams["application_name"]).Scan(&blocked); err != nil {
						t.Fatal(err)
					}
					if blocked {
						break
					}
					select {
					case <-ticker.C:
					case <-ctx.Done():
						t.Fatal("mutation never queued")
					}
				}
				status := "active"
				if action != "password" {
					status = action
				}
				if _, err = tx.Exec(ctx, `UPDATE users SET status=$2,token_version=token_version+1 WHERE id=$1`, actor.UserID, status); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				if err = <-done; err == nil {
					t.Fatal("queued mutation adopted higher actor version")
				}
				after, _ := users.GetByID(ctx, actor.UserID)
				if after.AvatarURL != old.AvatarURL || after.Name != old.Name || after.Timezone != old.Timezone {
					t.Fatal("revoked actor changed profile")
				}
				objects, _ := avatarImageEntries(root, actor.UserID)
				if len(objects) != 1 {
					t.Fatalf("queued failed write retained new object count=%d", len(objects))
				}
				r, _, _, err := files.Open(ctx, old.AvatarURL)
				if err != nil {
					t.Fatal("queued failure deleted old image")
				}
				r.Close()
			})
		}
	}
}

// Hold both storage calls after each has read the same DB reference. The real
// CAS must pick one winner; cleanup must preserve that winner's new object.
type simultaneousAvatar struct {
	AvatarStorage
	ready   chan struct{}
	release chan struct{}
}

func (s *simultaneousAvatar) Put(ctx context.Context, uid string, r io.Reader) (string, error) {
	ref, err := s.AvatarStorage.Put(ctx, uid, r)
	if err != nil {
		return "", err
	}
	s.ready <- struct{}{}
	select {
	case <-s.release:
		return ref, nil
	case <-ctx.Done():
		return ref, ctx.Err()
	}
}
func TestProfileAvatarPGConcurrentReplacementKeepsWinner(t *testing.T) {
	pool := pgtest.Pool(t, "avatar_race")
	users := NewPGStore(pool)
	seedUCUserOnUserStore(t, users, "avatar-race", "avatar-race@example.invalid")
	root := t.TempDir()
	files := storage.NewLocalAvatar(root)
	s := NewService(users, testSecret, "15m", "720h")
	s.EnableUserCenter(users, nil, nil, nil, "")
	s.EnableAvatarStorage(files)
	actor := Principal{UserID: "avatar-race", TokenVersion: 0}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	content := avatarFixture(t)
	if err := s.UpdateAvatar(ctx, actor, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	barrier := &simultaneousAvatar{AvatarStorage: files, ready: make(chan struct{}, 2), release: make(chan struct{})}
	s.EnableAvatarStorage(barrier)
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { out <- s.UpdateAvatar(ctx, actor, bytes.NewReader(content)) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-barrier.ready:
		case <-ctx.Done():
			t.Fatal("uploads never reached CAS barrier")
		}
	}
	close(barrier.release)
	success := 0
	for i := 0; i < 2; i++ {
		if err := <-out; err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("same-reference replacement winners=%d", success)
	}
	u, err := users.GetByID(ctx, actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, err := files.Open(ctx, u.AvatarURL)
	if err != nil {
		t.Fatal("cleanup deleted winning replacement")
	}
	r.Close()
	objects, _ := avatarImageEntries(root, actor.UserID)
	if len(objects) != 1 {
		t.Fatalf("winner-only object count=%d", len(objects))
	}
}
