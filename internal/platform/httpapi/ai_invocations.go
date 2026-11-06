package api

import (
	"sync"
	"time"

	"github.com/misty-org/misty-server/internal/platform/db"
)

const (
	aiInvocationTTL       = 24 * time.Hour
	maxAIInvocationPrompt = 32 << 10
)

type aiInvocationInput struct {
	MethodVersionID string             `json:"method_version_id,omitempty"`
	MethodInputs    map[string]any     `json:"method_inputs,omitempty"`
	SkillVersionIDs []string           `json:"skill_version_ids,omitempty"`
	CompanionMode   string             `json:"companion_mode,omitempty"`
	DisplayCaptures []aiDisplayCapture `json:"display_captures,omitempty"`
	ThinkingMode    string             `json:"thinking_mode,omitempty"`
	TaskID          string             `json:"task_id,omitempty"`
	AgentID         string             `json:"agent_id,omitempty"`
	// SpaceID is the Space of the window the request came from. A new conversation
	// joins it; a run in an existing conversation takes the conversation's. Memory
	// tools read and write that Space's tree; empty is the asker's personal Space.
	SpaceID string `json:"space_id,omitempty"`
	// Profile is the profile of the window the request came from, whose sign-ins the
	// run uses; empty is the first profile.
	Profile string `json:"profile,omitempty"`
	// OriginDeviceID is the computer the request was sent from, where the run works.
	OriginDeviceID    string `json:"origin_device_id,omitempty"`
	ExecutionMode     string `json:"execution_mode,omitempty"`
	CollaborationMode string `json:"collaboration_mode,omitempty"` // "plan", "act" or "teach"; frozen at admission
	PermissionMode    string `json:"permission_mode,omitempty"`    // "ask" or "auto"; frozen at admission
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
	// Lesson and LessonIndex are a Teach-mode turn's lesson and the paused lessons
	// it may resume, kept on the device (ai_teach_mode.go).
	Lesson      *aiLessonState    `json:"lesson,omitempty"`
	LessonIndex []aiLessonSummary `json:"lesson_index,omitempty"`
	// MediaContext is what the video in front was saying, for a question at the
	// cursor (ai_media_context.go). It is page data, never instructions.
	MediaContext *aiMediaContext `json:"media_context,omitempty"`
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
	Citation   *aiCitation     `json:"citation,omitempty"`
	Artifact   *aiArtifact     `json:"artifact,omitempty"`
	AppRequest *appRequest     `json:"appRequest,omitempty"`
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
