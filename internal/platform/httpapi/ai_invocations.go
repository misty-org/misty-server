package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	agent "github.com/kannachi323/misty/server/internal/agents"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const (
	aiInvocationTTL       = 24 * time.Hour
	maxAIInvocationPrompt = 32 << 10
)

type aiSelectionSnapshot struct {
	Kind        string         `json:"kind"`
	Content     string         `json:"content,omitempty"`
	Object      map[string]any `json:"object"`
	Anchors     map[string]any `json:"anchors,omitempty"`
	ContentHash string         `json:"contentHash"`
}

type aiCaptureAttachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MimeType    string `json:"mime_type"`
	DataURL     string `json:"data_url"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ContentHash string `json:"content_hash"`
}

type aiInvocationDeviceContext struct {
	DeviceID     string          `json:"device_id"`
	Kind         string          `json:"kind"`
	OpaqueRef    string          `json:"opaque_ref"`
	DisplayName  string          `json:"display_name,omitempty"`
	Capabilities json.RawMessage `json:"capabilities"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

type aiInvocationInput struct {
	Mode                  string                      `json:"mode"`
	SurfaceID             string                      `json:"surface_id"`
	Trigger               string                      `json:"trigger"`
	Prompt                string                      `json:"prompt"`
	Context               []aiContextReference        `json:"context"`
	Selection             *aiSelectionSnapshot        `json:"selection,omitempty"`
	Capture               *aiCaptureAttachment        `json:"capture,omitempty"`
	AttachmentIDs         []string                    `json:"attachment_ids,omitempty"`
	DeviceContexts        []aiInvocationDeviceContext `json:"device_contexts,omitempty"`
	ModelID               string                      `json:"model_id,omitempty"`
	ReasoningEffort       string                      `json:"reasoning_effort,omitempty"`
	RequestedArtifactKind string                      `json:"requested_artifact_kind,omitempty"`
	ConversationID        string                      `json:"conversation_id,omitempty"`
	AgentID               string                      `json:"agent_id,omitempty"`
	IdempotencyKey        string                      `json:"idempotency_key"`
	Timezone              string                      `json:"timezone,omitempty"`
}

type aiInvocationEvent struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	State      string      `json:"state,omitempty"`
	Delta      string      `json:"delta,omitempty"`
	Text       string      `json:"text,omitempty"`
	Phase      string      `json:"phase,omitempty"`
	Summary    string      `json:"summary,omitempty"`
	ArtifactID string      `json:"artifactId,omitempty"`
	RunID      string      `json:"runId,omitempty"`
	ToolCallID string      `json:"toolCallId,omitempty"`
	ToolName   string      `json:"toolName,omitempty"`
	Citation   *aiCitation `json:"citation,omitempty"`
	Artifact   *aiArtifact `json:"artifact,omitempty"`
	Error      string      `json:"error,omitempty"`
}

type aiArtifact struct {
	ID             string         `json:"id"`
	SchemaVersion  int            `json:"schemaVersion"`
	Kind           string         `json:"kind"`
	Title          string         `json:"title"`
	Summary        string         `json:"summary"`
	Sources        []aiCitation   `json:"sources"`
	Target         map[string]any `json:"target,omitempty"`
	BaseRevision   any            `json:"baseRevision,omitempty"`
	Operations     map[string]any `json:"operations"`
	Risk           string         `json:"risk"`
	ApprovalPolicy string         `json:"approvalPolicy"`
	IdempotencyKey string         `json:"idempotencyKey"`
	ExpiresAt      string         `json:"expiresAt"`
	State          string         `json:"state"`
	Error          string         `json:"error,omitempty"`
	InvocationID   string         `json:"invocationId,omitempty"`
	OwnerUserID    string         `json:"-"`
}

type aiInvocationRecord struct {
	ID             string
	OwnerUserID    string
	ConversationID string
	State          string
	Events         []aiInvocationEvent
	CreatedAt      time.Time
	ExpiresAt      time.Time
	Notify         chan struct{}
}

