package api

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

// Signed device records (docs/sync/devices.md). Each is a compact JSON
// array whose first element is its domain string, sent as the exact signed
// bytes. The server verifies those bytes and then reads them; it never
// re-serializes a record, so client and server cannot disagree on encoding.

const (
	devicePolicyDomain    = "misty.device.policy.v1"
	deviceRunGrantDomain  = "misty.device.run-grant.v1"
	deviceRegisterDomain  = "misty.device.register.v1"
	deviceChannelDomain   = "misty.device.channel.v1"
	deviceRequestDomain   = "misty.device.request.v1"

	deviceRecordClockSkew = 10 * time.Minute
	deviceRunGrantMaxAge  = 24 * time.Hour
)

// deviceClock is the time record freshness is checked against; tests pin it.
var deviceClock = time.Now

var (
	errSignedDeviceRecord  = errors.New("signed device record is invalid")
	deviceAgentIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_:.-]{1,128}$`)
	deviceGrantIDPattern   = regexp.MustCompile(`^rungrant_[0-9a-f-]{36}$`)
	deviceScopeRefPattern  = regexp.MustCompile(`^[A-Za-z0-9_:./+=-]{1,512}$`)
	deviceCapabilityFormat = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)+$`)
)

// deviceAgentSurfaces are what a device's policy can let agents use.
var deviceAgentSurfaces = map[string]bool{"folders": true, "browser": true, "terminal": true}

type signedDeviceRecord struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// verify checks the signature over the decoded payload bytes and returns them
// with the parsed JSON array, whose first element must be the domain.
func (r signedDeviceRecord) verify(publicKey []byte, domain string) ([]byte, []json.RawMessage, error) {
	payload, err := base64.StdEncoding.DecodeString(r.Payload)
	if err != nil || len(payload) == 0 || len(payload) > 65536 {
		return nil, nil, errSignedDeviceRecord
	}
	signature, err := base64.StdEncoding.DecodeString(r.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, payload, signature) {
		return nil, nil, errSignedDeviceRecord
	}
	var fields []json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if decoder.Decode(&fields) != nil || decoder.More() || len(fields) < 2 {
		return nil, nil, errSignedDeviceRecord
	}
	var actual string
	if json.Unmarshal(fields[0], &actual) != nil || actual != domain {
		return nil, nil, errSignedDeviceRecord
	}
	return payload, fields, nil
}

func decodeDeviceFields(fields []json.RawMessage, targets ...any) error {
	if len(fields) != len(targets)+1 {
		return errSignedDeviceRecord
	}
	for index, target := range targets {
		if json.Unmarshal(fields[index+1], target) != nil {
			return errSignedDeviceRecord
		}
	}
	return nil
}

func decodeDevicePublicKey(value string) ([]byte, bool) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(raw) != value {
		return nil, false
	}
	return raw, true
}

func deviceTimeFresh(unix int64) bool {
	return deviceClock().Sub(time.Unix(unix, 0)).Abs() <= deviceRecordClockSkew
}

// deviceRegistrationProof shows the registering install holds the private key
// for the public key it names, so nobody can claim another device's key.
func verifyDeviceRegistrationProof(accountID, publicKey, endpointID string, issuedAt int64, signature string) bool {
	key, ok := decodeDevicePublicKey(publicKey)
	if !ok || !deviceTimeFresh(issuedAt) || endpointID != hex.EncodeToString(key) {
		return false
	}
	message, _ := json.Marshal([]any{deviceRegisterDomain, accountID, publicKey, endpointID, issuedAt})
	raw, err := base64.StdEncoding.DecodeString(signature)
	return err == nil && len(raw) == ed25519.SignatureSize && ed25519.Verify(key, message, raw)
}

func deviceChannelProof(accountID, deviceID, instanceID, challenge string) []byte {
	message, _ := json.Marshal([]any{deviceChannelDomain, accountID, deviceID, instanceID, challenge})
	return message
}

// unifiedDeviceRequestMessage is what a unified device signs for an HTTP call.
func unifiedDeviceRequestMessage(canonical string) []byte {
	return []byte(deviceRequestDomain + "\n" + canonical)
}
