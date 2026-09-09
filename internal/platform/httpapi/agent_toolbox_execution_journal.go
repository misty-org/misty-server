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
		boundedNext := func() (json.RawMessage, error) {
			executionCtx, cancel, err := boundedAgentExecutionContext(ctx, database, invocation.UserID, invocation.RunID)
			if err != nil {
				return nil, err
			}
			defer cancel()
			return next(executionCtx, invocation, request)
		}
		// MCP owns this journal with encrypted replay results. A redacted outer
		// journal would lose the response on retries.
		if strings.HasPrefix(descriptor.Name, "mcp.") {
			return next(ctx, invocation, request)
		}
		if descriptor.Risk == serveragent.RiskRead && !strings.HasPrefix(descriptor.Name, "browser.") {
			return boundedNext()
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
		if strings.TrimSpace(request.ID) == "" {
			return nil, workflowv2.ErrCapabilityDenied
		}
		// The logical call owns its effect identity. Changed arguments must
		// conflict with that effect, never allocate another mutation.
		identity := strings.Join([]string{invocation.UserID, invocation.RunID, invocation.SessionID, request.ID}, "\x00")
		legacyIdentity := strings.Join([]string{invocation.UserID, spaceID, invocation.AgentID, invocation.AgentInstanceID, invocation.RunID, invocation.SessionID, invocation.Source, invocation.Trigger, descriptor.Name, request.ID, string(request.Arguments)}, "\x00")
		legacyDigest := sha256.Sum256([]byte(legacyIdentity))
		digest := sha256.Sum256([]byte(identity))
		return database.JournalAgentToolboxAction(ctx, db.AgentToolboxAction{
			IdempotencyKey:       "toolbox:v2:" + hex.EncodeToString(digest[:]),
			RequireSettledRun:    invocation.Source == "space_conversation" && invocation.RunID != "",
			LegacyIdempotencyKey: "toolbox:" + hex.EncodeToString(legacyDigest[:]),
			UserID:               invocation.UserID, SpaceID: spaceID, AgentID: invocation.AgentID,
			AgentInstanceID: invocation.AgentInstanceID, RunID: invocation.RunID, SessionID: invocation.SessionID, ToolName: descriptor.Name,
			AuditEvent: descriptor.AuditEvent, Risk: descriptor.Risk, Source: invocation.Source, Request: request.Arguments,
			RedactPayload: strings.HasPrefix(descriptor.Name, "mcp."),
		}, func() (json.RawMessage, error) {
			return boundedNext()
		})
	}
}
