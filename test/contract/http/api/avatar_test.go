package api

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestReadAvatarPNGAcceptsValidPNG(t *testing.T) {
	var encoded bytes.Buffer
	image := image.NewRGBA(image.Rect(0, 0, 2, 2))
	image.Set(0, 0, color.White)
	if err := png.Encode(&encoded, image); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/me/avatar", bytes.NewReader(encoded.Bytes()))
	recorder := httptest.NewRecorder()

	data, ok := TestingReadAvatarPNG(recorder, req)
	if !ok || !bytes.Equal(data, encoded.Bytes()) {
		t.Fatalf("readAvatarPNG() accepted=%v bytes=%d, want valid PNG", ok, len(data))
	}
}

func TestReadAvatarPNGRejectsNonPNG(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/me/avatar", strings.NewReader("not an image"))
	recorder := httptest.NewRecorder()

	if _, ok := TestingReadAvatarPNG(recorder, req); ok {
		t.Fatal("readAvatarPNG() accepted non-PNG bytes")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("readAvatarPNG() status=%d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
