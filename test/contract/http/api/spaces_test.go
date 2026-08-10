package api

import (
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
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

func TestSpaceTargetFingerprintDoesNotExposeTarget(t *testing.T) {
	target := "https://drive.google.com/file/d/secret/view"
	fingerprint := SpaceTargetFingerprint(target)
	if len(fingerprint) != 16 || strings.Contains(fingerprint, "secret") {
		t.Fatalf("unsafe fingerprint %q", fingerprint)
	}
}

func TestAgentMentionFailuresAreSafeAndActionable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "hosted AI", err: serveragent.HostedAILimitReachedError{Required: 10, Available: 2}, code: "hosted_ai_limit_reached"},
		{name: "integration", err: db.ErrWorkflowIntegrationRequired, code: "integration_required"},
		{name: "permission", err: db.ErrLibraryForbidden, code: "forbidden"},
		{name: "removed", err: db.ErrAgentNotFound, code: "resource_unavailable"},
		{name: "internal", err: errors.New("provider secret detail"), code: "run_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := TestingAgentMentionFailureFromError("agent_one", test.err)
			if failure.AgentID != "agent_one" || failure.Code != test.code || failure.Message == "" {
				t.Fatalf("agentMentionFailureFromError() = %#v", failure)
			}
			if strings.Contains(failure.Message, "provider secret detail") {
				t.Fatalf("failure leaked internal error: %#v", failure)
			}
		})
	}
}
