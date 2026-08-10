package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	. "github.com/kannachi323/misty/server/internal/platform/httpapi"
)

func TestTrustedDeviceSignatureBindsMethodPathNonceAndBody(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := TestingDeviceSignaturePayload("POST", "/api/devices/device_123/workflow-node-jobs/claim", "1700000000", "unique-nonce-1234", []byte(`{"limit":1}`))
	signature := ed25519.Sign(privateKey, []byte(payload))
	if !ed25519.Verify(publicKey, []byte(payload), signature) {
		t.Fatal("valid signature rejected")
	}
	tampered := TestingDeviceSignaturePayload("POST", "/api/devices/device_123/workflow-node-jobs/claim", "1700000000", "unique-nonce-1234", []byte(`{"limit":2}`))
	if ed25519.Verify(publicKey, []byte(tampered), signature) {
		t.Fatal("tampered request reused a signature")
	}
}

func TestTrustedDeviceRegistrationIsStrict(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.RawURLEncoding.EncodeToString(publicKey)
	if !TestingValidDeviceRegistration("My Mac", key, "ed25519", json.RawMessage(`{"workflowNodeLeases":true}`)) {
		t.Fatal("valid trusted device registration rejected")
	}
	if TestingValidDeviceRegistration("My Mac", key, "rsa", json.RawMessage(`{}`)) {
		t.Fatal("unsupported key algorithm accepted")
	}
	if TestingValidDeviceRegistration("My Mac", key, "ed25519", json.RawMessage(`{"libraryPath":"/Users/me"}`)) {
		t.Fatal("device capabilities leaked a local path")
	}
}
