package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/kannachi323/misty/server/internal/agent"
	"github.com/kannachi323/misty/server/internal/agenttools"
	"github.com/kannachi323/misty/server/internal/integrations/composio"
	"github.com/kannachi323/misty/server/internal/platform/db"
)

type appsCall struct {
	ToolSlug  string          `json:"tool_slug"`
	Arguments json.RawMessage `json:"arguments"`
	Account   string          `json:"account"`
}

// appsExecute runs one app tool. Reads run directly and may be retried. Writes
// are journaled under the call ID with encrypted results; sends, shares,
// deletes and payments first wait for the user when the account asks for that.
func (s *SpacesService) appsExecute(ctx context.Context, invocation agenttools.Invocation, request agent.ToolRequest) (json.RawMessage, error) {
	var call appsCall
	if json.Unmarshal(request.Arguments, &call) != nil || !composio.ValidToolSlug(call.ToolSlug) {
		return nil, agent.ErrInvalidRequest("tool_slug must be an exact slug from integration_search, such as GMAIL_SEND_EMAIL")
	}
	if len(call.Arguments) == 0 || string(call.Arguments) == "null" {
		call.Arguments = json.RawMessage(`{}`)
	}
	call.Account = strings.TrimSpace(call.Account)
	if strings.TrimSpace(request.ID) == "" {
		return nil, agenttools.ErrCapabilityDenied
	}
	client, err := composioClient()
	if err != nil {
		return nil, appsError(err)
	}
	info, err := client.Tool(ctx, call.ToolSlug)
	if composio.NotFound(err) {
		return nil, agent.ErrInvalidRequest("Unknown app tool " + call.ToolSlug + ". Use integration_search to find exact slugs.")
	}
	if err != nil {
		return nil, appsError(err)
	}
	// The call runs as one of the person's own accounts, named explicitly, so
	// Composio never falls back to another of the person's accounts.
	apps, err := s.loadRunApps(ctx, client, invocation)
	if err != nil {
		return nil, appsError(err)
	}
	account, need := apps.account(info.Toolkit.Slug, call.Account)
	if need != appsReady {
		return nil, appsNeedError(apps, info.Toolkit.Slug, need)
	}
	call.Account = account
	kind := composio.Classify(info.Slug, info.Tags)
	bounded, cancel, err := boundedAgentExecutionContext(ctx, s.database, invocation.UserID, invocation.RunID)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if kind == composio.KindRead {
		result, err := s.runAppTool(bounded, invocation.UserID, call)
		return result, appsError(err)
	}
	var approval *db.AgentAppRequest
	if kind != composio.KindRead {
		var waiting json.RawMessage
		if approval, waiting, err = s.appsApproval(bounded, invocation, info, call, kind); err != nil || waiting != nil {
			return waiting, err
		}
	}
	effect := "apps:" + invocation.RunID + ":" + request.ID
	return s.database.JournalAgentToolboxAction(ctx, db.AgentToolboxAction{
		IdempotencyKey: effect, RequireSettledRun: invocation.Source == "space_conversation" && invocation.RunID != "",
		UserID: invocation.UserID, SpaceID: invocation.SpaceID, AgentID: invocation.AgentID, AgentInstanceID: invocation.AgentInstanceID,
		RunID: invocation.RunID, SessionID: invocation.SessionID, ToolName: appsExecuteTool, AuditEvent: appsExecuteTool,
		Risk: agent.RiskWrite, Source: invocation.Source, RedactPayload: true,
		Request:       MustAPIRawJSON(map[string]any{"tool_slug": call.ToolSlug, "kind": kind, "arguments_hash": appsArgumentsHash(call), "account": call.Account}),
		ProtectResult: func(value json.RawMessage) ([]byte, error) { return s.protectAgentEffectResult(effect, value) },
		RestoreResult: func(value []byte) (json.RawMessage, error) { return s.restoreAgentEffectResult(effect, value) },
	}, func() (json.RawMessage, error) {
		if approval != nil {
			if used, err := s.database.ResolveAgentAppRequest(bounded, invocation.UserID, approval.ID, "approved", "used"); err != nil || !used {
				return nil, errors.Join(db.ErrAgentToolboxNotAttempted, agent.ErrInvalidRequest("That approval was already used. Call integration_execute again to ask the user again."))
			}
		}
		result, err := s.runAppTool(bounded, invocation.UserID, call)
		if err != nil && composio.Rejected(err) {
			// Composio refused the request before running it.
			return nil, errors.Join(db.ErrAgentToolboxNotAttempted, appsError(err))
		}
		return result, err
	})
}

// runAppTool runs one tool and shapes its result for the model.
func (s *SpacesService) runAppTool(ctx context.Context, user string, call appsCall) (json.RawMessage, error) {
	var execution composio.Execution
	err := s.withAppsSession(ctx, user, func(client *composio.Client, session string) error {
		var err error
		execution, err = client.Execute(ctx, session, call.ToolSlug, call.Arguments, call.Account)
		return err
	})
	if err != nil {
		return nil, err
	}
	if execution.Error != nil && strings.TrimSpace(*execution.Error) != "" {
		message := truncateAgentRuntimeText(*execution.Error, 600)
		if execution.LogID != "" {
			message += " (log " + execution.LogID + ")"
		}
		// The app answered with a failure, so the action changed nothing.
		return nil, errors.Join(db.ErrAgentToolboxNotAttempted, agent.ErrInvalidRequest(message))
	}
	data := execution.Data
	if len(data) == 0 {
		data = json.RawMessage(`null`)
	}
	result := map[string]any{"tool": call.ToolSlug, "log_id": execution.LogID}
	if len(data) <= appsResultLimit {
		result["data"] = data
	} else {
		result["data_preview"] = truncateAgentRuntimeText(string(data), appsResultLimit)
		result["note"] = "The result is larger than Misty passes to the model. Narrow the request: fewer items, a date range or specific fields."
	}
	return json.Marshal(result)
}

// appsArgumentsHash binds an approval to one exact tool, arguments and account.
func appsArgumentsHash(call appsCall) string {
	var arguments any
	_ = json.Unmarshal(call.Arguments, &arguments)
	canonical, _ := json.Marshal([]any{call.ToolSlug, arguments, call.Account})
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}