type aiInvocationHub struct {
	mu          sync.Mutex
	invocations map[string]*aiInvocationRecord
	artifacts   map[string]*aiArtifact
	idempotency map[string]string
	database    *db.Database
}

func newAIInvocationHub(databases ...*db.Database) *aiInvocationHub {
	var database *db.Database
	if len(databases) > 0 {
		database = databases[0]
	}
	return &aiInvocationHub{invocations: map[string]*aiInvocationRecord{}, artifacts: map[string]*aiArtifact{}, idempotency: map[string]string{}, database: database}
}

func (s *AIService) CreateInvocation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		var body aiInvocationInput
		if err := decodeAIJSON(w, r, &body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := validateAIInvocationInput(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_invocation", "message": err.Error()})
			return
		}
		if body.AgentID != "" {
			writeJSON(w, http.StatusGone, map[string]any{
				"code": "custom_agents_retired", "message": "Misty is now the only assistant. Existing Agent tasks remain available as read-only history.",
			})
			return
		}
		if header := strings.TrimSpace(r.Header.Get("Idempotency-Key")); header != "" {
			if body.IdempotencyKey != "" && body.IdempotencyKey != header {
				http.Error(w, "idempotency key mismatch", http.StatusBadRequest)
				return
			}
			body.IdempotencyKey = header
		}
		if body.IdempotencyKey == "" {
			http.Error(w, "idempotency key is required", http.StatusBadRequest)
			return
		}
		actionID := body.RequestedArtifactKind
		if actionID == "" {
			actionID = "ask"
		}
		if !s.agentRuntime.Enabled() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "agent_runtime_unavailable", "message": "Misty's agent runtime is not configured."})
			return
		}
		conversationID := strings.TrimSpace(body.ConversationID)
		var err error
		modelFallbackNotice := false
		modelID := strings.TrimSpace(body.ModelID)
		if modelID == "" {
			modelID = agent.FrontierDefaultModelID()
		}
		reasoning := strings.ToLower(strings.TrimSpace(body.ReasoningEffort))
		spaceID := firstAIContextSpace(body.Context)
		if spaceID != "" {
			if _, spaceErr := s.database.SpaceByID(r.Context(), userID, spaceID); spaceErr != nil {
				writeSpaceError(w, spaceErr)
				return
			}
		}
		if conversationID != "" {
			bound, boundErr := s.database.AgentConversationIdentity(r.Context(), userID, conversationID)
			if boundErr != nil || bound.AgentID != "" || conversationSpaceChanged(bound.SpaceID, spaceID) {
				writeJSON(w, http.StatusConflict, map[string]any{"code": "conversation_context_changed", "message": "Start a new Misty task for this Space."})
				return
			}
			if spaceID == "" {
				spaceID = bound.SpaceID
			} else if bound.SpaceID == "" {
				if bindErr := s.database.BindMistyConversationSpace(r.Context(), userID, conversationID, spaceID); bindErr != nil {
					writeMistyConversationBindingError(w, bindErr)
					return
				}
			}
			modelID, reasoning = bound.ModelID, bound.ReasoningEffort
			if !agent.FrontierModelAvailable(r.Context(), modelID) {
				modelID, reasoning = agent.FrontierDefaultModelID(), ""
				_ = s.database.UpdateMistyConversationModel(r.Context(), userID, conversationID, modelID, reasoning, agent.FrontierModelCatalogVersion)
				modelFallbackNotice = true
			}
		}
		if err := validateAIInvocationDeviceContexts(body.Context, body.DeviceContexts, spaceID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_device_context", "message": err.Error()})
			return
		}
		if conversationID == "" && (body.Mode == "drawer" || body.Mode == "companion") && body.RequestedArtifactKind == "" {
			conversationID, err = s.database.CreateAIConversation(r.Context(), userID, spaceID)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			_ = s.database.RenameAgentSession(r.Context(), userID, conversationID, cleanMistyTitle(body.Prompt))
			_ = s.database.UpdateMistyConversationModel(r.Context(), userID, conversationID, modelID, reasoning, agent.FrontierModelCatalogVersion)
		}
		if conversationID == "" && body.Mode == "companion" {
			conversationID, err = s.database.CreateAIConversation(r.Context(), userID, spaceID)
			if err != nil {
				TestingWriteAIError(w, err)
				return
			}
			_ = s.database.RenameAgentSession(r.Context(), userID, conversationID, cleanMistyTitle(body.Prompt))
			_ = s.database.UpdateMistyConversationModel(r.Context(), userID, conversationID, modelID, reasoning, agent.FrontierModelCatalogVersion)
		}
		if !agent.FrontierModelAvailable(r.Context(), modelID) || !agent.FrontierModelReasoningAvailable(r.Context(), modelID, reasoning) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "model_unavailable", "message": "That model or reasoning level is no longer available. Choose another frontier model."})
			return
		}
		available, err := s.database.AIActionAvailable(r.Context(), userID, body.SurfaceID, actionID, modelID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		if !available {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "ai_surface_unavailable", "message": "Misty is temporarily unavailable for this action."})
			return
		}
		if body.Mode == "companion" && conversationID != "" {
			if err := s.database.BindCompanionConversation(r.Context(), userID, conversationID, body.AgentID, spaceID, modelID, body.SurfaceID, firstAIContextHref(body.Context), aiInvocationPrivacyBoundary(body.Context)); err != nil {
				TestingWriteAIError(w, err)
				return
			}
		}
		body.ConversationID = conversationID
		body.ModelID = modelID
		body.ReasoningEffort = reasoning
		if err := s.database.ValidateAIConversationAttachments(r.Context(), userID, conversationID, body.AttachmentIDs); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_attachment", "message": err.Error()})
			return
		}
		requestPayload, err := json.Marshal(body)
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		stored, created, err := s.database.CreateAIInvocationRecord(r.Context(), db.AIInvocationRecord{
			ID: "invocation_" + uuid.NewString(), UserID: userID, ConversationID: conversationID,
			SurfaceID: body.SurfaceID, Mode: body.Mode, Trigger: body.Trigger, State: "queued",
			IdempotencyKey: body.IdempotencyKey, RequestPayload: requestPayload, ExpiresAt: now.Add(aiInvocationTTL),
		})
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		record, err := s.invocations.restoreDurable(r.Context(), stored)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load invocation stream"})
			return
		}
		if !created {
			writeAIInvocationCreated(w, record)
			return
		}
		if err := s.database.BindAIConversationAttachments(r.Context(), userID, conversationID, stored.ID, body.AttachmentIDs); err != nil {
			s.invocations.fail(stored.ID, "Misty could not attach one of those images. Please remove it and try again.")
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_attachment", "message": err.Error()})
			return
		}
		for _, deviceContext := range body.DeviceContexts {
			if _, err := s.database.AttachAIInvocationContext(
				r.Context(), userID, stored.ID, spaceID, deviceContext.DeviceID, deviceContext.Kind,
				deviceContext.OpaqueRef, deviceContext.DisplayName, deviceContext.Capabilities, deviceContext.Metadata,
			); err != nil {
				s.invocations.fail(stored.ID, "Misty could not connect to its browser workspace. Please keep Misty open and try again.")
				writeJSON(w, http.StatusBadRequest, map[string]any{"code": "invalid_device_context", "message": "Misty could not connect to that browser workspace."})
				return
			}
		}
		if modelFallbackNotice {
			s.invocations.append(stored.ID, aiInvocationEvent{Type: "assistant.status", Text: "The previous model retired, so Misty switched this conversation to the frontier default.", Phase: "model_fallback"})
		}
		if _, err := s.agentRuntime.Start(r.Context(), record.ID); err != nil {
			s.invocations.fail(record.ID, "Misty could not start the agent runtime. Please try again.")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "agent_runtime_start_failed", "message": "Misty could not start the agent runtime."})
			return
		}
		writeAIInvocationCreated(w, record)
	}
}

