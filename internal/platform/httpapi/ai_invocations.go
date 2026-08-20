package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	aiInvocationTimeout   = 5 * time.Minute
	maxAIInvocationPrompt = 32 << 10
)

type aiSelectionSnapshot struct {
	Kind        string         `json:"kind"`
	Content     string         `json:"content,omitempty"`
	Object      map[string]any `json:"object"`
	Anchors     map[string]any `json:"anchors,omitempty"`
	ContentHash string         `json:"contentHash"`
}

type aiInvocationInput struct {
	Mode                  string               `json:"mode"`
	SurfaceID             string               `json:"surface_id"`
	Trigger               string               `json:"trigger"`
	Prompt                string               `json:"prompt"`
	Context               []aiContextReference `json:"context"`
	Selection             *aiSelectionSnapshot `json:"selection,omitempty"`
	RequestedArtifactKind string               `json:"requested_artifact_kind,omitempty"`
	ConversationID        string               `json:"conversation_id,omitempty"`
	AgentID               string               `json:"agent_id,omitempty"`
	IdempotencyKey        string               `json:"idempotency_key"`
}

type aiInvocationEvent struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	State    string      `json:"state,omitempty"`
	Delta    string      `json:"delta,omitempty"`
	Citation *aiCitation `json:"citation,omitempty"`
	Artifact *aiArtifact `json:"artifact,omitempty"`
	Error    string      `json:"error,omitempty"`
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
	Cancel         context.CancelFunc
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
		available, err := s.database.AIActionAvailable(r.Context(), userID, body.SurfaceID, actionID, agent.InitialSelectedModelID)
		if err != nil {
			TestingWriteAIError(w, err)
			return
		}
		if !available {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "ai_surface_unavailable", "message": "Misty is temporarily unavailable for this action."})
			return
		}
		release, ok := s.acquireProviderCall(w, userID)
		if !ok {
			return
		}
		conversationID := strings.TrimSpace(body.ConversationID)
		if conversationID == "" && body.Mode == "drawer" && body.RequestedArtifactKind == "" {
			session := s.runtime.CreateSessionWithModel(userID, userID, agent.InitialSelectedModelID)
			conversationID = session.ID
			_ = s.database.RenameAgentSession(r.Context(), userID, conversationID, cleanMistyTitle(body.Prompt))
		}
		body.ConversationID = conversationID
		requestPayload, err := json.Marshal(body)
		if err != nil {
			release()
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
			release()
			TestingWriteAIError(w, err)
			return
		}
		record := s.invocations.restore(stored, nil)
		if !created {
			release()
			writeAIInvocationCreated(w, record)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), aiInvocationTimeout)
		s.invocations.setCancel(record.ID, cancel)
		go func() {
			defer release()
			defer cancel()
			s.runInvocation(ctx, userID, record.ID, body)
		}()
		writeAIInvocationCreated(w, record)
	}
}

func writeAIInvocationCreated(w http.ResponseWriter, record *aiInvocationRecord) {
	writeJSON(w, http.StatusAccepted, map[string]any{
		"invocationId":   record.ID,
		"conversationId": record.ConversationID,
		"state":          record.State,
		"eventsUrl":      "/ai/invocations/" + record.ID + "/events",
	})
}

