package api

import (
	"encoding/json"
	"errors"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

func validateAIInvocationDeviceContexts(references []aiContextReference, contexts []aiInvocationDeviceContext, spaceID string) error {
	if len(contexts) > 50 {
		return errors.New("at most 50 browser workspaces can be attached")
	}
	// Account-scoped invocations can attach local browser tabs without a Space.
	// Each workspace must still match an explicitly attached device reference.
	seen := map[string]bool{}
	for _, deviceContext := range contexts {
		deviceContext.DeviceID = strings.TrimSpace(deviceContext.DeviceID)
		deviceContext.Kind = strings.TrimSpace(deviceContext.Kind)
		deviceContext.OpaqueRef = strings.TrimSpace(deviceContext.OpaqueRef)
		if deviceContext.DeviceID == "" || deviceContext.OpaqueRef == "" || seen[deviceContext.OpaqueRef] {
			return errors.New("browser workspace identity is invalid")
		}
		seen[deviceContext.OpaqueRef] = true
		if deviceContext.Kind == "local_folder" || deviceContext.Kind == "workspace" || deviceContext.Kind == "inbox" {
			// Folders, the Misty browser and the device's inbox are granted on
			// the device itself; they never come from page content, so no page
			// reference applies.
			if !db.DeviceContextCapabilitiesAllowed(deviceContext.Kind, deviceContext.Capabilities) {
				return errors.New("device grant capabilities are invalid")
			}
			continue
		}
		if deviceContext.Kind != "browser_tab" {
			return errors.New("browser workspace identity is invalid")
		}
		var capabilities []string
		if json.Unmarshal(deviceContext.Capabilities, &capabilities) != nil || len(capabilities) == 0 {
			return errors.New("browser workspace capabilities are invalid")
		}
		matched := false
		for _, reference := range references {
			if reference.Kind == "browser-tab" && reference.Privacy == "device" && reference.Attached && (reference.SpaceID == "" || reference.SpaceID == spaceID) && reference.OpaqueScopeID == deviceContext.OpaqueRef {
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("browser workspace must match an attached context reference")
		}
	}
	return nil
}

func TestingValidateAIInvocationDeviceContexts(referencesJSON, contextsJSON []byte, spaceID string) error {
	var references []aiContextReference
	var contexts []aiInvocationDeviceContext
	if json.Unmarshal(referencesJSON, &references) != nil || json.Unmarshal(contextsJSON, &contexts) != nil {
		return errors.New("invalid test input")
	}
	return validateAIInvocationDeviceContexts(references, contexts, spaceID)
}
