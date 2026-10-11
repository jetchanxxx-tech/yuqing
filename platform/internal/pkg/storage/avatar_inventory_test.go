package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAvatarClosureLargeDirectoryCookiesDoNotSkipOwnedFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a := NewLocalAvatar(root)
	for i := 0; i < 600; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%s-%032x.png", owner("closing"), i)), []byte("owned prior object"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := lifecycle(t, a)
	if err := p.Seal(ctx, "closing"); err != nil {
		t.Fatal(err)
	}
	var cursor int64
	total := 0
	done := false
	for pages := 0; pages < 1000 && !done; pages++ {
		next, finished, n, err := p.Reconcile(ctx, "closing", cursor, 53)
		if err != nil {
			t.Fatal(err)
		}
		if n > 53 {
			t.Fatal("deletion budget exceeded")
		}
		cursor = next
		done = finished
		total += n
	}
	if !done || total != 600 {
		t.Fatalf("large unbuffered inventory: done=%v removed=%d", done, total)
	}
}