func (s *AIService) runInvocation(ctx context.Context, userID, invocationID string, body aiInvocationInput) {
	startedAt := time.Now()
	firstOutput := time.Duration(0)
	outcome := "failed"
	actionID := firstAIText(body.RequestedArtifactKind, "ask")
	defer func() {
		if s.metrics != nil {
			s.metrics.RecordAIInvocation(body.SurfaceID, actionID, agent.InitialSelectedModelID, outcome, time.Since(startedAt), firstOutput)
		}
	}()
	s.invocations.append(invocationID, aiInvocationEvent{Type: "invocation.started", State: "running"})
	broker := aiContextBroker{database: s.database}
	resolved, err := broker.resolve(ctx, userID, body.Context)
	if err != nil {
		s.invocations.fail(invocationID, publicAIInvocationError(err))
		return
	}
	if body.SurfaceID == "home" || body.SurfaceID == "activity" || body.SurfaceID == "global" {
		var embedding []float64
		var semantic *hostedSemanticQueryOperation
		if s.analyzer != nil && len(body.Prompt) <= 512 {
			semantic, _ = beginHostedSemanticQuery(ctx, s.database, s.analyzer, userID, "ai-retrieval-query:"+invocationID, body.Prompt)
			if semantic != nil {
				embedding = semantic.Vector
			}
		}
		retrieved, retrieveErr := broker.retrieveAccount(ctx, userID, body.Prompt, embedding, 8)
		if retrieveErr != nil {
			semantic.Release(s.database)
			s.invocations.fail(invocationID, publicAIInvocationError(retrieveErr))
			return
		}
		if semantic != nil {
			if settleErr := semantic.Settle(s.database); settleErr != nil {
				semantic.Release(s.database)
			}
		}
		resolved = mergeAIResolvedContext(resolved, retrieved, 12)
	}
	for _, item := range resolved {
		citation := item.Citation
		s.invocations.append(invocationID, aiInvocationEvent{Type: "citation", Citation: &citation})
	}
	if citation := aiSelectionCitation(body); citation != nil {
		s.invocations.append(invocationID, aiInvocationEvent{Type: "citation", Citation: citation})
	}
	prompt := aiContextPrompt(body.Prompt, resolved, body.Selection, body.Context)
	artifactKind := body.RequestedArtifactKind
	if artifactKind == "" && body.Selection != nil && strings.HasPrefix(body.SurfaceID, "notes") {
		artifactKind = "text_patch"
	}
	if artifactKind == "text_patch" {
		prompt = "Rewrite the selected content to satisfy the user request. Return only the replacement text, with no preface, quotation marks, or Markdown fence.\n\n" + prompt
	} else if artifactKind == "task_set" {
		prompt = "Extract concrete, non-duplicative tasks from the authorized content. Return strict JSON only in this shape: {\"tasks\":[{\"title\":\"...\",\"notes\":\"...\",\"priority\":\"high|medium|low\"}]}. Use at most 20 tasks. Do not invent owners, dates, or commitments. Return {\"tasks\":[]} when there are no concrete tasks.\n\n" + prompt
	} else if spec, ok := aiArtifactSpecs[artifactKind]; ok {
		prompt = spec.Prompt + " Return strict JSON only in this shape: {\"summary\":\"short review summary\",\"operations\":" + spec.Shape + "}. Do not claim the proposal was applied or executed.\n\n" + prompt
	}
	var answer string
	if body.ConversationID != "" && artifactKind == "" {
		tier, tierErr := s.agentTierForUser(userID)
		if tierErr != nil {
			s.invocations.fail(invocationID, "Misty could not determine the model policy.")
			return
		}
		if err = s.runtime.ConfigureSession(body.ConversationID, userID, aiInvocationSystemPrompt(body.SurfaceID), false, false); err == nil {
			err = s.runtime.SendMessageWithTierContext(ctx, body.ConversationID, userID, agent.AgentMessageRequest{Mode: agent.ModeAsk, UserMessage: prompt}, tier)
		}
		if err == nil {
			transcript, transcriptErr := s.runtime.Transcript(ctx, body.ConversationID, userID)
			err = transcriptErr
			if transcriptErr == nil && len(transcript) > 0 {
				answer = transcript[len(transcript)-1].Content
			}
		}
	} else {
		answer, _, err = s.runtime.CompleteWithModelContext(ctx, userID, prompt, "assistant_ai", agent.InitialSelectedModelID)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			outcome = "canceled"
			s.invocations.cancel(invocationID)
			return
		}
		s.invocations.fail(invocationID, publicAIInvocationError(err))
		return
	}
	answer = strings.TrimSpace(answer)
	firstOutput = time.Since(startedAt)
	if artifactKind == "text_patch" && body.Selection != nil {
		artifact := s.invocations.addTextPatchArtifact(userID, invocationID, answer, resolved, body)
		s.invocations.append(invocationID, aiInvocationEvent{Type: "response.delta", Delta: "I prepared a revision for you to review."})
		s.invocations.append(invocationID, aiInvocationEvent{Type: "artifact.proposed", Artifact: artifact})
	} else if artifactKind == "task_set" {
		tasks, parseErr := parseAITaskDrafts(answer)
		if parseErr != nil || len(tasks) == 0 {
			s.invocations.append(invocationID, aiInvocationEvent{Type: "response.delta", Delta: "I did not find concrete tasks that were safe to propose."})
		} else if artifact := s.invocations.addTaskSetArtifact(userID, invocationID, tasks, resolved, body); artifact != nil {
			suffix := "s"
			if len(tasks) == 1 {
				suffix = ""
			}
			s.invocations.append(invocationID, aiInvocationEvent{Type: "response.delta", Delta: fmt.Sprintf("I prepared %d task%s for review.", len(tasks), suffix)})
			s.invocations.append(invocationID, aiInvocationEvent{Type: "artifact.proposed", Artifact: artifact})
		} else {
			s.invocations.fail(invocationID, "Misty could not determine which Space should receive these tasks.")
			return
		}
	} else if spec, ok := aiArtifactSpecs[artifactKind]; ok {
		summary, operations, parseErr := parseAIStructuredArtifact(answer)
		if parseErr != nil {
			s.invocations.fail(invocationID, "Misty could not create a valid reviewable proposal.")
			return
		}
		artifact := s.invocations.addStructuredArtifact(userID, invocationID, artifactKind, summary, operations, resolved, body, spec)
		s.invocations.append(invocationID, aiInvocationEvent{Type: "response.delta", Delta: "I prepared a reviewable proposal. Nothing has been applied."})
		s.invocations.append(invocationID, aiInvocationEvent{Type: "artifact.proposed", Artifact: artifact})
	} else {
		for _, delta := range aiTextDeltas(answer, 320) {
			s.invocations.append(invocationID, aiInvocationEvent{Type: "response.delta", Delta: delta})
		}
	}
	s.invocations.complete(invocationID)
	outcome = "completed"
}

