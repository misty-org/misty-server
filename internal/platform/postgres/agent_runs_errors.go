package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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

func (db *Database) CreateAgentRun(ctx context.Context, request AgentRunRequest) (*SpaceRun, error) {
	if !validRunSource(request.SourceType) || strings.TrimSpace(request.RequestingMemberID) == "" || strings.TrimSpace(request.AgentID) == "" {
		return nil, ErrSpaceInvalid
	}
	if len(request.Input) == 0 {
		request.Input = json.RawMessage(`{}`)
	}
	if !validJSONObject(request.Input) {
		return nil, ErrSpaceInvalid
	}
	instance, err := db.EnsureAgentInstance(ctx, request.RequestingMemberID, request.SpaceID, request.AgentID)
	if err != nil {
		return nil, err
	}
	out := &SpaceRun{
		ID: "run_" + uuid.NewString(), SpaceID: request.SpaceID, ResourceKind: "agent", ResourceID: request.AgentID,
		InitiatedByUserID: request.RequestingMemberID, BillingUserID: request.RequestingMemberID, TriggerKind: request.TriggerKind,
		RequestingMemberID: request.RequestingMemberID, SourceConversationID: request.SourceConversationID, SourceType: request.SourceType,
		AgentID: request.AgentID, AgentInstanceID: instance.ID, AgentVersionID: instance.AgentVersionID, Attempt: 1,
		Input: request.Input, Result: json.RawMessage(`{}`), Outputs: json.RawMessage(`{}`), Artifacts: json.RawMessage(`[]`),
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, request.RequestingMemberID, request.SpaceID, PermissionAgentsRun); err != nil {
			return err
		}
		var enabled bool
		if err := tx.QueryRowContext(ctx, `SELECT enabled FROM space_agents WHERE id=$1 AND space_id=$2 AND status='available'`, request.AgentID, request.SpaceID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return ErrSpaceInvalid
		}
		scope := request.ConversationScope
		if scope.Kind == "" {
			scope = NormalizeConversationScope(request.SourceConversationID)
		}
		if scope.Kind == ConversationScopePrivate {
			if err := validateResourceAudienceTx(ctx, tx, request.RequestingMemberID, request.SpaceID, SpaceResourceAudience{Kind: SpaceAudienceConversation, ConversationID: scope.ConversationID}); err != nil {
				return err
			}
		}
		out.ConversationScopeKind, out.ScopeConversationID, out.SourceMessageID = scope.Kind, scope.ConversationID, request.SourceMessageID
		out.State = "running"
		out.CapabilityID = strings.TrimSpace(request.CapabilityID)
		var workflow *WorkflowVersion
		var capability *WorkflowCapability
		if out.CapabilityID == "" || out.CapabilityID == "chat" {
			out.CapabilityID = "chat"
		} else {
			var err error
			workflow, capability, err = resolveAgentWorkflowCapabilityTx(ctx, tx, instance.AgentVersionID, out.CapabilityID)
			if err != nil {
				return err
			}
			if err := TestingValidateCapabilityInput(*capability, request.Input); err != nil {
				return err
			}
			if err := authorizeAgentWorkflowRequirementsTx(ctx, tx, request.RequestingMemberID, request.SpaceID, instance.ID, workflow.Metadata); err != nil {
				return err
			}
			if request.SourceType == "schedule" {
				var automationEnabled bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agent_instance_workflows WHERE instance_id=$1 AND workflow_version_id=$2 AND enabled AND consent->>'granted'='true')`, instance.ID, workflow.ID).Scan(&automationEnabled); err != nil || !automationEnabled {
					return ErrSpaceForbidden
				}
			}
			out.WorkflowIdentifier, out.WorkflowVersionID, out.WorkflowVersion = workflow.StableIdentifier, workflow.ID, workflow.Version
			// Destructive graph nodes request approval only after their exact,
			// fingerprinted action input is known. Non-destructive capabilities
			// may still require an up-front workflow confirmation.
			if capability.ConfirmationRequired && !capability.Destructive {
				out.State = "awaiting_approval"
			}
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_runs(id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,trigger_kind,state,input,requesting_member_id,source_conversation_id,source_type,agent_id,workflow_identifier,workflow_version_id,workflow_version,capability_id,outputs,artifacts,agent_instance_id,agent_version_id,attempt,conversation_scope_kind,scope_conversation_id,source_message_id)
			VALUES($1,$2,'agent',$3,$4,$4,$5,$6,$7,$4,NULLIF($8,''),$9,$3,NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),$13,'{}'::jsonb,'[]'::jsonb,$14,$15,1,$16,NULLIF($17,''),NULLIF($18,'')) RETURNING created_at,updated_at`,
			out.ID, request.SpaceID, request.AgentID, request.RequestingMemberID, request.TriggerKind, out.State, request.Input, request.SourceConversationID, request.SourceType, out.WorkflowIdentifier, out.WorkflowVersionID, out.WorkflowVersion, out.CapabilityID, instance.ID, instance.AgentVersionID, scope.Kind, scope.ConversationID, request.SourceMessageID).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if out.State == "awaiting_approval" {
			if err := insertRunApprovalTx(ctx, tx, out.ID, request.RequestingMemberID, capability, workflow.ID); err != nil {
				return err
			}
		}
		_, err = recordSpaceEventTx(ctx, tx, request.SpaceID, request.RequestingMemberID, "agent.run.started", out.ID, map[string]any{"agent_id": request.AgentID, "workflow_version": out.WorkflowVersion, "capability_id": out.CapabilityID, "source_type": out.SourceType})
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func authorizeAgentWorkflowRequirementsTx(ctx context.Context, tx *sql.Tx, userID, spaceID, instanceID string, metadata WorkflowMetadata) error {
	for _, permission := range metadata.RequiredPermissions {
		spacePermission, ok := TestingWorkflowPermissionSpacePermission(permission)
		if ok {
			if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, spacePermission); err != nil {
				return err
			}
		}
	}
	for _, provider := range metadata.RequiredIntegrations {
		var available bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agent_instances i JOIN space_integrations c ON c.id=i.connection_bindings->>$3 WHERE i.id=$1 AND i.user_id=$2 AND c.connected_by_user_id=$2 AND c.status='active')`, instanceID, userID, provider).Scan(&available); err != nil {
			return err
		}
		if !available {
			return ErrWorkflowIntegrationRequired
		}
	}
	return nil
}

func resolveAgentWorkflowCapabilityTx(ctx context.Context, tx *sql.Tx, agentVersionID, capabilityID string) (*WorkflowVersion, *WorkflowCapability, error) {
	rows, err := tx.QueryContext(ctx, `SELECT workflow_version_id FROM space_agent_version_workflows WHERE agent_version_id=$1 AND enabled ORDER BY position,alias`, agentVersionID)
	if err != nil {
		return nil, nil, err
	}
	versionIDs := []string{}
	for rows.Next() {
		var versionID string
		if err := rows.Scan(&versionID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		versionIDs = append(versionIDs, versionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	for _, versionID := range versionIDs {
		workflow, err := loadWorkflowVersionTx(ctx, tx, versionID)
		if err != nil {
			return nil, nil, err
		}
		for index := range workflow.Metadata.Capabilities {
			if workflow.Metadata.Capabilities[index].ID == capabilityID {
				return workflow, &workflow.Metadata.Capabilities[index], nil
			}
		}
	}
	return nil, nil, ErrSpaceInvalid
}

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
