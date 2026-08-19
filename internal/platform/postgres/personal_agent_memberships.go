package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type SpaceAgentMembership struct {
	ID                string          `json:"id"`
	SpaceID           string          `json:"space_id"`
	AgentID           string          `json:"agent_id"`
	OwnerUserID       string          `json:"owner_user_id"`
	CanControl        bool            `json:"can_control"`
	Name              string          `json:"name"`
	Role              string          `json:"-"`
	Description       string          `json:"description"`
	Icon              string          `json:"icon"`
	Avatar            json.RawMessage `json:"avatar"`
	Instructions      string          `json:"instructions,omitempty"`
	ModelID           string          `json:"model_id,omitempty"`
	ReasoningEffort   string          `json:"reasoning_effort,omitempty"`
	DefaultRunMode    string          `json:"default_run_mode"`
	Enabled           bool            `json:"enabled"`
	RoleID            string          `json:"-"`
	CapabilityGrants  json.RawMessage `json:"-"`
	RolePermissions   json.RawMessage `json:"-"`
	ApprovedVersionID string          `json:"-"`
	ApprovedVersion   int64           `json:"version"`
	LatestVersionID   string          `json:"-"`
	LatestVersion     int64           `json:"-"`
	UpdateAvailable   bool            `json:"-"`
	SpaceRole         string          `json:"-"`
	SpaceInstructions string          `json:"-"`
	Permissions       json.RawMessage `json:"-"`
	ManagedByUserID   string          `json:"-"`
	MembershipVersion int64           `json:"-"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	WorkState         string          `json:"work_state"`
	AttentionCount    int64           `json:"attention_count"`
	LastActivityAt    *time.Time      `json:"last_activity_at,omitempty"`
	CurrentTaskID     string          `json:"current_task_id,omitempty"`
}

const spaceAgentMembershipColumns = `'companion:'||$1||':'||a.id,$1,a.id,a.owner_user_id,v.name,v.role,v.description,v.icon,v.avatar,v.instructions,v.model_id,v.reasoning_effort,v.default_run_mode,a.enabled,
	'', '[]'::jsonb, '[]'::jsonb,
	v.id,v.version,v.id,v.version,FALSE,v.role,'',
	'{}'::jsonb,a.owner_user_id,a.version,a.created_at,a.updated_at,
	CASE WHEN NOT a.enabled THEN 'disabled'
		WHEN COALESCE(run_summary.awaiting_count,0)>0 THEN 'awaiting_approval'
		WHEN COALESCE(run_summary.device_count,0)>0 THEN 'awaiting_device'
		WHEN COALESCE(run_summary.working_count,0)>0 THEN 'working'
		WHEN run_summary.latest_state IN ('failed','completed_with_errors') THEN 'failed'
		ELSE 'ready' END,
	COALESCE(run_summary.awaiting_count,0)+CASE WHEN run_summary.latest_state IN ('failed','completed_with_errors') THEN 1 ELSE 0 END,run_summary.last_activity_at,
	COALESCE(run_summary.current_task_id,'')`

func scanSpaceAgentMembership(row scanner, out *SpaceAgentMembership) error {
	return row.Scan(&out.ID, &out.SpaceID, &out.AgentID, &out.OwnerUserID, &out.Name, &out.Role, &out.Description, &out.Icon, &out.Avatar, &out.Instructions, &out.ModelID, &out.ReasoningEffort,
		&out.DefaultRunMode, &out.Enabled, &out.RoleID, &out.CapabilityGrants, &out.RolePermissions, &out.ApprovedVersionID, &out.ApprovedVersion, &out.LatestVersionID, &out.LatestVersion,
		&out.UpdateAvailable, &out.SpaceRole, &out.SpaceInstructions, &out.Permissions, &out.ManagedByUserID, &out.MembershipVersion,
		&out.CreatedAt, &out.UpdatedAt, &out.WorkState, &out.AttentionCount, &out.LastActivityAt, &out.CurrentTaskID)
}

const spaceAgentMembershipJoins = ` FROM personal_agents a
	JOIN personal_agent_versions v ON v.agent_id=a.id AND v.version=a.version
	LEFT JOIN LATERAL (
		SELECT COUNT(*) FILTER (WHERE r.state='awaiting_approval') AS awaiting_count,
			COUNT(*) FILTER (WHERE r.state='awaiting_device') AS device_count,
			COUNT(*) FILTER (WHERE r.state IN ('queued','running','cooldown')) AS working_count,
			(ARRAY_AGG(r.state ORDER BY r.updated_at DESC))[1] AS latest_state,
			MAX(r.updated_at) AS last_activity_at,
			(ARRAY_AGG(r.source_task_id ORDER BY r.updated_at DESC) FILTER (
				WHERE r.source_task_id IS NOT NULL AND r.state IN ('queued','running','cooldown','awaiting_approval','failed','completed_with_errors')
			))[1] AS current_task_id
		FROM space_runs r
		WHERE r.space_id=$1 AND r.agent_id=a.id
	) run_summary ON TRUE `

func (db *Database) SpaceAgentMemberships(ctx context.Context, userID, spaceID string) ([]SpaceAgentMembership, error) {
	items := []SpaceAgentMembership{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceAgentMembershipColumns+spaceAgentMembershipJoins+`
			WHERE a.deleted_at IS NULL AND ((a.owner_user_id=$2 AND a.enabled) OR EXISTS(
				SELECT 1 FROM space_tasks t WHERE t.space_id=$1 AND t.assignee_agent_id=a.id
			) OR EXISTS(SELECT 1 FROM space_messages m WHERE m.space_id=$1 AND m.sender_agent_id=a.id))
			ORDER BY lower(v.name),a.id`, spaceID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceAgentMembership
			if err := scanSpaceAgentMembership(rows, &item); err != nil {
				return err
			}
			if item.OwnerUserID != userID {
				item.Instructions, item.SpaceInstructions, item.ModelID, item.ReasoningEffort = "", "", "", ""
			}
			item.CanControl = item.OwnerUserID == userID && item.Enabled
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SpaceAgentMembership(ctx context.Context, userID, spaceID, agentID string) (*SpaceAgentMembership, error) {
	out := &SpaceAgentMembership{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return scanSpaceAgentMembership(tx.QueryRowContext(ctx, `SELECT `+spaceAgentMembershipColumns+spaceAgentMembershipJoins+`
			WHERE a.id=$2 AND a.deleted_at IS NULL AND ((a.owner_user_id=$3 AND a.enabled) OR EXISTS(
				SELECT 1 FROM space_tasks t WHERE t.space_id=$1 AND t.assignee_agent_id=a.id
			) OR EXISTS(SELECT 1 FROM space_messages m WHERE m.space_id=$1 AND m.sender_agent_id=a.id))`, spaceID, agentID, userID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	out.CanControl = out.OwnerUserID == userID && out.Enabled
	return out, err
}

func activePersonalAgentMembershipTx(ctx context.Context, tx *sql.Tx, userID, spaceID, agentID string) (*SpaceAgentMembership, error) {
	if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
		return nil, err
	}
	out := &SpaceAgentMembership{}
	err := scanSpaceAgentMembership(tx.QueryRowContext(ctx, `SELECT `+spaceAgentMembershipColumns+spaceAgentMembershipJoins+`
		WHERE a.id=$2 AND a.owner_user_id=$3 AND a.deleted_at IS NULL AND a.enabled
		AND EXISTS(SELECT 1 FROM space_members owner_membership WHERE owner_membership.space_id=$1 AND owner_membership.user_id=a.owner_user_id)`, spaceID, agentID, userID), out)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	return out, err
}

func personalAgentAllowedTx(ctx context.Context, tx *sql.Tx, userID, spaceID, agentID string) (*SpaceAgentMembership, error) {
	return activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID)
}

func (db *Database) EffectiveAgentSpacePermission(ctx context.Context, userID, spaceID, agentID, permission string) (bool, error) {
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		memberAllowed, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, permission)
		if err != nil || !memberAllowed {
			return err
		}
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		allowed = memberAllowed
		return nil
	})
	return allowed, err
}

func (db *Database) EffectivePersonalAgentToolPermissions(ctx context.Context, userID, spaceID, agentID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		raw = json.RawMessage(`{"mode":"inherit_creator","read":true,"write":true,"integrations":["discord","google","notion","slack"]}`)
		return nil
	})
	return raw, err
}

// PersonalAgentToolPermissionsForSpace returns the creator-inherited action
// policy after proving the creator can currently use the Agent in this Space.
func (db *Database) PersonalAgentToolPermissionsForSpace(ctx context.Context, userID, spaceID, agentID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		raw = json.RawMessage(`{"mode":"inherit_creator","read":true,"write":true,"integrations":["discord","google","notion","slack"]}`)
		return nil
	})
	return raw, err
}

// EffectivePersonalAgentContextPermissions returns the companion's fixed Space
// context after rechecking creator membership and enabled state. Membership
// revocation or disabling the Agent takes effect on the next operation.
func (db *Database) EffectivePersonalAgentContextPermissions(ctx context.Context, userID, spaceID, agentID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		raw = json.RawMessage(`{"space_chat":true,"library":true,"task_notes":true,"tasks":true,"members":true}`)
		return nil
	})
	return raw, err
}

func (db *Database) CreatePersonalAgentSpaceRun(ctx context.Context, userID, spaceID, agentID, sourceConversationID, sourceType, triggerKind string, input, envelope json.RawMessage) (*SpaceRun, error) {
	if sourceType != "group_mention" && sourceType != "direct" && sourceType != "suggestion" && sourceType != "follow_up" && sourceType != RunSourceAgentConsole ||
		triggerKind != "mention" && triggerKind != "direct" && triggerKind != "delegation" && triggerKind != "suggestion" && triggerKind != "follow_up" && triggerKind != RunSourceAgentConsole {
		return nil, ErrSpaceInvalid
	}
	if !validJSONObject(input) || !validJSONObject(envelope) {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		membership, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		scope := NormalizeConversationScope("")
		sourceMessageID := ""
		if sourceConversationID != "" {
			var isConversation bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversations WHERE id=$1 AND space_id=$2)`, sourceConversationID, spaceID).Scan(&isConversation); err != nil {
				return err
			}
			if isConversation {
				scope = NormalizeConversationScope(sourceConversationID)
			} else {
				sourceMessageID = sourceConversationID
			}
		}
		out = &SpaceRun{
			ID: "run_" + uuid.NewString(), SpaceID: spaceID, ResourceKind: "agent", ResourceID: agentID,
			InitiatedByUserID: userID, BillingUserID: userID, TriggerKind: triggerKind, State: "running", Input: input,
			Result: json.RawMessage(`{}`), RequestingMemberID: userID, SourceConversationID: sourceConversationID,
			SourceType: sourceType, AgentID: agentID, CapabilityID: "chat", Outputs: json.RawMessage(`{}`), Artifacts: json.RawMessage(`[]`),
			Attempt: 1, ActionEnvelope: envelope,
			ConversationScopeKind: scope.Kind, ScopeConversationID: scope.ConversationID, SourceMessageID: sourceMessageID,
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `INSERT INTO space_runs(
			id,space_id,resource_kind,resource_id,initiated_by_user_id,billing_user_id,trigger_kind,state,input,result,
			requesting_member_id,source_conversation_id,source_type,agent_id,capability_id,outputs,artifacts,attempt,action_envelope,
			conversation_scope_kind,scope_conversation_id,source_message_id
		) VALUES($1,$2,'agent',$3,$4,$4,$5,'running',$6,'{}'::jsonb,$4,NULLIF($7,''),$8,$3,'chat','{}'::jsonb,'[]'::jsonb,1,$9,$10,NULLIF($11,''),NULLIF($12,''))
		RETURNING `+spaceRunColumns, out.ID, spaceID, agentID, userID, triggerKind, input, sourceConversationID, sourceType, envelope, scope.Kind, scope.ConversationID, sourceMessageID), out); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.run.started", out.ID, map[string]any{"agent_id": agentID, "approved_agent_version_id": membership.ApprovedVersionID, "source_type": sourceType})
		return err
	})
	return out, err
}
