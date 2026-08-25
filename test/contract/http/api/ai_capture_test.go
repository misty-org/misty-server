package api

import (
	"encoding/base64"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestValidateAICaptureAcceptsBoundedImageData(t *testing.T) {
	capture := &TestingAICaptureAttachment{
		ID: "capture-1", Name: "Region", MimeType: "image/jpeg",
		DataURL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		Width:   40, Height: 20, ContentHash: "hash",
	}
	if err := TestingValidateAICapture(capture); err != nil {
		t.Fatalf("expected valid capture, got %v", err)
	}
}

func TestValidateAICaptureRejectsOversizedImageData(t *testing.T) {
	capture := &TestingAICaptureAttachment{
		ID: "capture-1", Name: "Region", MimeType: "image/jpeg",
		DataURL: "data:image/jpeg;base64," + strings.Repeat("A", 2<<20),
		Width:   40, Height: 20, ContentHash: "hash",
	}
	if err := TestingValidateAICapture(capture); err == nil {
		t.Fatal("expected oversized capture to be rejected")
	}
}
