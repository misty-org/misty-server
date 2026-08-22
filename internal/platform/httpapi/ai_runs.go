package api

import (
	"net/http"
	"net/url"
	"strings"

	serveragent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

type aiRunOrigin struct {
	SurfaceID    string `json:"surface_id"`
	PaneID       string `json:"pane_id,omitempty"`
	InvocationID string `json:"invocation_id,omitempty"`
	Href         string `json:"href,omitempty"`
	Title        string `json:"title,omitempty"`
}

// AIRuns bridges an embedded Misty result into the existing audited Agent run
// system. Embedded surfaces therefore do not create a shadow execution history.
func (s *SpacesService) AIRuns() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := authenticatedUser(w, r, s.database)
		if !ok {
			return
		}
		var body struct {
			Prompt         string               `json:"prompt"`
			SpaceID        string               `json:"space_id,omitempty"`
			AgentID        string               `json:"agent_id,omitempty"`
			CapabilityID   string               `json:"capability_id,omitempty"`
			InvocationID   string               `json:"invocation_id,omitempty"`
			IdempotencyKey string               `json:"idempotency_key"`
			Origin         aiRunOrigin          `json:"origin"`
			Context        []aiContextReference `json:"context,omitempty"`
		}
		if decodeJSON(w, r, &body) != nil {
			return
		}
		body.Prompt = strings.TrimSpace(body.Prompt)
		if header := strings.TrimSpace(r.Header.Get("Idempotency-Key")); header != "" {
			body.IdempotencyKey = header
		} else {
			body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
		}
		body.Origin.SurfaceID = strings.TrimSpace(body.Origin.SurfaceID)
		if body.Prompt == "" || len([]rune(body.Prompt)) > maxAIInvocationPrompt || body.IdempotencyKey == "" || len(body.IdempotencyKey) > 200 || body.Origin.SurfaceID == "" {
			writeSpaceError(w, db.ErrSpaceInvalid)
			return
		}
		available, err := s.database.AIActionAvailable(r.Context(), userID, body.Origin.SurfaceID, "handoff", serveragent.InitialSelectedModelID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if !available {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "ai_surface_unavailable", "message": "Agent handoff is temporarily unavailable on this surface."})
			return
		}
		resolved, err := (aiContextBroker{database: s.database}).resolve(r.Context(), userID, body.Context)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		delegatedPrompt := aiContextPrompt(body.Prompt, resolved, nil, body.Context)
		decision, err := s.database.RouteAgentRequest(r.Context(), userID, body.Prompt, strings.TrimSpace(body.SpaceID), strings.TrimSpace(body.AgentID), strings.TrimSpace(body.CapabilityID))
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if decision.NeedsClarification || decision.Selected == nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "needs_clarification", "routing": decision})
			return
		}
		citations := make([]aiCitation, 0, len(resolved))
		for _, item := range resolved {
			citations = append(citations, item.Citation)
		}
		input := TestingMustAPIRawJSON(map[string]any{
			"prompt": delegatedPrompt, "user_prompt": body.Prompt, "origin": body.Origin,
			"citations": citations, "ai_invocation_id": strings.TrimSpace(body.InvocationID),
			"ai_idempotency_key": body.IdempotencyKey,
		})
		envelope := TestingMustAPIRawJSON(map[string]any{
			"source": "ai_surface", "origin": body.Origin, "ai_invocation_id": strings.TrimSpace(body.InvocationID),
		})
		run, err := s.database.CreateAgentRun(r.Context(), db.AgentRunRequest{
			RequestingMemberID: userID, SpaceID: decision.Selected.SpaceID, AgentID: decision.Selected.AgentID,
			SourceType: db.RunSourceAgentConsole, CapabilityID: decision.Selected.CapabilityID,
			Input: input, TriggerKind: db.RunSourceAgentConsole, ActionEnvelope: envelope,
		})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		agentsHref := "/agents?agent=" + url.QueryEscape(decision.Selected.AgentID) + "&space=" + url.QueryEscape(decision.Selected.SpaceID) + "&run=" + url.QueryEscape(run.ID)
		if run.IdempotentReplay {
			writeJSON(w, http.StatusOK, map[string]any{"status": run.State, "routing": decision, "run": run, "agents_href": agentsHref, "origin": body.Origin, "replayed": true})
			return
		}
		if run.State == "awaiting_approval" {
			writeJSON(w, http.StatusAccepted, map[string]any{"status": run.State, "routing": decision, "run": run, "agents_href": agentsHref, "origin": body.Origin})
			return
		}
		finished, err := s.executeCanonicalAgentRun(r, run, delegatedPrompt)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": finished.State, "routing": decision, "run": finished, "agents_href": agentsHref, "origin": body.Origin})
	}
}
