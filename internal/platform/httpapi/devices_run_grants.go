package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// Every device context an agent chat attaches carries a run grant signed by
// the device that asked (docs/sync/devices.md), and names that same
// device: an agent run works only on the computer the person sent it from. A
// signed-in device may do so before it is added. The runtime may request less
// than a grant, never more.

var errDeviceGrantRequired = errors.New("a signed device grant is required")

// unverifiedRunGrantRequester reads who claims to have signed a grant so its
// key can be looked up; the signature is checked right after.
func unverifiedRunGrantRequester(record signedDeviceRecord) string {
	payload, err := base64.StdEncoding.DecodeString(record.Payload)
	if err != nil {
		return ""
	}
	var fields []json.RawMessage
	if json.Unmarshal(payload, &fields) != nil || len(fields) != 10 {
		return ""
	}
	var requester string
	_ = json.Unmarshal(fields[3], &requester)
	return requester
}

func verifyDeviceContextGrants(ctx context.Context, database *db.Database, userID, agentID string, contexts []aiInvocationDeviceContext) error {
	for _, deviceContext := range contexts {
		if deviceContext.RunGrant == nil {
			return errDeviceGrantRequired
		}
		requester := unverifiedRunGrantRequester(*deviceContext.RunGrant)
		if !deviceIDPattern.MatchString(requester) {
			return errDeviceGrantRequired
		}
		keyText, _, err := database.UnifiedDeviceKey(ctx, userID, requester)
		if err != nil {
			return errDeviceGrantRequired
		}
		key, ok := decodeDevicePublicKey(keyText)
		if !ok {
			return errDeviceGrantRequired
		}
		grant, err := parseDeviceRunGrant(*deviceContext.RunGrant, key)
		if err != nil || grant.AccountID != userID || grant.RequesterDeviceID != requester || grant.TargetDeviceID != deviceContext.DeviceID ||
			(grant.AgentID != "" && grant.AgentID != agentID) {
			return errDeviceGrantRequired
		}
		var capabilities []string
		if json.Unmarshal(deviceContext.Capabilities, &capabilities) != nil || !grant.allows(deviceContext.OpaqueRef, capabilities) {
			return errDeviceGrantRequired
		}
		// An agent run works only on the device the person sent it from: a grant
		// that names any other device is refused, whatever that device allows.
		if grant.TargetDeviceID != requester {
			return errDeviceGrantRequired
		}
	}
	return nil
}

// runGrantRecord is what the context row keeps so device jobs can carry it.
func runGrantRecord(record *signedDeviceRecord) *db.DeviceRunGrantRecord {
	if record == nil {
		return nil
	}
	payload, err := base64.StdEncoding.DecodeString(record.Payload)
	if err != nil {
		return nil
	}
	return &db.DeviceRunGrantRecord{Payload: payload, Signature: record.Signature, RequesterDeviceID: unverifiedRunGrantRequester(*record)}
}

// TestingVerifyDeviceContextGrants exposes the grant check to contract tests.
func TestingVerifyDeviceContextGrants(ctx context.Context, database *db.Database, userID, agentID string, contextsJSON []byte) error {
	var contexts []aiInvocationDeviceContext
	if err := json.Unmarshal(contextsJSON, &contexts); err != nil {
		return err
	}
	return verifyDeviceContextGrants(ctx, database, userID, agentID, contexts)
}
