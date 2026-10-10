package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalAvatarBoundsReencodeAndConfinement(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	a := NewLocalAvatar(root)
	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			var input bytes.Buffer
			im := image.NewRGBA(image.Rect(0, 0, 5, 7))
			if format == "png" {
				_ = png.Encode(&input, im)
			} else {
				_ = jpeg.Encode(&input, im, nil)
			}
			input.WriteString("untrusted trailing metadata")
			ref, err := a.Put(ctx, "owner", &input)
			if err != nil {
				t.Fatal(err)
			}
			r, _, kind, err := a.Open(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := io.ReadAll(r)
			r.Close()
			if kind != "image/png" || bytes.Contains(out, []byte("untrusted")) {
				t.Fatal("untrusted input survived reencoding")
			}
			pic, err := png.Decode(bytes.NewReader(out))
			if err != nil || pic.Bounds().Dx() != 5 || pic.Bounds().Dy() != 7 {
				t.Fatal("canonical image invalid")
			}
			if err = a.Delete(ctx, "other", ref); err == nil {
				t.Fatal("foreign owner deleted image")
			}
			if err = a.Delete(ctx, "owner", ref); err != nil {
				t.Fatal(err)
			}
		})
	}
	var huge bytes.Buffer
	_ = png.Encode(&huge, image.NewRGBA(image.Rect(0, 0, 4097, 1)))
	for _, bad := range [][]byte{[]byte("<svg/>"), []byte("PNG"), huge.Bytes(), bytes.Repeat([]byte{0}, AvatarMaxBytes+1)} {
		if _, err := a.Put(ctx, "owner", bytes.NewReader(bad)); err == nil {
			t.Fatal("invalid content accepted")
		}
	}
	names, _ := os.ReadDir(root)
	if len(names) != 0 {
		t.Fatal("rejected files left stored content")
	}
	for _, ref := range []string{AvatarURLPrefix + "../secret", AvatarURLPrefix + "%2e%2e/secret", "https://example.invalid/image.png", "/etc/passwd"} {
		if _, _, _, err := a.Open(ctx, ref); err == nil {
			t.Fatal("unconfined read")
		}
		if err := a.Delete(ctx, "owner", ref); err == nil {
			t.Fatal("unconfined delete")
		}
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	name := owner("owner") + "-" + strings.Repeat("a", 32) + ".png"
	if err := os.Symlink(outside, filepath.Join(root, name)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.Open(ctx, AvatarURLPrefix+name); err == nil {
		t.Fatal("avatar served symlink outside root")
	}
	if err := a.Delete(ctx, "owner", AvatarURLPrefix+name); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "private" {
		t.Fatal("delete followed symlink")
	}
}
func TestLocalAvatarFileFailureAndCancellation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "not-a-directory")
	_ = os.WriteFile(p, []byte("keep"), 0600)
	var imageBytes bytes.Buffer
	_ = png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if _, err := NewLocalAvatar(p).Put(context.Background(), "owner", bytes.NewReader(imageBytes.Bytes())); err == nil {
		t.Fatal("failed file write accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	if _, err := NewLocalAvatar(root).Put(ctx, "owner", bytes.NewReader(imageBytes.Bytes())); err == nil {
		t.Fatal("cancelled upload accepted")
	}
	files, _ := os.ReadDir(root)
	if len(files) != 0 {
		t.Fatal("cancelled upload wrote files")
	}
}

func TestLocalAvatarWebPAndDimensionBoundary(t *testing.T) {
	encoded, err := os.ReadFile("testdata/blue-purple-pink.lossless.webp.base64")
	if err != nil {
		t.Fatal(err)
	}
	input, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a := NewLocalAvatar(t.TempDir())
	ref, err := a.Put(ctx, "webp-owner", bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	r, _, kind, err := a.Open(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = png.Decode(r); err != nil || kind != "image/png" {
		t.Fatalf("WebP was not reencoded: %v", err)
	}
	var boundary bytes.Buffer
	_ = png.Encode(&boundary, image.NewRGBA(image.Rect(0, 0, 4096, 1)))
	if _, err = a.Put(ctx, "owner", &boundary); err != nil {
		t.Fatal("maximum dimension rejected")
	}
}
