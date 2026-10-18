package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/kannachi323/misty/server/internal/platform/postgres"
)

const (
	aiInvocationTTL       = 24 * time.Hour
	maxAIInvocationPrompt = 32 << 10
)

type aiInvocationInput struct {
	MethodVersionID string         `json:"method_version_id,omitempty"`
	MethodInputs    map[string]any `json:"method_inputs,omitempty"`
	SkillVersionIDs []string       `json:"skill_version_ids,omitempty"`
	CompanionMode   string         `json:"companion_mode,omitempty"`
	CompanionModel  string         `json:"companion_model,omitempty"`
	// ModelOverride pins a new conversation's Thinking model with its first
	// message; an existing conversation sets it with PATCH instead.
	ModelOverride     string             `json:"model_override,omitempty"`
	DisplayCaptures   []aiDisplayCapture `json:"display_captures,omitempty"`
	ThinkingMode      string             `json:"thinking_mode,omitempty"`
	TaskID            string             `json:"task_id,omitempty"`
	AgentID           string             `json:"agent_id,omitempty"`
	// SpaceID is the Space of the window the request came from. Memory tools
	// read and write that Space's tree; empty is the asker's personal Space.
	SpaceID string `json:"space_id,omitempty"`
	ExecutionMode     string             `json:"execution_mode,omitempty"`
	CollaborationMode string             `json:"collaboration_mode,omitempty"` // "plan" or "act"; frozen at admission
	PermissionMode    string             `json:"permission_mode,omitempty"`    // "ask" or "auto"; frozen at admission
	// CodingProject is the attached project a delegated coding run works in.
	CodingProject string `json:"coding_project,omitempty"`
	// CodingTool is the coding tool the person picked for this chat; empty uses the Settings default.
	CodingTool            string                      `json:"coding_tool,omitempty"`
	WindowLabel           string                      `json:"window_label,omitempty"`
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
	IdempotencyKey        string                      `json:"idempotency_key"`
	Timezone              string                      `json:"timezone,omitempty"`
	// NotebookKey and DelegatedFrom are set by the server for sub-agents
	// (agent_delegate); requests from clients never carry them.
	NotebookKey   string `json:"notebook_key,omitempty"`
	DelegatedFrom string `json:"delegated_from,omitempty"`
	// CurrentTab is the Misty browser tab in front of the user when they wrote,
	// so "this page" has a referent. Its title and address are untrusted page data.
	CurrentTab *aiCurrentTab `json:"current_tab,omitempty"`
	// ScheduleID and ScheduleClaim are sent by the computer starting a
	// scheduled run it claimed (roadmap C1). The claim is never stored.
	ScheduleID    string `json:"schedule_id,omitempty"`
	ScheduleClaim string `json:"schedule_claim,omitempty"`
}

