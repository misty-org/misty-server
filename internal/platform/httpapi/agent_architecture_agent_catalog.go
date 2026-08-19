package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	db "github.com/kannachi323/misty/server/internal/platform/postgres"

	"github.com/go-chi/chi/v5"
	serveragent "github.com/kannachi323/misty/server/internal/agents"
	workflowv2 "github.com/kannachi323/misty/server/internal/workflows"
)

func (s *SpacesService) AgentCatalog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		items, err := s.database.DiscoverAgentCatalog(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": items})
	}
}

func (s *SpacesService) AgentDiscovery() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaces, err := s.database.ListSpaces(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		agents, err := s.database.DiscoverAgentCatalog(r.Context(), userID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces, "agents": agents})
	}
}

func (s *SpacesService) AgentDelegation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Prompt               string          `json:"prompt"`
			SpaceID              string          `json:"space_id"`
			AgentID              string          `json:"agent_id"`
			CapabilityID         string          `json:"capability_id"`
			SourceConversationID string          `json:"source_conversation_id"`
			Input                json.RawMessage `json:"input"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Prompt = strings.TrimSpace(body.Prompt)
		if body.Prompt == "" {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		decision, err := s.database.RouteAgentRequest(r.Context(), userID, body.Prompt, body.SpaceID, body.AgentID, body.CapabilityID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if decision.NeedsClarification || decision.Selected == nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "needs_clarification", "routing": decision})
			return
		}
		if len(body.Input) == 0 {
			body.Input = TestingMustAPIRawJSON(map[string]string{"prompt": body.Prompt})
		}
		run, err := s.database.CreateAgentRun(r.Context(), db.AgentRunRequest{
			RequestingMemberID: userID, SpaceID: decision.Selected.SpaceID, AgentID: decision.Selected.AgentID,
			SourceConversationID: body.SourceConversationID, SourceType: db.RunSourceAgentConsole, CapabilityID: decision.Selected.CapabilityID,
			Input: body.Input, TriggerKind: db.RunSourceAgentConsole,
		})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		trace := fmt.Sprintf("The agent system assigned this task to %s in %s.", decision.Selected.AgentName, decision.Selected.SpaceName)
		if run.State == "awaiting_approval" {
			writeJSON(w, http.StatusAccepted, map[string]any{"status": "awaiting_approval", "trace": trace, "routing": decision, "run": run})
			return
		}
		finished, err := s.executeCanonicalAgentRun(r, run, body.Prompt)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": finished.State, "trace": trace, "routing": decision, "run": finished})
	}
}

func (s *SpacesService) DirectAgentRun() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		spaceID, agentID := chi.URLParam(r, "spaceID"), chi.URLParam(r, "agentID")
		if r.Method == http.MethodGet {
			items, err := s.database.SpaceRuns(r.Context(), userID, spaceID, agentID, 100)
			if err != nil {
				writeSpaceError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"runs": items})
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body db.CreatorAgentRunInput
		if decodeJSON(w, r, &body) != nil {
			return
		}
		run, err := s.database.CreateCreatorAgentRun(r.Context(), userID, spaceID, agentID, body)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, run)
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
