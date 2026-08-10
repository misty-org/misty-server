package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	"github.com/kannachi323/misty/server/internal/agenttools"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func agentToolboxExecutionJournal(database *db.Database) agenttools.ExecutionMiddleware {
	return func(ctx context.Context, invocation agenttools.Invocation, descriptor agenttools.Descriptor, request serveragent.ToolRequest, next agenttools.Handler) (json.RawMessage, error) {
		if descriptor.Risk == serveragent.RiskRead {
			return next(ctx, invocation, request)
		}
		if database == nil || strings.TrimSpace(invocation.UserID) == "" || strings.TrimSpace(descriptor.AuditEvent) == "" || strings.TrimSpace(invocation.RunID) == "" && strings.TrimSpace(invocation.SessionID) == "" {
			return nil, workflowv2.ErrCapabilityDenied
		}
		spaceID := strings.TrimSpace(invocation.SpaceID)
		if spaceID == "" {
			var dynamicTarget struct {
				SpaceID string `json:"space_id"`
			}
			if json.Unmarshal(request.Arguments, &dynamicTarget) == nil {
				spaceID = strings.TrimSpace(dynamicTarget.SpaceID)
			}
		}
		identity := strings.Join([]string{
			invocation.UserID, spaceID, invocation.AgentID, invocation.AgentInstanceID,
			invocation.RunID, invocation.SessionID, invocation.Source, invocation.Trigger,
			descriptor.Name, request.ID, string(request.Arguments),
		}, "\x00")
		digest := sha256.Sum256([]byte(identity))
		return database.JournalAgentToolboxAction(ctx, db.AgentToolboxAction{
			IdempotencyKey: "toolbox:" + hex.EncodeToString(digest[:]),
			UserID:         invocation.UserID, SpaceID: spaceID, AgentID: invocation.AgentID,
			AgentInstanceID: invocation.AgentInstanceID, RunID: invocation.RunID, SessionID: invocation.SessionID, ToolName: descriptor.Name,
			AuditEvent: descriptor.AuditEvent, Risk: descriptor.Risk, Source: invocation.Source, Request: request.Arguments,
		}, func() (json.RawMessage, error) {
			return next(ctx, invocation, request)
		})
	}
}
