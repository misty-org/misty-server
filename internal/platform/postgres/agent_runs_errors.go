package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrWorkflowIntegrationRequired = errors.New("workflow integration required")

type AgentRunRequest struct {
	RequestingMemberID   string                    `json:"requesting_member_id"`
	SpaceID              string                    `json:"space_id"`
	AgentID              string                    `json:"agent_id"`
	SourceConversationID string                    `json:"source_conversation_id,omitempty"`
	ConversationScope    SpaceConversationScopeRef `json:"conversation_scope"`
	SourceMessageID      string                    `json:"source_message_id,omitempty"`
	SourceType           string                    `json:"source_type"`
	CapabilityID         string                    `json:"capability_id,omitempty"`
	Input                json.RawMessage           `json:"input"`
	TriggerKind          string                    `json:"trigger_kind"`
	SourceTaskID         string                    `json:"source_task_id,omitempty"`
	ActionEnvelope       json.RawMessage           `json:"action_envelope,omitempty"`
}

type RunAction struct {
	ID          string          `json:"id"`
	RunID       string          `json:"run_id"`
	ActionKind  string          `json:"action_kind"`
	Summary     string          `json:"summary"`
	Details     json.RawMessage `json:"details"`
	Destructive bool            `json:"destructive"`
	State       string          `json:"state"`
	PerformedAt *time.Time      `json:"performed_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type RunApproval struct {
	ID                  string          `json:"id"`
	RunID               string          `json:"run_id"`
	RequestedFromUserID string          `json:"requested_from_user_id"`
	DecidedByUserID     string          `json:"decided_by_user_id,omitempty"`
	ActionSummary       string          `json:"action_summary"`
	ProposedActions     json.RawMessage `json:"proposed_actions"`
	State               string          `json:"state"`
	CreatedAt           time.Time       `json:"created_at"`
	DecidedAt           *time.Time      `json:"decided_at,omitempty"`
	ExpiresAt           time.Time       `json:"expires_at"`
}

type AgentConversation struct {
	ID          string    `json:"id"`
	SpaceID     string    `json:"space_id"`
	OwnerUserID string    `json:"owner_user_id"`
	AgentID     string    `json:"agent_id"`
	AgentName   string    `json:"agent_name"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AgentConversationEvent struct {
	ID             int64           `json:"id"`
	ConversationID string          `json:"conversation_id"`
	UserID         string          `json:"user_id"`
	EventType      string          `json:"event_type"`
	Data           json.RawMessage `json:"data"`
	CreatedAt      time.Time       `json:"created_at"`
}

const spaceRunColumns = `id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,trigger_kind,state,input,result,COALESCE(error_code,''),created_at,completed_at,requesting_member_id,COALESCE(source_conversation_id,''),source_type,COALESCE(agent_id,''),COALESCE(workflow_identifier,''),COALESCE(workflow_version_id,''),COALESCE(workflow_version,''),COALESCE(capability_id,''),progress,outputs,artifacts,COALESCE(error_message,''),COALESCE(retry_of_run_id,''),canceled_at,updated_at,COALESCE(agent_instance_id,''),COALESCE(agent_version_id,''),attempt,next_retry_at,COALESCE(source_task_id,''),action_envelope,conversation_scope_kind,COALESCE(scope_conversation_id,''),COALESCE(source_message_id,''),runtime_kind,runtime_run_id,runtime_phase,runtime_heartbeat_at,owner_user_id,initial_run_mode,effective_run_mode,agent_version_snapshot,approval_state,COALESCE(parent_run_id,''),delegation_depth,context_bindings,device_wait_hook_token,device_wait_expires_at`

func insertRunApprovalTx(ctx context.Context, tx *sql.Tx, runID, userID string, capability *WorkflowCapability, workflowVersionID string) error {
	proposed := mustJSON([]map[string]any{{"capability_id": capability.ID, "description": capability.Description, "destructive": capability.Destructive}})
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_run_approvals(id,run_id,requested_from_user_id,action_summary,proposed_actions) VALUES($1,$2,$3,$4,$5)`, "runapproval_"+uuid.NewString(), runID, userID, "Approve "+capability.Name, proposed); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO space_run_actions(id,run_id,action_kind,summary,details,destructive,state) VALUES($1,$2,$3,$4,$5,$6,'proposed')`, "runaction_"+uuid.NewString(), runID, capability.ID, capability.Description, mustJSON(map[string]any{"capability_id": capability.ID, "workflow_version_id": workflowVersionID}), capability.Destructive)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO space_inbox_items(user_id,space_id,kind,payload) SELECT $1,space_id,'approval',$2 FROM space_runs WHERE id=$3`, userID, mustJSON(map[string]any{"run_id": runID, "capability_id": capability.ID, "workflow_version_id": workflowVersionID}), runID)
	return err
}