type aiCurrentTab struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type aiInvocationEvent struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	State      string `json:"state,omitempty"`
	Delta      string `json:"delta,omitempty"`
	Text       string `json:"text,omitempty"`
	Phase      string `json:"phase,omitempty"`
	Summary    string `json:"summary,omitempty"`
	ArtifactID string `json:"artifactId,omitempty"`
	RunID      string `json:"runId,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	// Activity and DurationMS describe a tool call for the reply's timeline.
	Activity   *aiToolActivity `json:"activity,omitempty"`
	DurationMS int64           `json:"durationMs,omitempty"`
	// Result is the raw tool result, sent only to local workflow evals.
	Result     string      `json:"result,omitempty"`
	Citation   *aiCitation `json:"citation,omitempty"`
	Artifact   *aiArtifact `json:"artifact,omitempty"`
	AppRequest *appRequest `json:"appRequest,omitempty"`
	// ScreenRequest asks the desktop to open a screen and continue the task.
	ScreenRequest *screenRequest `json:"screenRequest,omitempty"`
	// Notebook is the run's plan and findings after a notebook write.
	Notebook *aiNotebookEvent `json:"notebook,omitempty"`
	// Delegation lists a run's sub-agents and their progress.
	Delegation *aiDelegationEvent `json:"delegation,omitempty"`
	aiCollaborationEventFields
	Error string `json:"error,omitempty"`
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
	nextPrune   time.Time
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
		if !decodeAIInvocationBody(w, r, &body) {
			return
		}
		// Only the server links a run to a parent's notebook or delegation.
		body.NotebookKey, body.DelegatedFrom, body.CodingProject = "", "", ""
		scheduled, ok := s.admitScheduledInvocation(w, r, userID, &body)
		if !ok {
			return
		}
		admittedRun := false
		defer func() {
			if scheduled != nil && !admittedRun {
				scheduled.release(context.WithoutCancel(r.Context()), s.database, errors.New("the scheduled run couldn't start on its computer"))
			}
		}()
		if !s.validateInvocationRequest(w, r, userID, &body) {
			return
		}
		if !ensureIdempotencyKey(w, r, &body) {
			return
		}
		actionID := body.RequestedArtifactKind
		if actionID == "" {
			actionID = "ask"
		}
		if !s.validateExecutionTarget(w, &body) {
			return
		}
		if !s.resolveInvocationAgent(w, r, userID, &body) {
			return
		}
		// AI execution belongs to the authenticated account.
		st := aiInvocationAdmitState{conversationID: strings.TrimSpace(body.ConversationID), spaceID: ""}
		if !s.chooseInvocationModel(w, r, userID, &body, &st) {
			return
		}
		if !s.bindConversationModel(w, r, userID, &body, &st) {
			return
		}
		if !s.applyRequestedModelOverride(w, r, &body, &st) {
			return
		}
		if !s.admitInvocationExecution(w, r, userID, &body, &st) {
			return
		}
		if !s.createInvocationConversation(w, r, userID, &body, &st) {
			return
		}
		if !s.saveConversationModelOverride(w, r, userID, &body, &st) {
			return
		}
		if !checkInvocationModelAvailable(w, r, &body, &st) {
			return
		}
		if !s.checkInvocationSurface(w, r, userID, &body, actionID, &st) {
			return
		}
		if !s.admitInvocationCollaboration(w, r, userID, &body, &st) {
			return
		}
		stored, created, now, ok := s.storeInvocationRecord(w, r, userID, &body, &st, scheduled)
		if !ok {
			return
		}
		if created && s.providerSettings != nil && !s.freezeInvocationRoutes(w, r, userID, stored.ID, &st) {
			return
		}
		if scheduled != nil {
			if err := s.database.BindWorkflowScheduleRun(r.Context(), *scheduled.schedule, st.conversationID, stored.ID); err != nil {
				TestingWriteAIError(w, err)
				return
			}
			admittedRun = true
		}
		record, err := s.invocations.restoreDurable(r.Context(), stored)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load invocation stream"})
			return
		}
		if scheduled != nil && created {
			if notice := scheduled.lateNotice(now); notice != "" {
				s.invocations.append(stored.ID, aiInvocationEvent{Type: "assistant.status", Text: notice, Phase: "schedule_late"})
			}
		}
		if !created {
			writeAIInvocationCreated(w, record)
			return
		}
		titleNewConversation(s.database, s.runtime, userID, st.conversationID, body)
		s.recordInvocationOnboarding(r.Context(), userID, body)
		if st.modelFallbackNotice {
			s.invocations.append(stored.ID, aiInvocationEvent{Type: "assistant.status", Text: "The previous model retired, so Misty switched this conversation to the frontier default.", Phase: "model_fallback"})
		}
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
		if err := s.database.CancelMistyInvocationChildren(r.Context(), userID, stored.ID); err != nil {
			writeSpaceError(w, err)
			return
		}
		if s.providerSettings != nil {
			// Sub-agents stop with the run that started them.
			s.providerSettings.cancelDelegatedChildren(r.Context(), userID, stored.ID)
		}
		if !aiInvocationTerminal(stored.State) && stored.AgentRunID != "" {
			if run, cancelErr := s.database.CancelPersonalAgentTaskRunForOwner(r.Context(), userID, stored.AgentRunID); cancelErr == nil && run.RuntimeRunID != "" {
				_ = s.agentRuntime.Cancel(r.Context(), run.RuntimeRunID, run.ID)
			}
		} else if !aiInvocationTerminal(stored.State) && stored.RuntimeRunID != "" {
			_ = s.agentRuntime.Cancel(r.Context(), stored.RuntimeRunID, stored.ID)
		}
		current, err := s.database.AIInvocationByID(r.Context(), userID, stored.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Misty could not load the stop result."})
			return
		}
		_, _ = s.invocations.restoreDurable(r.Context(), *current)
		state := current.State
		_ = s.database.SetAgentQuestionState(r.Context(), userID, "", stored.ID, "canceled")
		writeJSON(w, http.StatusAccepted, map[string]any{"state": state, "runtime_cancel_pending": !aiInvocationTerminal(stored.State) && stored.RuntimeRunID != "", "message": "Stop requested. Previously completed or uncertain actions remain in history."})
	}
}
