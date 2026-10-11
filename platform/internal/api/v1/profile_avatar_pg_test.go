package v1_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuqing/platform/internal/pkg/storage"
)

func avatarHTTP(t *testing.T, e *billingActorPGEnv, token, filename string, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("avatar", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(payload)
	_ = form.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/avatar", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}
func TestProfileAvatarPGHTTPBoundaries(t *testing.T) {
	e := newBillingActorPGEnv(t)
	e.cfg.Storage.AvatarRoot = t.TempDir()
	e.rebuild()
	token, _, u := mustRegister(t, e.router, "avatar-http@example.invalid", "头像持有人")
	other, _, _ := mustRegister(t, e.router, "avatar-other@example.invalid", "其他持有人")
	var pic bytes.Buffer
	_ = png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 6, 6)))
	w := avatarHTTP(t, e, token, "avatar.png", pic.Bytes())
	if w.Code != 200 {
		t.Fatalf("upload=%d %s", w.Code, w.Body.String())
	}
	ref := decodeBody(t, w)["avatar_url"].(string)
	w = avatarHTTP(t, e, other, "other.png", pic.Bytes())
	if w.Code != 200 {
		t.Fatal("other owner upload failed")
	}
	otherRef := decodeBody(t, w)["avatar_url"].(string)
	e.rebuild()
	w = doReq(t, e.router, http.MethodGet, "/api/v1/user/profile", token, nil)
	if decodeBody(t, w)["avatar_url"] != ref {
		t.Fatal("reference lost on restart")
	}
	w = doReq(t, e.router, http.MethodGet, ref, "", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("stored image response=%d headers=%v", w.Code, w.Header())
	}
	// Client-controlled object, URL and user targets cannot remove another image.
	for _, path := range []string{"/api/v1/user/avatar?avatar_url=" + ref, "/api/v1/user/avatar?user_id=" + u["user_id"].(string)} {
		w = doReq(t, e.router, http.MethodDelete, path, other, nil)
		if w.Code != 400 {
			t.Fatal("foreign delete selector accepted")
		}
	}
	w = doReq(t, e.router, http.MethodDelete, "/api/v1/user/avatar", other, nil)
	if w.Code != 200 {
		t.Fatal("own delete failed")
	}
	if w = doReq(t, e.router, http.MethodGet, otherRef, "", nil); w.Code != 404 {
		t.Fatal("own image removal not reclaimed")
	}
	w = doReq(t, e.router, http.MethodGet, ref, "", nil)
	if w.Code != 200 {
		t.Fatal("other user's removal deleted owner image")
	}
	for i, tc := range []struct {
		name string
		data []byte
	}{{"fake.png", []byte("<svg onload='evil()'/> ")}, {"broken.png", []byte{137, 80, 78, 71}}, {"large.png", bytes.Repeat([]byte{0}, storage.AvatarMaxBytes+1)}, {"../traversal.png", pic.Bytes()}, {"bad.svg", []byte("https://example.invalid/image.png")}} {
		t.Run(fmt.Sprintf("reject_%d", i), func(t *testing.T) {
			w := avatarHTTP(t, e, token, tc.name, tc.data)
			if w.Code != 400 {
				t.Fatalf("invalid upload=%d", w.Code)
			}
		})
	}
	if w = avatarHTTP(t, e, "", "avatar.png", pic.Bytes()); w.Code != 401 {
		t.Fatalf("anonymous upload=%d", w.Code)
	}
	key := doReq(t, e.router, http.MethodPost, "/api/v1/apikeys", token, map[string]any{"name": "avatar-boundary"})
	raw := decodeBody(t, key)["api_key"].(string)
	if w = avatarHTTP(t, e, raw, "avatar.png", pic.Bytes()); w.Code != 403 {
		t.Fatalf("API key upload=%d", w.Code)
	}
	if w = doReq(t, e.router, http.MethodDelete, "/api/v1/user/avatar", raw, nil); w.Code != 403 {
		t.Fatalf("API key delete=%d", w.Code)
	}
	var stored string
	if err := e.pool.QueryRow(context.Background(), `SELECT avatar_url FROM users WHERE id=$1`, u["user_id"]).Scan(&stored); err != nil || stored != ref {
		t.Fatal("failed uploads changed reference")
	}
	w = doReq(t, e.router, http.MethodDelete, "/api/v1/user/avatar", token, nil)
	if w.Code != 200 || decodeBody(t, w)["avatar_url"] != "" {
		t.Fatal("remove failed")
	}
	if w = doReq(t, e.router, http.MethodGet, ref, "", nil); w.Code != 404 {
		t.Fatal("removed file still served")
	}
	for _, path := range []string{"/api/v1/avatars/not-an-object", "/api/v1/avatars/%2e%2e%2fprivate"} {
		if w = doReq(t, e.router, http.MethodGet, path, "", nil); w.Code == 200 {
			t.Fatal("path served")
		}
	}
}
