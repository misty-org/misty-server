package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) appendPersonalAgentMCPTools(ctx context.Context, userID, agentID string, registrations []agenttools.Registration, requested []string, handler agenttools.Handler) ([]agenttools.Registration, []string) {
	tools, err := s.database.EnabledPersonalAgentMCPTools(ctx, userID, agentID)
	if err != nil {
		return registrations, requested
	}
	for _, tool := range tools {
		registrations = append(registrations, agenttools.Registration{Descriptor: mcpAgentToolDescriptor(tool), Handler: handler})
		requested = append(requested, tool.StableName)
	}
	return registrations, requested
}

func mcpAgentToolDescriptor(tool db.MCPAgentToolBinding) agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: tool.StableName, Version: 1, Description: tool.Description,
		Risk: serveragent.RiskWrite, InputSchema: tool.InputSchema, OutputSchema: agentToolObjectOutputSchema(),
		Approval: agenttools.ApprovalInteractive, Locality: agenttools.LocalityProvider, Idempotent: false,
		AuditEvent: "mcp.tool.execute", AllowCustomAgent: true, Sources: []string{canonicalAgentToolSource, "task_assignment", "space_conversation"},
	}
}

func authorizeMCPAgentTool(ctx context.Context, database *db.Database, invocation agenttools.Invocation, descriptor agenttools.Descriptor) (bool, error) {
	if !strings.HasPrefix(descriptor.Name, "mcp.") || invocation.UserID == "" || invocation.AgentID == "" {
		return false, nil
	}
	_, err := database.PersonalAgentMCPToolForExecution(ctx, invocation.UserID, invocation.AgentID, descriptor.Name)
	if errors.Is(err, db.ErrSpaceNotFound) || errors.Is(err, db.ErrPersonalAgentNotFound) || errors.Is(err, db.ErrSpaceForbidden) {
		return false, nil
	}
	return err == nil, err
}

func (s *SpacesService) executeMCPAgentTool(ctx context.Context, run *db.SpaceRun, tool serveragent.ToolRequest, workflowApproval bool, source string) (json.RawMessage, error) {
	item, err := s.database.PersonalAgentMCPToolForExecution(ctx, run.RequestingMemberID, run.AgentID, tool.Name)
	if err != nil || !item.ConnectionUp {
		return nil, workflowv2.ErrCapabilityDenied
	}
	idempotencyKey := "mcp:" + run.ID + ":" + tool.ID
	if source == "" {
		source = canonicalAgentToolSource
	}
	if workflowApproval {
		approved, approvalErr := s.database.EnsureWorkflowNodeApproval(ctx, run.ID, "mcp_tool_"+tool.ID, tool.Name, tool.Arguments)
		if approvalErr != nil {
			return nil, approvalErr
		}
		if !approved {
			_ = s.database.RecordMCPExecutionAudit(ctx, db.MCPExecutionAudit{OwnerUserID: run.RequestingMemberID, AgentID: run.AgentID, ConnectionID: item.ConnectionID, RemoteToolID: item.RemoteToolID, RemoteName: item.RemoteName, StableName: item.StableName, RunID: run.ID, IdempotencyKey: idempotencyKey + ":approval", Source: source, Approved: false, Success: false, ErrorCode: "approval_required"})
			return nil, workflowv2.ErrAwaitingApproval
		}
	}
	bearer, err := s.decryptMCPBearer(item.BearerCipher, item.BearerNonce)
	if err != nil {
		return nil, workflowv2.ErrCapabilityDenied
	}
	started := time.Now()
	var resultJSON json.RawMessage
	resultJSON, callErr := s.database.JournalAgentToolboxAction(ctx, db.AgentToolboxAction{
		IdempotencyKey: idempotencyKey, UserID: run.RequestingMemberID, SpaceID: run.SpaceID,
		AgentID: run.AgentID, RunID: run.ID, ToolName: item.StableName,
		AuditEvent: "mcp.tool.execute", Risk: serveragent.RiskWrite, Source: source,
		Request: tool.Arguments, RedactPayload: true,
	}, func() (json.RawMessage, error) {
		result, remoteErr := s.mcpConnectorClient.CallTool(ctx, item.EndpointURL, bearer, item.RemoteName, tool.Arguments)
		if remoteErr != nil {
			return nil, remoteErr
		}
		return TestingMustAPIRawJSON(map[string]any{"provider": "mcp", "connection_id": item.ConnectionID, "remote_name": item.RemoteName, "stable_name": item.StableName, "text": result.Text, "structured_content": result.StructuredContent}), nil
	})
	errorCode := ""
	if callErr != nil {
		errorCode = mcpErrorCode(callErr)
	}
	_ = s.database.RecordMCPExecutionAudit(ctx, db.MCPExecutionAudit{OwnerUserID: run.RequestingMemberID, AgentID: run.AgentID, ConnectionID: item.ConnectionID, RemoteToolID: item.RemoteToolID, RemoteName: item.RemoteName, StableName: item.StableName, RunID: run.ID, IdempotencyKey: idempotencyKey, Source: source, Approved: true, Success: callErr == nil, ErrorCode: errorCode, DurationMS: int(time.Since(started).Milliseconds())})
	if callErr != nil {
		return nil, callErr
	}
	return resultJSON, nil
}

func (s *SpacesService) TestingExecuteMCPAgentTool(ctx context.Context, run *db.SpaceRun, tool serveragent.ToolRequest, workflowApproval bool, source string) (json.RawMessage, error) {
	return s.executeMCPAgentTool(ctx, run, tool, workflowApproval, source)
}
