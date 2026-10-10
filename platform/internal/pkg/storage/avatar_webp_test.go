package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

// A single-symbol lossless image uses no pixel codes, irrespective of width.
// The maximum decoded fixture is 4097x1 (~16 KiB), not a memory/load probe.
type webpFixtureBits struct {
	data []byte
	bit  uint
}

func (b *webpFixtureBits) put(value uint32, n uint) {
	for i := uint(0); i < n; i++ {
		if b.bit%8 == 0 {
			b.data = append(b.data, 0)
		}
		b.data[b.bit/8] |= byte((value>>i)&1) << (b.bit % 8)
		b.bit++
	}
}
func solidVP8L(width, height int) []byte {
	data := make([]byte, 5)
	data[0] = 0x2f
	binary.LittleEndian.PutUint32(data[1:], uint32(width-1)|uint32(height-1)<<14)
	b := webpFixtureBits{}
	b.put(0, 1)
	b.put(0, 1)
	b.put(0, 1) // no transform/cache/meta image
	for _, symbol := range []uint32{0, 0, 0, 255, 0} {
		b.put(1, 1)
		b.put(0, 1) // simple tree, one symbol
		if symbol < 2 {
			b.put(0, 1)
			b.put(symbol, 1)
		} else {
			b.put(1, 1)
			b.put(symbol, 8)
		}
	}
	data = append(data, b.data...)
	return append(data, make([]byte, 8)...)
}
func webpChunk(kind string, data []byte) []byte {
	chunk := make([]byte, 8)
	copy(chunk, kind)
	binary.LittleEndian.PutUint32(chunk[4:], uint32(len(data)))
	chunk = append(chunk, data...)
	if len(data)%2 != 0 {
		chunk = append(chunk, 0)
	}
	return chunk
}
func webpWithFrame(kind string, frame []byte, canvasWidth, canvasHeight int) []byte {
	content := []byte("WEBP")
	if canvasWidth > 0 {
		canvas := make([]byte, 10)
		w, h := canvasWidth-1, canvasHeight-1
		for i := 0; i < 3; i++ {
			canvas[4+i] = byte(w >> (8 * i))
			canvas[7+i] = byte(h >> (8 * i))
		}
		content = append(content, webpChunk("VP8X", canvas)...)
	}
	content = append(content, webpChunk(kind, frame)...)
	out := make([]byte, 8)
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(len(content)))
	return append(out, content...)
}
func assertWebPFixture(t *testing.T, payload []byte, canvasW, frameW, frameH int) {
	t.Helper()
	cfg, kind, err := image.DecodeConfig(bytes.NewReader(payload))
	if err != nil || kind != "webp" || cfg.Width != canvasW {
		t.Fatalf("fixture config invalid: %v %s %dx%d", err, kind, cfg.Width, cfg.Height)
	}
	pic, kind, err := image.Decode(bytes.NewReader(payload))
	if err != nil || kind != "webp" || pic.Bounds().Dx() != frameW || pic.Bounds().Dy() != frameH {
		t.Fatalf("fixture full frame invalid (not behavioral RED): %v", err)
	}
	t.Logf("valid frame decoded from %d bytes: config width=%d actual=%dx%d", len(payload), cfg.Width, pic.Bounds().Dx(), pic.Bounds().Dy())
}
func TestLocalAvatarWebPCanvasCannotHideFrameBounds(t *testing.T) {
	ctx := context.Background()
	a := NewLocalAvatar(t.TempDir())
	for _, tc := range []struct {
		name          string
		width, canvas int
		accept        bool
	}{{"simple_valid", 4, 0, true}, {"extended_valid", 4, 4, true}, {"small_mismatch", 2, 1, false}, {"oversize_hidden_frame", 4097, 1, false}, {"dimension_boundary", 4096, 4096, true}} {
		t.Run(tc.name, func(t *testing.T) {
			payload := webpWithFrame("VP8L", solidVP8L(tc.width, 1), tc.canvas, 1)
			advertised := tc.canvas
			if advertised == 0 {
				advertised = tc.width
			}
			assertWebPFixture(t, payload, advertised, tc.width, 1)
			ref, err := a.Put(ctx, "owner", bytes.NewReader(payload))
			if !tc.accept {
				if err == nil {
					t.Fatalf("avatar accepted mismatched/overlimit WebP frame: canvas=%d frame=%d ref=%s", advertised, tc.width, ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid supported WebP rejected: %v", err)
			}
			r, _, kind, err := a.Open(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			out, err := png.Decode(r)
			if err != nil || kind != "image/png" || out.Bounds().Dx() != tc.width || out.Bounds().Dy() != 1 {
				t.Fatal("supported frame was not reencoded correctly")
			}
		})
	}
}

func TestLocalAvatarWebPLossyCanvasConsistency(t *testing.T) {
	encoded, err := os.ReadFile("testdata/blue-purple-pink.lossy.webp.base64")
	if err != nil {
		t.Fatal(err)
	}
	original, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	cfg, kind, err := image.DecodeConfig(bytes.NewReader(original))
	if err != nil || kind != "webp" || cfg.Width <= 1 || cfg.Height <= 1 || cfg.Width*cfg.Height > 65536 {
		t.Fatal("bounded pinned lossy fixture invalid")
	}
	var frame []byte
	for pos := 12; pos+8 <= len(original); {
		size := int(binary.LittleEndian.Uint32(original[pos+4 : pos+8]))
		end := pos + 8 + size
		if size < 0 || end > len(original) {
			t.Fatal("fixture chunk invalid")
		}
		if string(original[pos:pos+4]) == "VP8 " {
			frame = original[pos+8 : end]
			break
		}
		pos = end + size%2
	}
	if len(frame) == 0 {
		t.Fatal("pinned fixture has no VP8 frame")
	}
	for _, tc := range []struct {
		name             string
		canvasW, canvasH int
		accept           bool
	}{{"simple_valid", 0, 0, true}, {"extended_valid", cfg.Width, cfg.Height, true}, {"mismatch", 1, 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			payload := webpWithFrame("VP8 ", frame, tc.canvasW, tc.canvasH)
			advertised := tc.canvasW
			if advertised == 0 {
				advertised = cfg.Width
			}
			assertWebPFixture(t, payload, advertised, cfg.Width, cfg.Height)
			a := NewLocalAvatar(t.TempDir())
			ref, err := a.Put(context.Background(), "owner", bytes.NewReader(payload))
			if !tc.accept {
				if err == nil {
					t.Fatal("avatar accepted mismatched VP8 canvas/frame")
				}
				return
			}
			if err != nil {
				t.Fatalf("valid VP8 rejected: %v", err)
			}
			r, _, _, err := a.Open(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			actual, err := png.Decode(r)
			if err != nil || actual.Bounds().Dx() != cfg.Width || actual.Bounds().Dy() != cfg.Height {
				t.Fatal("VP8 was not reencoded")
			}
		})
	}
}
