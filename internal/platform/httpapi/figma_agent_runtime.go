package api

import (
	"context"
	"encoding/json"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) figmaProviderWriteNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var input struct {
		BindingID string `json:"binding_id"`
		FileKey   string `json:"file_key"`
		Message   string `json:"message"`
		NodeID    string `json:"node_id"`
	}
	if json.Unmarshal(invocation.Input, &input) != nil {
		return nil, db.ErrSpaceInvalid
	}
	input.Message = strings.TrimSpace(input.Message)
	input.NodeID = strings.TrimSpace(input.NodeID)
	if input.BindingID == "" || input.Message == "" || len([]rune(input.Message)) > 5000 || len(input.NodeID) > 256 {
		return nil, db.ErrSpaceInvalid
	}
	if err := s.database.RequireSpacePermission(ctx, run.RequestingMemberID, run.SpaceID, db.PermissionIntegrationsManage); err != nil {
		return nil, err
	}
	binding, err := s.database.FigmaBinding(ctx, run.RequestingMemberID, run.SpaceID, input.BindingID)
	if err != nil {
		return nil, err
	}
	fileKey := binding.FileKey
	if binding.ResourceType == "project" {
		fileKey = strings.TrimSpace(input.FileKey)
	}
	allowed, err := s.database.FigmaBindingContainsFile(ctx, binding, fileKey)
	if err != nil || !allowed {
		return nil, db.ErrSpaceForbidden
	}
	idempotencyKey := invocation.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = githubFingerprint(map[string]any{"run_id": invocation.RunID, "node_id": invocation.NodeID, "file_key": fileKey, "message": input.Message})
	}
	fingerprint := githubFingerprint(map[string]any{"file_key": fileKey, "message": input.Message, "node_id": input.NodeID})
	claimed, err := s.database.ClaimFigmaCommentAction(ctx, run.RequestingMemberID, run.SpaceID, binding.ID, "agent", fileKey, input.NodeID, idempotencyKey, fingerprint)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return TestingMustAPIRawJSON(map[string]any{"executed": false, "duplicate": true, "provider": "figma", "binding_id": binding.ID}), nil
	}
	token, _, err := s.connectedAccountAccessTokenForCapability(ctx, binding.BoundByUserID, binding.ConnectionID, "drawings_comments")
	if err != nil {
		_ = s.database.FinishFigmaCommentAction(ctx, binding.ID, idempotencyKey, "reauthorization_required", false)
		return nil, err
	}
	comment, err := s.figmaProvider(token).PostComment(ctx, fileKey, input.Message, input.NodeID)
	if err != nil {
		_ = s.database.FinishFigmaCommentAction(ctx, binding.ID, idempotencyKey, "figma_api_error", false)
		return nil, err
	}
	_ = s.database.FinishFigmaCommentAction(ctx, binding.ID, idempotencyKey, "", true)
	_ = s.database.UpsertFigmaContentRecord(ctx, normalizeFigmaCommentRecord(binding.ID, fileKey, comment, "agent"))
	return TestingMustAPIRawJSON(map[string]any{
		"executed": true, "provider": "figma", "binding_id": binding.ID,
		"file_key": fileKey, "comment": comment, "approved_by": run.RequestingMemberID,
	}), nil
}
