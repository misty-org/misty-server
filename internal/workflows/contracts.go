package workflow

import (
	"encoding/json"
	"time"
)

const FormatVersion = 2

type Risk string

const (
	RiskRead        Risk = "read"
	RiskWrite       Risk = "write"
	RiskDestructive Risk = "destructive"
)

type ExecutionLocation string

const (
	LocationCloud  ExecutionLocation = "cloud"
	LocationDevice ExecutionLocation = "device"
	LocationEither ExecutionLocation = "either"
)

type JSONSchema map[string]any

// ContentRef is the only content identity accepted by the universal reader.
// Provider-specific credentials and raw local paths are deliberately absent.
type ContentRef struct {
	SourceKind      string `json:"sourceKind"`
	ProviderID      string `json:"providerId"`
	ResourceID      string `json:"resourceId"`
	Version         string `json:"version,omitempty"`
	Fingerprint     string `json:"fingerprint,omitempty"`
	DisplayName     string `json:"displayName"`
	MIMEType        string `json:"mimeType,omitempty"`
	Locator         string `json:"locator,omitempty"`
	PermissionScope string `json:"permissionScope"`
}

type Citation struct {
	Content ContentRef `json:"content"`
	Kind    string     `json:"kind"`
	Locator string     `json:"locator"`
	Excerpt string     `json:"excerpt,omitempty"`
}

type ContentSection struct {
	Kind         string `json:"kind"`
	Locator      string `json:"locator"`
	Text         string `json:"text,omitempty"`
	MediaDataURL string `json:"mediaDataUrl,omitempty"`
}

type ContentPage struct {
	Content       ContentRef       `json:"content"`
	Sections      []ContentSection `json:"sections"`
	Citations     []Citation       `json:"citations"`
	NextCursor    string           `json:"nextCursor,omitempty"`
	Truncated     bool             `json:"truncated"`
	SourceChanged bool             `json:"sourceChanged"`
}

