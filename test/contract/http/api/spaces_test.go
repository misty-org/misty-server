package api

import (
	"encoding/base64"
	"net"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

)

func TestSpaceLinkEncryptionRoundTrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	service, err := NewSpacesService(nil, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	target := "https://drive.google.com/file/d/abc/view"
	ciphertext, nonce, err := service.TestingEncryptTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(ciphertext) == target {
		t.Fatal("Drive target was stored as plaintext")
	}
	got, err := service.TestingDecryptTarget(ciphertext, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("decrypted target = %q, want %q", got, target)
	}
}

func TestWorkflowHTTPRejectsPrivateAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "::1", "fc00::1"} {
		if TestingIsPublicWorkflowIP(net.ParseIP(raw)) {
			t.Fatalf("private workflow address %s was accepted", raw)
		}
	}
	if !TestingIsPublicWorkflowIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public workflow address was rejected")
	}
}

func TestValidGoogleDriveTarget(t *testing.T) {
	accepted := []string{
		"https://drive.google.com/file/d/abc/view",
		"https://docs.google.com/document/d/abc/edit",
		"https://drive.usercontent.google.com/download?id=abc",
	}
	for _, target := range accepted {
		if _, err := TestingValidGoogleDriveTarget(target); err != nil {
			t.Errorf("validGoogleDriveTarget(%q) error = %v", target, err)
		}
	}
	for _, target := range []string{"http://drive.google.com/file", "https://drive.google.com.evil.example/file", "https://example.com/file", "file:///tmp/private"} {
		if _, err := TestingValidGoogleDriveTarget(target); err == nil {
			t.Errorf("validGoogleDriveTarget(%q) unexpectedly succeeded", target)
		}
	}
}

