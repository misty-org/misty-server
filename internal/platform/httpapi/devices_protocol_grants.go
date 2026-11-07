package api

import (
	"sort"

	"github.com/misty-org/misty-server/internal/platform/db"
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
