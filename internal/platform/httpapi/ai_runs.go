package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
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
			ConversationID string               `json:"conversation_id,omitempty"`
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
		invocationID := strings.TrimSpace(body.InvocationID)
		conversationID := strings.TrimSpace(body.ConversationID)
		if invocationID == "" && conversationID != "" {
			invocationBody := aiInvocationInput{
				Mode: "drawer", SurfaceID: body.Origin.SurfaceID, Trigger: "message", Prompt: body.Prompt,
				Context: body.Context, ConversationID: conversationID,
				IdempotencyKey: "agent-run:" + body.IdempotencyKey, Timezone: "UTC",
			}
			payload, marshalErr := json.Marshal(invocationBody)
			if marshalErr != nil {
				writeSpaceError(w, marshalErr)
				return
			}
			stored, _, createErr := s.database.CreateAIInvocationRecord(r.Context(), db.AIInvocationRecord{
				ID: "invocation_" + uuid.NewString(), UserID: userID, ConversationID: conversationID,
				SurfaceID: body.Origin.SurfaceID, Mode: "drawer", Trigger: "message", State: "queued",
				IdempotencyKey: invocationBody.IdempotencyKey, RequestPayload: payload,
				ExpiresAt: time.Now().UTC().Add(aiInvocationTTL),
			})
			if createErr != nil {
				writeSpaceError(w, createErr)
				return
			}
			invocationID = stored.ID
		}
		if conversationID != "" {
			if err := s.database.RenameAgentSession(
				r.Context(),
				userID,
				conversationID,
				cleanMistyTitle(body.Prompt),
			); err != nil {
				writeSpaceError(w, err)
				return
			}
		}
		space, err := s.managedMistyRunSpace(r.Context(), userID, strings.TrimSpace(body.SpaceID), body.Context)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		if conversationID != "" {
			bound, boundErr := s.database.AgentConversationIdentity(r.Context(), userID, conversationID)
			if boundErr != nil || bound.AgentID != "" || conversationSpaceChanged(bound.SpaceID, space.ID) {
				writeJSON(w, http.StatusConflict, map[string]any{"code": "conversation_context_changed", "message": "Start a new conversation to work in a different Space."})
				return
			}
			if bound.SpaceID == "" {
				if bindErr := s.database.BindMistyConversationSpace(r.Context(), userID, conversationID, space.ID); bindErr != nil {
					writeMistyConversationBindingError(w, bindErr)
					return
				}
			}
		}
		misty, err := s.database.EnsureManagedMistyAgent(r.Context(), userID, serveragent.InitialSelectedModelID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		run, err := s.database.CreateCreatorAgentRun(r.Context(), userID, space.ID, misty.ID, db.CreatorAgentRunInput{
			Instruction: delegatedPrompt, Mode: "auto", SourceType: "direct", Timezone: "UTC",
			AIInvocationID: invocationID, AIConversationID: conversationID,
			AIIdempotencyKey: body.IdempotencyKey,
		})
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		decision := &db.RoutingDecision{Selected: &db.RoutingOption{
			SpaceID: space.ID, SpaceName: space.Name, AgentID: misty.ID, AgentName: "Misty",
			CapabilityID: "companion", CapabilityName: "Misty",
		}, Reason: "Misty automatically selected the active Space and managed runtime."}
		if invocationID != "" {
			if err := s.database.LinkAIInvocationAgentRun(r.Context(), userID, invocationID, run.ID); err != nil {
				writeSpaceError(w, err)
				return
			}
		}
		linkedConversationID, err := s.database.LinkAgentRunConversation(r.Context(), userID, run.ID, invocationID)
		if err != nil {
			writeSpaceError(w, err)
			return
		}
		agentsHref := "/agents"
		if linkedConversationID != "" {
			agentsHref += "?conversation=" + url.QueryEscape(linkedConversationID)
		} else {
			agentsHref += "?space=" + url.QueryEscape(space.ID) + "&run=" + url.QueryEscape(run.ID)
		}
		if run.IdempotentReplay {
			writeJSON(w, http.StatusOK, map[string]any{"status": run.State, "routing": decision, "run": run, "agents_href": agentsHref, "origin": body.Origin, "replayed": true})
			return
		}
		if run.State == "awaiting_approval" {
			writeJSON(w, http.StatusAccepted, map[string]any{"status": run.State, "routing": decision, "run": run, "agents_href": agentsHref, "origin": body.Origin})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": run.State, "routing": decision, "run": run, "agents_href": agentsHref, "origin": body.Origin})
	}
}

func (s *SpacesService) managedMistyRunSpace(ctx context.Context, userID, requestedSpaceID string, references []aiContextReference) (*db.Space, error) {
	spaceID := strings.TrimSpace(requestedSpaceID)
	if spaceID == "" {
		spaceID = firstAIContextSpace(references)
	}
	if spaceID != "" {
		space, err := s.database.SpaceByID(ctx, userID, spaceID)
		if err != nil {
			return nil, err
		}
		if !space.Permissions[db.PermissionAgentsRun] {
			return nil, db.ErrSpaceForbidden
		}
		return space, nil
	}
	spaces, err := s.database.ListSpaces(ctx, userID)
	if err != nil {
		return nil, err
	}
	var fallback *db.Space
	for i := range spaces {
		if !spaces[i].Permissions[db.PermissionAgentsRun] {
			continue
		}
		if spaces[i].Kind != "misty" {
			return &spaces[i], nil
		}
		if fallback == nil {
			fallback = &spaces[i]
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, db.ErrSpaceForbidden
}
