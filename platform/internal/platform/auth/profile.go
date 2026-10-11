package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"
	_ "time/tzdata" // prebuilt deployments need IANA data independent of the host

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

const DefaultTimezone = "Asia/Shanghai"

func validTimezone(zone string) bool {
	if zone == "" || zone == "Local" {
		return false
	}
	_, err := time.LoadLocation(zone)
	return err == nil
}
func profileTimezone(zone string) string {
	if !validTimezone(zone) {
		return DefaultTimezone
	}
	return zone
}

// ProfileStore checks the original authenticated identity in the same lock or
// statement as the write. Avatar replacement is CAS, including explicit removal.
// ReplaceOwnAvatar must mark only definitive non-commit errors with
// avatarWriteNotCommitted. All other failures may still commit after return.
type ProfileStore interface {
	UpdateOwnProfile(context.Context, Principal, string, string) error
	ReplaceOwnAvatar(context.Context, Principal, string, string) error
}

// avatarWriteNotCommitted is evidence from the write adapter, not a later read.
// Completed zero-row CAS and confirmed integrity rejections establish noncommit;
// connection errors, cancellation and unknown server failures do not.
type avatarWriteNotCommitted struct{ cause error }

func (e *avatarWriteNotCommitted) Error() string { return e.cause.Error() }
func (e *avatarWriteNotCommitted) Unwrap() error { return e.cause }

// AvatarStorage is an owned-object port; auth rules never use filesystem paths.
// Put validates and reencodes content, returns a new never-reused site reference.
// Delete must reject objects not owned by userID. Unknown legacy references are
// left untouched. Implementations must confine reads/writes to their object root.
type AvatarStorage interface {
	Put(context.Context, string, io.Reader) (string, error)
	Delete(context.Context, string, string) error
	Open(context.Context, string) (io.ReadCloser, int64, string, error)
}

func (s *Service) EnableAvatarStorage(storage AvatarStorage) { s.avatars = storage }

func (s *Service) UpdateAvatar(ctx context.Context, actor Principal, content io.Reader) error {
	if s.avatars == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	u, err := s.verificationUser(ctx, actor.UserID, []int64{actor.TokenVersion})
	if err != nil {
		return err
	}
	store, ok := s.userStore.(ProfileStore)
	if !ok {
		return pkgerrors.ErrServiceUnavailable
	}
	next, err := s.avatars.Put(ctx, actor.UserID, content)
	if err != nil {
		return err
	}
	if err = store.ReplaceOwnAvatar(ctx, actor, u.AvatarURL, next); err != nil {
		// An old reference read can race a still-running UPDATE. Retain both
		// objects for unknown finality; only the write adapter can prove reject.
		var rejected *avatarWriteNotCommitted
		if errors.As(err, &rejected) {
			s.cleanupAvatar(actor.UserID, next)
		}
		return err
	}
	s.cleanupAvatar(actor.UserID, u.AvatarURL)
	return nil
}
func (s *Service) RemoveAvatar(ctx context.Context, actor Principal) error {
	if s.avatars == nil {
		return pkgerrors.ErrServiceUnavailable
	}
	u, err := s.verificationUser(ctx, actor.UserID, []int64{actor.TokenVersion})
	if err != nil {
		return err
	}
	store, ok := s.userStore.(ProfileStore)
	if !ok {
		return pkgerrors.ErrServiceUnavailable
	}
	if err = store.ReplaceOwnAvatar(ctx, actor, u.AvatarURL, ""); err != nil {
		return err
	}
	s.cleanupAvatar(actor.UserID, u.AvatarURL)
	return nil
}
func (s *Service) cleanupAvatar(uid, reference string) {
	if reference == "" {
		return
	}
	// A bounded independent context permits cleanup after a disconnected client.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.avatars.Delete(ctx, uid, reference); err != nil {
		slog.Warn("avatar cleanup deferred; owned object retained")
	}
}
func (s *Service) OpenAvatar(ctx context.Context, reference string) (io.ReadCloser, int64, string, error) {
	if s.avatars == nil {
		return nil, 0, "", pkgerrors.ErrNotFound
	}
	return s.avatars.Open(ctx, reference)
}
