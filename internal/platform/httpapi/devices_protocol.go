package api

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

// Signed device records (docs/design/devices/BRIEF.md). Each is a compact JSON
// array whose first element is its domain string, sent as the exact signed
// bytes. The server verifies those bytes and then reads them; it never
// re-serializes a record, so client and server cannot disagree on encoding.

const (
	deviceGrantDomain     = "misty.device.grant.v2"
	deviceListDomain      = "misty.device.list.v1"
	devicePolicyDomain    = "misty.device.policy.v1"
	deviceRunGrantDomain  = "misty.device.run-grant.v1"
	deviceAdmissionDomain = "misty.device.admission.v1"
	deviceRegisterDomain  = "misty.device.register.v1"
	deviceChannelDomain   = "misty.device.channel.v1"
	deviceRequestDomain   = "misty.device.request.v1"

	deviceRecordClockSkew = 10 * time.Minute
	deviceRunGrantMaxAge  = 24 * time.Hour
	maxDeviceListMembers  = 64
)

// deviceClock is the time record freshness is checked against; tests pin it.
var deviceClock = time.Now

var (
	errSignedDeviceRecord  = errors.New("signed device record is invalid")
	deviceSyncIDPattern    = regexp.MustCompile(`^[0-9a-f-]{36}$`)
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

// admissionGrant is a vault-root-signed admission of one device key.
type admissionGrant struct {
	AccountID          string
	VaultID            string
	DeviceID           string
	KeyEpoch           int64
	PublicKey          string
	SyncDeviceID       string
	IssuedAt           int64
	ApprovedByDeviceID string
	Payload            []byte
	Signature          string
}

func parseDeviceGrant(record signedDeviceRecord, root []byte) (*admissionGrant, error) {
	payload, fields, err := record.verify(root, deviceGrantDomain)
	if err != nil {
		return nil, err
	}
	grant := &admissionGrant{Payload: payload, Signature: record.Signature}
	if err := decodeDeviceFields(fields, &grant.AccountID, &grant.VaultID, &grant.DeviceID, &grant.KeyEpoch, &grant.PublicKey, &grant.SyncDeviceID, &grant.IssuedAt, &grant.ApprovedByDeviceID); err != nil {
		return nil, err
	}
	if _, ok := decodeDevicePublicKey(grant.PublicKey); !ok || !deviceIDPattern.MatchString(grant.DeviceID) || !deviceSyncIDPattern.MatchString(grant.VaultID) ||
		(grant.SyncDeviceID != "" && !deviceSyncIDPattern.MatchString(grant.SyncDeviceID)) ||
		(grant.ApprovedByDeviceID != "" && !deviceIDPattern.MatchString(grant.ApprovedByDeviceID)) || grant.KeyEpoch < 1 || !deviceTimeFresh(grant.IssuedAt) {
		return nil, errSignedDeviceRecord
	}
	return grant, nil
}

func (g *admissionGrant) record() db.DeviceGrantRecord {
	return db.DeviceGrantRecord{DeviceID: g.DeviceID, PublicKey: g.PublicKey, VaultID: g.VaultID, KeyEpoch: g.KeyEpoch,
		SyncDeviceID: g.SyncDeviceID, ApprovedByDeviceID: g.ApprovedByDeviceID, Payload: g.Payload, Signature: g.Signature}
}

// parseDeviceList verifies a root-signed list. Members are sorted and unique;
// no key is both admitted and removed.
func parseDeviceList(record signedDeviceRecord, root []byte) (*db.DeviceListChange, string, int64, error) {
	payload, fields, err := record.verify(root, deviceListDomain)
	if err != nil {
		return nil, "", 0, err
	}
	var accountID, vaultID string
	var version, epoch, issuedAt int64
	var admitted, revoked [][2]string
	if err := decodeDeviceFields(fields, &accountID, &vaultID, &version, &epoch, &admitted, &revoked, &issuedAt); err != nil {
		return nil, "", 0, err
	}
	if !deviceSyncIDPattern.MatchString(vaultID) || version < 1 || epoch < 1 || !deviceTimeFresh(issuedAt) || len(admitted)+len(revoked) > maxDeviceListMembers*4 {
		return nil, "", 0, errSignedDeviceRecord
	}
	members := func(raw [][2]string) ([]db.DeviceListMember, bool) {
		out := make([]db.DeviceListMember, 0, len(raw))
		for index, pair := range raw {
			if !deviceIDPattern.MatchString(pair[0]) {
				return nil, false
			}
			if _, ok := decodeDevicePublicKey(pair[1]); !ok {
				return nil, false
			}
			if index > 0 && pair[0] <= raw[index-1][0] {
				return nil, false
			}
			out = append(out, db.DeviceListMember{DeviceID: pair[0], PublicKey: pair[1]})
		}
		return out, true
	}
	admittedMembers, ok := members(admitted)
	if !ok || len(admittedMembers) > maxDeviceListMembers {
		return nil, "", 0, errSignedDeviceRecord
	}
	revokedMembers, ok := members(revoked)
	if !ok {
		return nil, "", 0, errSignedDeviceRecord
	}
	removedKeys := map[string]bool{}
	for _, member := range revokedMembers {
		removedKeys[member.PublicKey] = true
	}
	for _, member := range admittedMembers {
		if removedKeys[member.PublicKey] {
			return nil, "", 0, errSignedDeviceRecord
		}
	}
	return &db.DeviceListChange{VaultID: vaultID, Version: version, Payload: payload, Signature: record.Signature, Admitted: admittedMembers, Revoked: revokedMembers}, accountID, epoch, nil
}

// devicePolicy is a device's own signed permissions.
type devicePolicy struct {
	AccountID string
	DeviceID  string
	Record    db.DevicePolicyRecord
}

func parseDevicePolicy(record signedDeviceRecord, devicePublicKey []byte) (*devicePolicy, error) {
	payload, fields, err := record.verify(devicePublicKey, devicePolicyDomain)
	if err != nil {
		return nil, err
	}
	policy := &devicePolicy{Record: db.DevicePolicyRecord{Payload: payload, Signature: record.Signature}}
	var issuedAt int64
	var folders [][2]string
	if err := decodeDeviceFields(fields, &policy.AccountID, &policy.DeviceID, &policy.Record.Version, &policy.Record.Files, &policy.Record.Clipboard, &policy.Record.AgentSurfaces, &folders, &issuedAt); err != nil {
		return nil, err
	}
	if len(folders) > 64 {
		return nil, errSignedDeviceRecord
	}
	policy.Record.SharedFolders = make([]db.DeviceSharedFolder, 0, len(folders))
	seenFolders := map[string]bool{}
	for _, folder := range folders {
		if !deviceScopeRefPattern.MatchString(folder[0]) || seenFolders[folder[0]] || !validText(folder[1], 1, 200) {
			return nil, errSignedDeviceRecord
		}
		seenFolders[folder[0]] = true
		policy.Record.SharedFolders = append(policy.Record.SharedFolders, db.DeviceSharedFolder{ScopeID: folder[0], Name: folder[1]})
	}
	if !deviceIDPattern.MatchString(policy.DeviceID) || policy.Record.Version < 1 || !deviceTimeFresh(issuedAt) ||
		(policy.Record.Files != "off" && policy.Record.Files != "view" && policy.Record.Files != "edit") || len(policy.Record.AgentSurfaces) > len(deviceAgentSurfaces) {
		return nil, errSignedDeviceRecord
	}
	if !sort.StringsAreSorted(policy.Record.AgentSurfaces) {
		return nil, errSignedDeviceRecord
	}
	for index, surface := range policy.Record.AgentSurfaces {
		if !deviceAgentSurfaces[surface] || (index > 0 && policy.Record.AgentSurfaces[index-1] == surface) {
			return nil, errSignedDeviceRecord
		}
	}
	if policy.Record.AgentSurfaces == nil {
		policy.Record.AgentSurfaces = []string{}
	}
	return policy, nil
}

// deviceRunGrant lets one run use named scopes and capabilities on a target
// device. The requesting device signs it; the server cannot widen it.
type deviceRunGrant struct {
	AccountID         string
	GrantID           string
	RequesterDeviceID string
	TargetDeviceID    string
	AgentID           string
	Capabilities      []string
	Scopes            []string
	IssuedAt          int64
	ExpiresAt         int64
	Payload           []byte
	Signature         string
}

func parseDeviceRunGrant(record signedDeviceRecord, requesterKey []byte) (*deviceRunGrant, error) {
	payload, fields, err := record.verify(requesterKey, deviceRunGrantDomain)
	if err != nil {
		return nil, err
	}
	grant := &deviceRunGrant{Payload: payload, Signature: record.Signature}
	if err := decodeDeviceFields(fields, &grant.AccountID, &grant.GrantID, &grant.RequesterDeviceID, &grant.TargetDeviceID, &grant.AgentID, &grant.Capabilities, &grant.Scopes, &grant.IssuedAt, &grant.ExpiresAt); err != nil {
		return nil, err
	}
	now := deviceClock().Unix()
	if !deviceGrantIDPattern.MatchString(grant.GrantID) || !deviceIDPattern.MatchString(grant.RequesterDeviceID) || !deviceIDPattern.MatchString(grant.TargetDeviceID) ||
		(grant.AgentID != "" && !deviceAgentIDPattern.MatchString(grant.AgentID)) || !deviceTimeFresh(grant.IssuedAt) || grant.ExpiresAt <= now ||
		grant.ExpiresAt-grant.IssuedAt > int64(deviceRunGrantMaxAge/time.Second) || len(grant.Capabilities) == 0 || len(grant.Capabilities) > 64 || len(grant.Scopes) == 0 || len(grant.Scopes) > 64 {
		return nil, errSignedDeviceRecord
	}
	for _, capability := range grant.Capabilities {
		if !deviceCapabilityFormat.MatchString(capability) {
			return nil, errSignedDeviceRecord
		}
	}
	for _, scope := range grant.Scopes {
		if !deviceScopeRefPattern.MatchString(scope) {
			return nil, errSignedDeviceRecord
		}
	}
	return grant, nil
}

func (g *deviceRunGrant) allows(scope string, capabilities []string) bool {
	scopes := map[string]bool{}
	for _, value := range g.Scopes {
		scopes[value] = true
	}
	granted := map[string]bool{}
	for _, value := range g.Capabilities {
		granted[value] = true
	}
	if !scopes[scope] {
		return false
	}
	for _, capability := range capabilities {
		if !granted[capability] {
			return false
		}
	}
	return true
}

// deviceAdmissionRequest is a pending device asking an added one to approve it.
type deviceAdmissionRequest struct {
	AccountID    string
	DeviceID     string
	PublicKey    string
	X25519Public string
	Commitment   string
	IssuedAt     int64
	Payload      []byte
}

func parseDeviceAdmissionRequest(record signedDeviceRecord, devicePublicKey []byte) (*deviceAdmissionRequest, error) {
	payload, fields, err := record.verify(devicePublicKey, deviceAdmissionDomain)
	if err != nil {
		return nil, err
	}
	request := &deviceAdmissionRequest{Payload: payload}
	if err := decodeDeviceFields(fields, &request.AccountID, &request.DeviceID, &request.PublicKey, &request.X25519Public, &request.Commitment, &request.IssuedAt); err != nil {
		return nil, err
	}
	commitment, commitErr := hex.DecodeString(request.Commitment)
	if _, ok := decodeDevicePublicKey(request.PublicKey); !ok || !validX25519Public(request.X25519Public) || commitErr != nil || len(commitment) != 32 ||
		!deviceIDPattern.MatchString(request.DeviceID) || !deviceTimeFresh(request.IssuedAt) {
		return nil, errSignedDeviceRecord
	}
	return request, nil
}

func validX25519Public(value string) bool {
	raw, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.StdEncoding.EncodeToString(raw) == value
}

func validDeviceNonce(value string) bool {
	raw, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.StdEncoding.EncodeToString(raw) == value
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
