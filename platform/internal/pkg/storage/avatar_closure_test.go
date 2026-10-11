package storage

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"os"
	"testing"

	pkgerrors "github.com/yuqing/platform/internal/pkg/errors"
)

type ownedClosureStorage interface {
	Seal(context.Context, string) error
	Reconcile(context.Context, string, int64, int) (int64, bool, int, error)
}

func closurePNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func lifecycle(t *testing.T, a *LocalAvatar) ownedClosureStorage {
	t.Helper()
	port, ok := any(a).(ownedClosureStorage)
	if !ok {
		t.Fatal("owned avatar completion seal and bounded reconciliation are unavailable")
	}
	return port
}
func TestAvatarClosureSealAndBoundedOwnershipReconciliation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	adapter := NewLocalAvatar(root)
	data := closurePNG(t)
	for i := 0; i < 4; i++ {
		if _, err := adapter.Put(ctx, "closing-owner", bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	other, err := adapter.Put(ctx, "other-owner", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(root+"/unknown.txt", []byte("preserve unknown ownership"), 0600); err != nil {
		t.Fatal(err)
	}
	port := lifecycle(t, adapter)
	if err = port.Seal(ctx, "closing-owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = NewLocalAvatar(root).Put(ctx, "closing-owner", bytes.NewReader(data)); !pkgerrors.Is(err, pkgerrors.ErrForbidden) {
		t.Fatalf("sealed owner wrote through a reconstructed adapter: %v", err)
	}
	var cursor int64
	removed := 0
	done := false
	for i := 0; i < 100 && !done; i++ {
		var n int
		cursor, done, n, err = port.Reconcile(ctx, "closing-owner", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if n > 1 {
			t.Fatal("reconciliation exceeded its deletion budget")
		}
		removed += n
	}
	if !done || removed != 4 {
		t.Fatalf("bounded inventory missed retained owned objects: done=%v removed=%d", done, removed)
	}
	f, _, _, err := adapter.Open(ctx, other)
	if err != nil {
		t.Fatal("other owner's live object was removed")
	}
	f.Close()
	if _, err = os.Stat(root + "/unknown.txt"); err != nil {
		t.Fatal("unknown object was removed")
	}
}

type closureBlockedReader struct {
	reader  io.Reader
	entered chan struct{}
	release chan struct{}
	blocked bool
}

func (r *closureBlockedReader) Read(p []byte) (int, error) {
	if !r.blocked {
		r.blocked = true
		close(r.entered)
		<-r.release
	}
	return r.reader.Read(p)
}
func TestAvatarClosureFenceStopsPreviouslyAdmittedLateFileCreation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	adapter := NewLocalAvatar(root)
	port := lifecycle(t, adapter)
	reader := &closureBlockedReader{reader: bytes.NewReader(closurePNG(t)), entered: make(chan struct{}), release: make(chan struct{})}
	out := make(chan error, 1)
	go func() { _, err := adapter.Put(ctx, "late-owner", reader); out <- err }()
	<-reader.entered
	err := port.Seal(ctx, "late-owner")
	close(reader.release)
	writeErr := <-out
	if err != nil {
		t.Fatal(err)
	}
	if !pkgerrors.Is(writeErr, pkgerrors.ErrForbidden) {
		t.Fatalf("pre-admitted write survived completion fence: %v", writeErr)
	}
	_, done, removed, err := port.Reconcile(ctx, "late-owner", 0, 10)
	if err != nil || !done || removed != 0 {
		t.Fatalf("late writer left PII after seal: %d %v %v", removed, done, err)
	}
}
