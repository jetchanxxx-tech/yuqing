package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	"io"
	"os"
)

// Identity follows the persistent private volume across process reconstruction.
// A newly created empty replacement root has a different identity and cannot
// satisfy a closure captured against the server's original owned-object root.
func (a *LocalAvatar) RootIdentity(ctx context.Context) (string, error) {
	r, err := a.root(true)
	if err != nil {
		return "", err
	}
	defer r.Close()
	release, err := lockAvatarOwner(ctx, r, "storage-root-identity")
	if err != nil {
		return "", err
	}
	defer release()
	if info, err := r.Lstat(".avatar-root-id"); err == nil && !info.Mode().IsRegular() {
		return "", pkgerrors.ErrServiceUnavailable
	} else if err != nil && !os.IsNotExist(err) {
		return "", pkgerrors.ErrServiceUnavailable
	}
	f, err := r.OpenFile(".avatar-root-id", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return "", pkgerrors.ErrServiceUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", pkgerrors.ErrServiceUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, 33))
	if err != nil {
		return "", pkgerrors.ErrServiceUnavailable
	}
	if len(data) == 0 {
		random := make([]byte, 16)
		if _, err = rand.Read(random); err != nil {
			return "", err
		}
		data = []byte(hex.EncodeToString(random))
		if _, err = f.Write(data); err != nil {
			return "", err
		}
		if err = f.Sync(); err != nil {
			return "", err
		}
		d, err := r.Open(".")
		if err != nil {
			return "", err
		}
		err = d.Sync()
		d.Close()
		if err != nil {
			return "", err
		}
	}
	if len(data) != 32 {
		return "", pkgerrors.ErrServiceUnavailable
	}
	if _, err = hex.DecodeString(string(data)); err != nil {
		return "", pkgerrors.ErrServiceUnavailable
	}
	return string(data), nil
}
