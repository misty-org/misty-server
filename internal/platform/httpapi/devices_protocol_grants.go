package api

import (
	"sort"
	"time"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

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