func aiSelectionCitation(body aiInvocationInput) *aiCitation {
	if body.Selection == nil || body.Selection.Object["kind"] != "browser-page" {
		return nil
	}
	scopeID, _ := body.Selection.Object["id"].(string)
	if strings.TrimSpace(scopeID) == "" {
		return nil
	}
	for _, reference := range body.Context {
		if reference.Kind != "browser-tab" || reference.OpaqueScopeID != scopeID || !reference.Attached {
			continue
		}
		return &aiCitation{
			ID: scopeID, Kind: "browser-page", Title: firstAIText(reference.Title, "Browser page"),
			Href: "misty://browser/" + url.PathEscape(scopeID), Revision: reference.Revision,
			Excerpt: aiExcerpt(body.Selection.Content),
		}
	}
	return nil
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
			if !aiInvocationTerminal(record.State) {
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
		state, found := s.invocations.cancelForUser(userID, chi.URLParam(r, "invocationID"))
		if !found {
			stored, err := s.database.AIInvocationByID(r.Context(), userID, chi.URLParam(r, "invocationID"))
			if err != nil {
				http.Error(w, "invocation not found", http.StatusNotFound)
				return
			}
			s.invocations.restore(*stored, nil)
			state, found = s.invocations.cancelForUser(userID, stored.ID)
			if !found {
				http.Error(w, "invocation not found", http.StatusNotFound)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": state})
	}
}
