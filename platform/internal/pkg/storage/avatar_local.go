package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"regexp"
	"strings"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
	_ "golang.org/x/image/webp"
)

// At most two decodes run in one process, bounding aggregate decompression
// memory as well as each image's encoded bytes and decoded pixel dimensions.
var avatarDecoders = make(chan struct{}, 2)
var avatarName = regexp.MustCompile(`^[a-f0-9]{64}-[a-f0-9]{32}\.png$`)

type LocalAvatar struct{ directory string }

// Initialization is lazy so constructing a service graph has no filesystem
// side effects. The application sets one persistent root at composition time.
func NewLocalAvatar(directory string) *LocalAvatar { return &LocalAvatar{directory: directory} }
func (a *LocalAvatar) root(create bool) (*os.Root, error) {
	if a.directory == "" {
		return nil, pkgerrors.ErrServiceUnavailable
	}
	if create {
		if err := os.MkdirAll(a.directory, 0700); err != nil {
			return nil, pkgerrors.ErrServiceUnavailable
		}
	}
	r, err := os.OpenRoot(a.directory)
	if err != nil {
		return nil, pkgerrors.ErrNotFound
	}
	return r, nil
}
func owner(uid string) string { h := sha256.Sum256([]byte(uid)); return hex.EncodeToString(h[:]) }
func object(reference string) (string, bool) {
	if !strings.HasPrefix(reference, AvatarURLPrefix) {
		return "", false
	}
	n := strings.TrimPrefix(reference, AvatarURLPrefix)
	return n, avatarName.MatchString(n)
}
func OwnsAvatarReference(uid, reference string) bool {
	name, ok := object(reference)
	return uid != "" && ok && strings.HasPrefix(name, owner(uid)+"-")
}
func (a *LocalAvatar) Put(ctx context.Context, uid string, reader io.Reader) (string, error) {
	if uid == "" || reader == nil {
		return "", pkgerrors.ErrBadRequest
	}
	select {
	case avatarDecoders <- struct{}{}:
		defer func() { <-avatarDecoders }()
	default:
		return "", pkgerrors.ErrQuotaExceeded
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(reader, AvatarMaxBytes+1))
	if err != nil {
		return "", pkgerrors.ErrBadRequest
	}
	if len(data) > AvatarMaxBytes {
		return "", pkgerrors.ErrBadRequest
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > AvatarMaxDimension || cfg.Height > AvatarMaxDimension || (format != "png" && format != "jpeg" && format != "webp") {
		return "", pkgerrors.ErrBadRequest
	}
	if format == "webp" && !validAvatarWebPFrames(data, cfg) {
		return "", pkgerrors.ErrBadRequest
	}
	picture, actual, err := image.Decode(bytes.NewReader(data))
	if err != nil || actual != format || picture == nil {
		return "", pkgerrors.ErrBadRequest
	}
	bounds := picture.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 || bounds.Dx() > AvatarMaxDimension || bounds.Dy() > AvatarMaxDimension || bounds.Dx() != cfg.Width || bounds.Dy() != cfg.Height {
		return "", pkgerrors.ErrBadRequest
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	root, err := a.root(true)
	if err != nil {
		return "", err
	}
	defer root.Close()
	release, err := lockAvatarOwner(ctx, root, uid)
	if err != nil {
		return "", err
	}
	defer release()
	if _, err = root.Lstat(avatarSeal(uid)); err == nil {
		return "", pkgerrors.ErrForbidden
	} else if !os.IsNotExist(err) {
		return "", pkgerrors.ErrServiceUnavailable
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return "", pkgerrors.ErrInternal
	}
	name := owner(uid) + "-" + hex.EncodeToString(random) + ".png"
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", pkgerrors.ErrServiceUnavailable
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			_ = root.Remove(name)
		}
	}()
	// Encode directly to disk: no unbounded encoded output buffer, no original
	// filename/metadata/polyglot bytes survive, WebP/JPEG become canonical PNG.
	if err = png.Encode(f, picture); err != nil {
		return "", pkgerrors.ErrServiceUnavailable
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", pkgerrors.ErrServiceUnavailable
	}
	if err = f.Close(); err != nil {
		return "", pkgerrors.ErrServiceUnavailable
	}
	keep = true
	return AvatarURLPrefix + name, nil
}
func (a *LocalAvatar) Delete(ctx context.Context, uid, reference string) error {
	name, ok := object(reference)
	if !ok || !strings.HasPrefix(name, owner(uid)+"-") {
		return pkgerrors.ErrForbidden
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := a.root(false)
	if err != nil {
		if errors.Is(err, pkgerrors.ErrNotFound) {
			return nil
		}
		return err
	}
	defer root.Close()
	err = root.Remove(name)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func (a *LocalAvatar) Open(ctx context.Context, reference string) (io.ReadCloser, int64, string, error) {
	name, ok := object(reference)
	if !ok {
		return nil, 0, "", pkgerrors.ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, "", err
	}
	root, err := a.root(false)
	if err != nil {
		return nil, 0, "", err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, "", pkgerrors.ErrNotFound
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, 0, "", pkgerrors.ErrNotFound
	}
	return f, info.Size(), "image/png", nil
}
