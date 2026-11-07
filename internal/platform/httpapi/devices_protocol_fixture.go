package api

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// DeviceRecordFixture is records signed by the desktop app's Rust code, so the
// server's parsers are checked against the exact bytes clients send.
type DeviceRecordFixture struct {
	AccountID       string             `json:"accountId"`
	DeviceID        string             `json:"deviceId"`
	DevicePublicKey string             `json:"devicePublicKey"`
	IssuedAt        int64              `json:"issuedAt"`
	Policy          signedDeviceRecord `json:"policy"`
	Registration    struct {
		EndpointID string `json:"endpointId"`
		Proof      string `json:"proof"`
	} `json:"registration"`
	Channel struct {
		Instance  string `json:"instance"`
		Challenge string `json:"challenge"`
		Signature string `json:"signature"`
	} `json:"channel"`
	Request struct {
		Canonical string `json:"canonical"`
		Signature string `json:"signature"`
	} `json:"request"`
}

// TestingVerifyDeviceRecordFixture parses every record with the server's own
// parsers at the fixture's time.
func TestingVerifyDeviceRecordFixture(fixture DeviceRecordFixture) error {
	previous := deviceClock
	deviceClock = func() time.Time { return time.Unix(fixture.IssuedAt+60, 0) }
	defer func() { deviceClock = previous }()
	device, ok := decodeDevicePublicKey(fixture.DevicePublicKey)
	if !ok {
		return errors.New("device key")
	}
	policy, err := parseDevicePolicy(fixture.Policy, device)
	if err != nil || policy.DeviceID != fixture.DeviceID || policy.Record.Files != "view" || len(policy.Record.SharedFolders) != 1 {
		return fmt.Errorf("policy: %v", err)
	}
	if !verifyDeviceRegistrationProof(fixture.AccountID, fixture.DevicePublicKey, fixture.Registration.EndpointID, fixture.IssuedAt, fixture.Registration.Proof) ||
		fixture.Registration.EndpointID != hex.EncodeToString(device) {
		return errors.New("registration proof")
	}
	signature, _ := base64.StdEncoding.DecodeString(fixture.Channel.Signature)
	if !ed25519Verify(device, deviceChannelProof(fixture.AccountID, fixture.DeviceID, fixture.Channel.Instance, fixture.Channel.Challenge), signature) {
		return errors.New("channel proof")
	}
	signature, _ = base64.StdEncoding.DecodeString(fixture.Request.Signature)
	if !ed25519Verify(device, unifiedDeviceRequestMessage(fixture.Request.Canonical), signature) {
		return errors.New("request signature")
	}
	return nil
}

func ed25519Verify(key, message, signature []byte) bool {
	return len(key) == ed25519.PublicKeySize && len(signature) == ed25519.SignatureSize && ed25519.Verify(key, message, signature)
}
