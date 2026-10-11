//go:build !linux

package storage

import (
	"context"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"os"
)

func avatarSeal(uid string) string                                      { return ".closure-" + owner(uid) + ".seal" }
func lockAvatarOwner(context.Context, *os.Root, string) (func(), error) { return func() {}, nil }

// Completion remains pending on platforms without the verified file fence.
func (*LocalAvatar) Seal(context.Context, string) error { return pkgerrors.ErrServiceUnavailable }
func (*LocalAvatar) Reconcile(context.Context, string, int64, int) (int64, bool, int, error) {
	return 0, false, 0, pkgerrors.ErrServiceUnavailable
}
