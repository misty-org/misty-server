package api

import (
	"encoding/json"
	"errors"
	"strings"
)

func validateAIInvocationDeviceContexts(references []aiContextReference, contexts []aiInvocationDeviceContext, spaceID string) error {
	if len(contexts) > 2 {
		return errors.New("at most two browser workspaces can be attached")
	}
	if len(contexts) > 0 && strings.TrimSpace(spaceID) == "" {
		return errors.New("browser workspaces require a bound Space")
	}
	seen := map[string]bool{}
	for _, deviceContext := range contexts {
		deviceContext.DeviceID = strings.TrimSpace(deviceContext.DeviceID)
		deviceContext.Kind = strings.TrimSpace(deviceContext.Kind)
		deviceContext.OpaqueRef = strings.TrimSpace(deviceContext.OpaqueRef)
		if deviceContext.DeviceID == "" || deviceContext.Kind != "browser_tab" || deviceContext.OpaqueRef == "" || seen[deviceContext.OpaqueRef] {
			return errors.New("browser workspace identity is invalid")
		}
		seen[deviceContext.OpaqueRef] = true
		var capabilities []string
		if json.Unmarshal(deviceContext.Capabilities, &capabilities) != nil || len(capabilities) == 0 {
			return errors.New("browser workspace capabilities are invalid")
		}
		matched := false
		for _, reference := range references {
			if reference.Kind == "browser-tab" && reference.Privacy == "device" && reference.Attached && reference.OpaqueScopeID == deviceContext.OpaqueRef {
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