type CapabilityRequirement struct {
	Capability string            `json:"capability"`
	Risk       Risk              `json:"risk"`
	Scopes     map[string]string `json:"scopes,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts     int `json:"maxAttempts"`
	CooldownSeconds int `json:"cooldownSeconds"`
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, CooldownSeconds: 60}
}

type ErrorPolicy struct {
	Mode           string `json:"mode"` // fail, continue, collect
	AcceptsPartial bool   `json:"acceptsPartial,omitempty"`
}

type Binding struct {
	SourceNode string `json:"sourceNode,omitempty"`
	SourcePort string `json:"sourcePort,omitempty"`
	InputPath  string `json:"inputPath,omitempty"`
	Literal    any    `json:"literal,omitempty"`
}

type Node struct {
	ID           string             `json:"id"`
	Kind         string             `json:"kind"`
	KindVersion  int                `json:"kindVersion"`
	Label        string             `json:"label"`
	Config       json.RawMessage    `json:"config"`
	Inputs       map[string]Binding `json:"inputs,omitempty"`
	OutputSchema JSONSchema         `json:"outputSchema"`
	Retry        RetryPolicy        `json:"retry"`
	Errors       ErrorPolicy        `json:"errors"`
}

// WorkflowNode is the public cross-executor name. Node is retained as the
// concise internal spelling used by the engine.
type WorkflowNode = Node

type Edge struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	SourcePort string `json:"sourcePort"`
	Target     string `json:"target"`
	TargetPort string `json:"targetPort"`
}

type WorkflowDependency struct {
	WorkflowID string `json:"workflowId"`
	VersionID  string `json:"versionId"`
	Checksum   string `json:"checksum"`
}

type Definition struct {
	FormatVersion int                     `json:"formatVersion"`
	Inputs        JSONSchema              `json:"inputs"`
	Outputs       JSONSchema              `json:"outputs"`
	Capabilities  []CapabilityRequirement `json:"capabilities"`
	Nodes         []Node                  `json:"nodes"`
	Edges         []Edge                  `json:"edges"`
	Dependencies  []WorkflowDependency    `json:"dependencies"`
}

type WorkflowVersion struct {
	ID           string                  `json:"id"`
	WorkflowID   string                  `json:"workflowId"`
	SpaceID      string                  `json:"spaceId"`
	CreatorID    string                  `json:"creatorId"`
	Version      string                  `json:"version"`
	Inputs       JSONSchema              `json:"inputs"`
	Outputs      JSONSchema              `json:"outputs"`
	Capabilities []CapabilityRequirement `json:"capabilityEnvelope"`
	Graph        Definition              `json:"graph"`
	Dependencies []WorkflowDependency    `json:"dependencyLock"`
	Checksum     string                  `json:"checksum"`
	PublishedAt  time.Time               `json:"publishedAt"`
}

type AgentWorkflowAttachment struct {
	Alias             string `json:"alias"`
	WorkflowID        string `json:"workflowId"`
	WorkflowVersionID string `json:"workflowVersionId"`
	Checksum          string `json:"checksum"`
	Enabled           bool   `json:"enabled"`
}

type AgentAccessPolicy struct {
	Mode           string   `json:"mode"` // space, selected
	AllowedUserIDs []string `json:"allowedUserIds,omitempty"`
}

type AgentVersion struct {
	ID           string                    `json:"id"`
	AgentID      string                    `json:"agentId"`
	SpaceID      string                    `json:"spaceId"`
	CreatorID    string                    `json:"creatorId"`
	Version      int                       `json:"version"`
	Instructions string                    `json:"instructions"`
	Access       AgentAccessPolicy         `json:"access"`
	Workflows    []AgentWorkflowAttachment `json:"workflows"`
	Capabilities []CapabilityRequirement   `json:"advertisedCapabilities,omitempty"`
	Checksum     string                    `json:"checksum,omitempty"`
	PublishedAt  time.Time                 `json:"publishedAt"`
}

type InstanceStatus string

const (
	InstanceIdle    InstanceStatus = "idle"
	InstanceRunning InstanceStatus = "running"
)

type AgentInstance struct {
	ID                 string            `json:"id"`
	AgentID            string            `json:"agentId"`
	UserID             string            `json:"userId"`
	AgentVersionID     string            `json:"agentVersionId"`
	Status             InstanceStatus    `json:"status"`
	EnabledWorkflows   map[string]bool   `json:"enabledWorkflows"`
	TriggerConfigs     json.RawMessage   `json:"triggerConfigs,omitempty"`
	ConnectionBindings map[string]string `json:"connectionBindings"`
	CapabilityGrants   json.RawMessage   `json:"capabilityGrants"`
	MemoryCheckpoint   json.RawMessage   `json:"memoryCheckpoint,omitempty"`
	UpdateAvailable    bool              `json:"updateAvailable"`
}

type RunState string

const (
	RunQueued              RunState = "queued"
	RunRunning             RunState = "running"
	RunCooldown            RunState = "cooldown"
	RunAwaitingApproval    RunState = "awaiting_approval"
	RunCompleted           RunState = "completed"
	RunCompletedWithErrors RunState = "completed_with_errors"
	RunFailed              RunState = "failed"
	RunCanceled            RunState = "canceled"
	RunRejected            RunState = "rejected"
)

func (state RunState) Terminal() bool {
	return state == RunCompleted || state == RunCompletedWithErrors || state == RunFailed || state == RunCanceled || state == RunRejected
}

type WorkflowRun struct {
	ID                string                     `json:"id"`
	SpaceID           string                     `json:"spaceId"`
	UserID            string                     `json:"userId"`
	AgentInstanceID   string                     `json:"agentInstanceId"`
	AgentVersionID    string                     `json:"agentVersionId"`
	WorkflowVersionID string                     `json:"workflowVersionId"`
	State             RunState                   `json:"state"`
	Trigger           json.RawMessage            `json:"triggerEnvelope"`
	Checkpoints       map[string]json.RawMessage `json:"checkpoints"`
	Attempts          map[string]int             `json:"attempts"`
	ActionJournal     []json.RawMessage          `json:"actionJournal"`
	Outputs           json.RawMessage            `json:"outputs"`
	Errors            map[string]string          `json:"errors"`
	Approvals         []json.RawMessage          `json:"approvals"`
	TraceEvents       []json.RawMessage          `json:"traceEvents"`
	CreatedAt         time.Time                  `json:"createdAt"`
	UpdatedAt         time.Time                  `json:"updatedAt"`
}