func conversationSpaceChanged(boundSpaceID, requestedSpaceID string) bool {
	return boundSpaceID != "" && requestedSpaceID != "" && boundSpaceID != requestedSpaceID
}

func TestingConversationSpaceChanged(boundSpaceID, requestedSpaceID string) bool {
	return conversationSpaceChanged(boundSpaceID, requestedSpaceID)
}

func firstAIError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}

func writeAIInvocationCreated(w http.ResponseWriter, record *aiInvocationRecord) {
	writeJSON(w, http.StatusAccepted, map[string]any{
		"invocationId":   record.ID,
		"conversationId": record.ConversationID,
		"state":          record.State,
		"eventsUrl":      "/ai/invocations/" + record.ID + "/events",
	})
}

func (s *AIService) InvocationEvents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		invocationID := strings.TrimSpace(chi.URLParam(r, "invocationID"))
		if _, _, _, found := s.invocations.events(userID, invocationID, 0); !found {
			stored, loadErr := s.database.AIInvocationByID(r.Context(), userID, invocationID)
			if loadErr != nil {
				http.Error(w, "invocation not found", http.StatusNotFound)
				return
			}
			persisted, state, loadErr := s.database.AIInvocationEvents(r.Context(), userID, invocationID, 0)
			if loadErr != nil {
				http.Error(w, "invocation not found", http.StatusNotFound)
				return
			}
			events := make([]aiInvocationEvent, 0, len(persisted))
			for _, item := range persisted {
				var event aiInvocationEvent
				if json.Unmarshal(item.Payload, &event) == nil {
					event.ID = strconv.FormatInt(item.Sequence, 10)
					events = append(events, event)
				}
			}
			stored.State = state
			record := s.invocations.restore(*stored, events)
			// Durable WorkflowAgent runs survive Go process restarts. A bound runtime
			// will continue posting signed events, so reconnecting must not convert an
			// active run into a failure merely because the in-memory hub was rebuilt.
			if !aiInvocationTerminal(record.State) && stored.RuntimeRunID == "" && stored.AgentRunID == "" {
				s.invocations.fail(record.ID, "Misty was interrupted before finishing. Please retry the request.")
			}
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unavailable", http.StatusInternalServerError)
			return
		}
		cursor := 0
		if value := strings.TrimSpace(r.Header.Get("Last-Event-ID")); value != "" {
			cursor, _ = strconv.Atoi(value)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		for {
			events, state, notify, found := s.invocations.events(userID, invocationID, cursor)
			if !found {
				http.Error(w, "invocation not found", http.StatusNotFound)
				return
			}
			for _, event := range events {
				payload, _ := json.Marshal(event)
				fmt.Fprintf(w, "id: %s\ndata: %s\n\n", event.ID, payload)
				cursor, _ = strconv.Atoi(event.ID)
			}
			flusher.Flush()
			if aiInvocationTerminal(state) {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-notify:
			case <-time.After(15 * time.Second):
				fmt.Fprint(w, ": keep-alive\n\n")
				flusher.Flush()
			}
		}
	}
}

func (s *AIService) CancelInvocation() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		stored, err := s.database.AIInvocationByID(r.Context(), userID, chi.URLParam(r, "invocationID"))
		if err != nil {
			http.Error(w, "invocation not found", http.StatusNotFound)
			return
		}
		if _, err := s.invocations.restoreDurable(r.Context(), *stored); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load invocation stream"})
			return
		}
		if !aiInvocationTerminal(stored.State) && stored.AgentRunID != "" {
			if run, cancelErr := s.database.CancelPersonalAgentTaskRunForOwner(r.Context(), userID, stored.AgentRunID); cancelErr == nil && run.RuntimeRunID != "" {
				_ = s.agentRuntime.Cancel(r.Context(), run.RuntimeRunID, run.ID)
			}
		} else if !aiInvocationTerminal(stored.State) && stored.RuntimeRunID != "" {
			_ = s.agentRuntime.Cancel(r.Context(), stored.RuntimeRunID, stored.ID)
		}
		state, found := s.invocations.cancelForUser(userID, stored.ID)
		if !found {
			http.Error(w, "invocation not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": state})
	}
}
