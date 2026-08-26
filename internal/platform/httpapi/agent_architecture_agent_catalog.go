package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

// AgentRunHistory preserves access to historical work without exposing a
// custom-Agent execution path.
func (s *SpacesService) AgentRunHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID")
		items, err := s.database.SpaceRuns(r.Context(), userID, spaceID, agentID, 100)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": items})
	}
}

func (s *SpacesService) executeCanonicalAgentRun(r *http.Request, run *db.SpaceRun, prompt string) (*db.SpaceRun, error) {
	resource, workflow, err := s.database.AgentExecutionContext(r.Context(), run.RequestingMemberID, run.SpaceID, run.AgentID, run.AgentVersionID, run.WorkflowVersionID)
	if err != nil {
		return s.finishFailedCanonicalRun(r.Context(), run, err)
	}
	var selectedCapability *db.WorkflowCapability
	if workflow != nil {
		for index := range workflow.Metadata.Capabilities {
			if workflow.Metadata.Capabilities[index].ID == run.CapabilityID {
				selectedCapability = &workflow.Metadata.Capabilities[index]
				break
			}
		}
		if selectedCapability == nil {
			return s.finishFailedCanonicalRun(r.Context(), run, db.ErrSpaceInvalid)
		}
	}
	// Published workflows always use the unified coordinator, including Agents
	// that also have device resources. Device-bound nodes are leased by the v2
	// runtime; the legacy whole-Agent device planner is never used for them.
	if workflow != nil {
		return s.executeWorkflowV2(r.Context(), run, resource, workflow, prompt)
	}
	if resource.RuntimeKind == "device" {
		return s.finishFailedCanonicalRun(r.Context(), run, errors.New("device-backed Space Agents require a published v2 workflow with an exact device node"))
	}
	if s.agent == nil {
		return s.finishFailedCanonicalRun(r.Context(), run, errors.New("Space Agent runtime is unavailable"))
	}
	spaceContext, err := s.database.PersonalAgentSpaceContext(r.Context(), run.RequestingMemberID, run.SpaceID, defaultSpaceContextSections)
	if err != nil {
		return s.finishFailedCanonicalRun(r.Context(), run, err)
	}
	identity := fmt.Sprintf("You are %s, an Agent in a Space. Follow these version-pinned instructions:\n%s", resource.Name, resource.Instructions)
	if selectedCapability != nil {
		capabilityDescription := selectedCapability.Name + ": " + selectedCapability.Description
		identity += fmt.Sprintf("\n\nExecute the pinned workflow capability %s. Use only its capability envelope.", capabilityDescription)
	}
	request := strings.TrimSpace(prompt)
	toolbox, invocation, manifest, err := s.resolveCanonicalAgentToolbox(r.Context(), run, prompt)
	if err != nil {
		return s.finishFailedCanonicalRun(r.Context(), run, err)
	}
	identity += "\n\nPermission-checked Misty Space context:\n" + spaceContext + "\n\n" + agentToolboxPromptContext(manifest, manifestToolNames(manifest))
	completion, err := s.agent.CompleteWithToolsContext(r.Context(), run.RequestingMemberID, run.BillingUserID, identity, request, serveragent.TierLow, manifest, func(toolCtx context.Context, tool serveragent.ToolRequest) (json.RawMessage, error) {
		return executeCanonicalAgentToolbox(toolCtx, toolbox, invocation, s.database, tool)
	})
	if err != nil {
		if errors.Is(err, workflowv2.ErrAwaitingApproval) {
			return s.database.SpaceRun(r.Context(), run.RequestingMemberID, run.ID)
		}
		code, message := spaceRunFailureFromError(err)
		destructive := selectedCapability != nil && selectedCapability.Destructive
		_ = s.database.RecordRunAction(r.Context(), run.ID, "capability", "Failed "+run.CapabilityID, TestingMustAPIRawJSON(map[string]string{"workflow_version": run.WorkflowVersion, "error_code": code, "message": message}), destructive, "failed")
		return s.finishFailedCanonicalRun(r.Context(), run, err)
	}
	result := TestingMustAPIRawJSON(map[string]any{"text": strings.TrimSpace(completion.Text), "citations": completion.Citations, "tool_calls": completion.ToolCalls})
	destructive := selectedCapability != nil && selectedCapability.Destructive
	_ = s.database.RecordRunAction(r.Context(), run.ID, "capability", "Executed "+run.CapabilityID, TestingMustAPIRawJSON(map[string]string{"workflow_version": run.WorkflowVersion}), destructive, "completed")
	return s.database.FinishSpaceRun(r.Context(), run.ID, "completed", result, "")
}
