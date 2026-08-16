package api

import (
	"context"
	"encoding/json"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) githubProviderWriteNode(ctx context.Context, run *db.SpaceRun, invocation workflowv2.Invocation) (json.RawMessage, error) {
	var input struct {
		WorkspaceID string          `json:"workspace_id"`
		Action      string          `json:"action"`
		Payload     json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(invocation.Input, &input) != nil || input.WorkspaceID == "" || input.Action == "" {
		return nil, db.ErrSpaceInvalid
	}
	if err := s.database.RequireSpacePermission(ctx, run.RequestingMemberID, run.SpaceID, db.PermissionIntegrationsManage); err != nil {
		return nil, err
	}
	workspace, err := s.database.GitHubCodeWorkspace(ctx, run.RequestingMemberID, run.SpaceID, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	installation, err := s.database.GitHubAppInstallation(ctx, run.RequestingMemberID, run.SpaceID, workspace.InstallationID)
	if err != nil {
		return nil, err
	}
	if err := githubValidateMutation(input.Action, input.Payload, installation.Permissions, workspace.Permissions); err != nil {
		return nil, err
	}
	provider, err := s.githubAppProvider(installation.InstallationID)
	if err != nil {
		return nil, err
	}
	result, err := provider.Mutate(ctx, input.Action, githubRepositoryFromWorkspace(*workspace), input.Payload)
	errorCode := ""
	if err != nil {
		errorCode = "github_api_error"
	}
	_ = s.database.RecordGitHubMutationAudit(ctx, run.RequestingMemberID, run.SpaceID, workspace.ID, "agent", input.Action, githubMutationTarget(input.Payload), errorCode, true, err == nil)
	if err != nil {
		return nil, err
	}
	return TestingMustAPIRawJSON(map[string]any{"executed": true, "provider": "github", "operation": input.Action, "workspace_id": workspace.ID, "result": result, "approved_by": run.RequestingMemberID}), nil
}
