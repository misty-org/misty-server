package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

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

func TestConnectedDeviceRegistrationRequiresEndpointAndProtocolTogether(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := base64.RawURLEncoding.EncodeToString(publicKey)
	endpointID := "endpoint_abcdefghijklmnopqrstuvwxyz0123456789"
	if !TestingValidDeviceRegistrationV2("My Mac", key, "ed25519", "macos", endpointID, json.RawMessage(`["misty-device/1"]`), json.RawMessage(`{"connectedDevices":true}`)) {
		t.Fatal("valid connected-device registration rejected")
	}
	if TestingValidDeviceRegistrationV2("My Mac", key, "ed25519", "macos", endpointID, nil, json.RawMessage(`{}`)) {
		t.Fatal("endpoint without a protocol was accepted")
	}
	if TestingValidDeviceRegistrationV2("My Mac", key, "ed25519", "macos", endpointID, json.RawMessage(`["misty-device/2"]`), json.RawMessage(`{}`)) {
		t.Fatal("unsupported protocol was accepted")
	}
	if TestingValidDeviceRegistrationV2("My Mac", key, "ed25519", "macos", endpointID, json.RawMessage(`["misty-device/1"]`), json.RawMessage(`{"clipboardPayload":"secret"}`)) {
		t.Fatal("clipboard data was accepted in registration")
	}
}

func TestConnectedDeviceFingerprintIsSymmetric(t *testing.T) {
	first := "endpoint_abcdefghijklmnopqrstuvwxyz0123456789"
	second := "endpoint_9876543210zyxwvutsrqponmlkjihgfedcba"
	forward := TestingConnectedDeviceFingerprint(first, second)
	if forward != TestingConnectedDeviceFingerprint(second, first) {
		t.Fatal("fingerprint changes with endpoint order")
	}
	if len(forward) != 9 || forward[4] != '-' {
		t.Fatalf("fingerprint = %q, want XXXX-XXXX", forward)
	}
}

func TestConnectedDeviceTicketSignatureAndExpiry(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	config := TestingConnectedDevicesConfig(privateKey, make([]byte, 32))
	if config.KeyID == "" || len(config.PublicKeys) != 1 {
		t.Fatal("test config did not expose a rotatable key id")
	}

	// Exercise the verifier against a ticket with the exact production envelope.
	now := time.Now().UTC()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{
		"iss": "misty-api", "aud": "misty-device/1", "jti": "peerticket_test",
		"pairId": "pair_test", "sourceDeviceId": "device_source", "sourceEndpointId": "endpoint_source",
		"targetDeviceId": "device_target", "targetEndpointId": "endpoint_target",
		"protocolVersion": "misty-device/1", "permissions": []string{"files:read"},
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	ticket := input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(input)))
	if err := TestingVerifyConnectedDeviceTicket(ticket, publicKey, now); err != nil {
		t.Fatalf("fresh ticket rejected: %v", err)
	}
	if err := TestingVerifyConnectedDeviceTicket(ticket, publicKey, now.Add(6*time.Minute)); err == nil {
		t.Fatal("expired ticket accepted")
	}
	tampered := ticket[:len(ticket)-1] + "A"
	if err := TestingVerifyConnectedDeviceTicket(tampered, publicKey, now); err == nil {
		t.Fatal("tampered ticket accepted")
	}
}
