package v1_test

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContractProfileTimezoneAndAvatar(t *testing.T) {
	r, _ := newContractEnv(t)
	token, _, _ := mustRegister(t, r, "avatar@example.invalid", "头像用户")
	t.Run("reject_invalid_timezone", func(t *testing.T) {
		w := doReq(t, r, http.MethodPut, "/api/v1/user/profile", token, map[string]any{"name": "头像用户", "timezone": "Local"})
		if w.Code < 400 || w.Code >= 500 {
			t.Errorf("invalid timezone HTTP status=%d", w.Code)
		}
	})
	t.Run("upload_and_remove_own_avatar", func(t *testing.T) {
		var picture bytes.Buffer
		if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("avatar", "avatar.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(picture.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user/avatar", &body)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("valid own avatar upload status=%d body=%s", w.Code, w.Body.String())
		}
		profile := decodeBody(t, w)
		if profile["avatar_url"] == "" {
			t.Fatal("upload did not persist reference")
		}
		w = doReq(t, r, http.MethodDelete, "/api/v1/user/avatar", token, nil)
		if w.Code != 200 || decodeBody(t, w)["avatar_url"] != "" {
			t.Fatalf("delete did not explicitly clear reference: %d %s", w.Code, w.Body.String())
		}
	})
}
