//go:build linux

package storage

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
	"unsafe"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"golang.org/x/sys/unix"
)

func avatarSeal(uid string) string { return ".closure-" + owner(uid) + ".seal" }

// Both API uploads and the prebuilt cleanup CLI use the same persistent lock.
// Decode stays outside it; every actual file write is fenced after decoding.
func lockAvatarOwner(ctx context.Context, root *os.Root, uid string) (func(), error) {
	f, err := root.OpenFile(".closure-"+owner(uid)+".lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	for {
		if err = ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			f.Close()
			return nil, pkgerrors.ErrServiceUnavailable
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *LocalAvatar) Seal(ctx context.Context, uid string) error {
	if uid == "" {
		return pkgerrors.ErrBadRequest
	}
	r, err := a.root(true)
	if err != nil {
		return err
	}
	defer r.Close()
	release, err := lockAvatarOwner(ctx, r, uid)
	if err != nil {
		return err
	}
	defer release()
	f, err := r.OpenFile(avatarSeal(uid), os.O_CREATE|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	dir, err := r.Open(".")
	if err != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return pkgerrors.ErrServiceUnavailable
	}
	return nil
}

// The cursor is a Linux directory cookie, never a buffered os.File.ReadDir
// offset. Scan and deletion budgets are bounded; only sealed owned PNG names
// may be deleted. Directory records are parsed from the unbuffered syscall.
func (a *LocalAvatar) Reconcile(ctx context.Context, uid string, cursor int64, limit int) (int64, bool, int, error) {
	if uid == "" || cursor < 0 || limit < 1 || limit > 1000 {
		return cursor, false, 0, pkgerrors.ErrBadRequest
	}
	r, err := a.root(false)
	if err != nil {
		return cursor, false, 0, err
	}
	defer r.Close()
	release, err := lockAvatarOwner(ctx, r, uid)
	if err != nil {
		return cursor, false, 0, err
	}
	defer release()
	if info, err := r.Lstat(avatarSeal(uid)); err != nil || !info.Mode().IsRegular() {
		return cursor, false, 0, pkgerrors.ErrConflict
	}
	dir, err := r.Open(".")
	if err != nil {
		return cursor, false, 0, err
	}
	defer dir.Close()
	if _, err = unix.Seek(int(dir.Fd()), cursor, 0); err != nil {
		return cursor, false, 0, err
	}
	buf := make([]byte, 4096)
	scanned, removed := 0, 0
	for scanned < limit {
		if err = ctx.Err(); err != nil {
			return cursor, false, removed, err
		}
		n, readErr := unix.ReadDirent(int(dir.Fd()), buf)
		if readErr != nil {
			return cursor, false, removed, readErr
		}
		if n == 0 {
			return cursor, true, removed, nil
		}
		for start := 0; start < n; {
			offset := int(unsafe.Offsetof(unix.Dirent{}.Name))
			if n-start < offset {
				return cursor, false, removed, pkgerrors.ErrInternal
			}
			d := (*unix.Dirent)(unsafe.Pointer(&buf[start]))
			length := int(d.Reclen)
			if length <= offset || length > n-start {
				return cursor, false, removed, pkgerrors.ErrInternal
			}
			nameBytes := buf[start+offset : start+length]
			end := 0
			for end < len(nameBytes) && nameBytes[end] != 0 {
				end++
			}
			name := string(nameBytes[:end])
			if name != "." && name != ".." && d.Ino != 0 {
				if avatarName.MatchString(name) && strings.HasPrefix(name, owner(uid)+"-") {
					info, statErr := r.Lstat(name)
					if statErr != nil && !os.IsNotExist(statErr) {
						return cursor, false, removed, statErr
					}
					if statErr == nil && !info.Mode().IsRegular() {
						return cursor, false, removed, pkgerrors.ErrConflict
					}
					if statErr == nil {
						if err = r.Remove(name); err != nil && !os.IsNotExist(err) {
							return cursor, false, removed, err
						}
						removed++
					}
				}
				scanned++
			}
			cursor = d.Off
			start += length
			if scanned >= limit {
				return cursor, false, removed, nil
			}
		}
	}
	return cursor, false, removed, nil
}
