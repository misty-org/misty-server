package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestReadAgentAvatarAcceptsPNGJPEGAndWebP(t *testing.T) {
	imageValue := image.NewRGBA(image.Rect(0, 0, 2, 2))
	imageValue.Set(0, 0, color.RGBA{R: 80, G: 120, B: 220, A: 255})

	var pngData bytes.Buffer
	if err := png.Encode(&pngData, imageValue); err != nil {
		t.Fatal(err)
	}
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, imageValue, nil); err != nil {
		t.Fatal(err)
	}
	webPData := append([]byte("RIFF\x16\x00\x00\x00WEBPVP8 \x0a\x00\x00\x00\x00\x00\x00\x9d\x01\x2a"), []byte{2, 0, 2, 0}...)

	for _, test := range []struct {
		name string
		data []byte
		mime string
	}{
		{name: "png", data: pngData.Bytes(), mime: "image/png"},
		{name: "jpeg", data: jpegData.Bytes(), mime: "image/jpeg"},
		{name: "webp", data: webPData, mime: "image/webp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("PUT", "/agents/test/avatar", bytes.NewReader(test.data))
			response := httptest.NewRecorder()
			data, mime, ok := TestingReadAgentAvatar(response, request)
			if !ok || mime != test.mime || !bytes.Equal(data, test.data) {
				t.Fatalf("readAgentAvatar() = %q, %q, %v; status=%d", data, mime, ok, response.Code)
			}
		})
	}
}

func TestReadAgentAvatarRejectsInvalidImage(t *testing.T) {
	request := httptest.NewRequest("PUT", "/agents/test/avatar", bytes.NewBufferString("not an image"))
	response := httptest.NewRecorder()
	if _, _, ok := TestingReadAgentAvatar(response, request); ok || response.Code != 415 {
		t.Fatalf("readAgentAvatar() ok=%v status=%d, want false/415", ok, response.Code)
	}
}
