package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

var errAIInvocationDeviceWait = errors.New("ai_invocation_device_wait")

func (s *SpacesService) aiBrowserDeviceWait(ctx context.Context, access *mcpRuntimeAccess, call agentRuntimeToolCall, begin bool) error {
	var input map[string]any
	if json.Unmarshal(call.Arguments, &input) != nil {
		return db.ErrSpaceInvalid
	}
	scope, _ := input["scopeId"].(string)
	canonical, _ := json.Marshal(input)
	digest := sha256.Sum256(canonical)
	waiting, err := s.database.AIInvocationDeviceWait(ctx, access.record.UserID, access.record.ID, call.RuntimeRunID, call.CallID, call.DeviceHookToken, scope, call.Name, hex.EncodeToString(digest[:]), begin)
	if err != nil {
		return err
	}
	if waiting {
		return errAIInvocationDeviceWait
	}
	return nil
}
