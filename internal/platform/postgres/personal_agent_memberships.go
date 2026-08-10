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

var defaultAgentSpacePermissions = json.RawMessage(`{"messages.read":true,"messages.write":true,"tasks.view":true,"tasks.manage":true,"attached_files.read":true}`)

type SpaceAgentMembership struct {
	ID                string          `json:"id"`
	SpaceID           string          `json:"space_id"`
	AgentID           string          `json:"agent_id"`
	OwnerUserID       string          `json:"owner_user_id"`
	Name              string          `json:"name"`
	Role              string          `json:"role"`
	Description       string          `json:"description"`
	Icon              string          `json:"icon"`
	Avatar            json.RawMessage `json:"avatar"`
	Instructions      string          `json:"instructions,omitempty"`
	ModelID           string          `json:"model_id,omitempty"`
	ReasoningEffort   string          `json:"reasoning_effort,omitempty"`
	Enabled           bool            `json:"enabled"`
	RoleID            string          `json:"role_id"`
	CapabilityGrants  json.RawMessage `json:"capability_grants"`
	RolePermissions   json.RawMessage `json:"-"`
	ApprovedVersionID string          `json:"approved_version_id"`
	ApprovedVersion   int64           `json:"approved_version"`
	LatestVersionID   string          `json:"latest_version_id"`
	LatestVersion     int64           `json:"latest_version"`
	UpdateAvailable   bool            `json:"update_available"`
	SpaceRole         string          `json:"space_role"`
	SpaceInstructions string          `json:"space_instructions,omitempty"`
	Permissions       json.RawMessage `json:"permissions"`
	ManagedByUserID   string          `json:"managed_by_user_id,omitempty"`
	MembershipVersion int64           `json:"membership_version"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	WorkState         string          `json:"work_state"`
	AttentionCount    int64           `json:"attention_count"`
	LastActivityAt    *time.Time      `json:"last_activity_at,omitempty"`
	CurrentTaskID     string          `json:"current_task_id,omitempty"`
}

type SpaceAgentMembershipInput struct {
	AgentID           string          `json:"agent_id"`
	Enabled           *bool           `json:"enabled,omitempty"`
	RoleID            *string         `json:"role_id,omitempty"`
	CapabilityGrants  json.RawMessage `json:"capability_grants,omitempty"`
	SpaceRole         *string         `json:"space_role,omitempty"`
	SpaceInstructions string          `json:"space_instructions,omitempty"`
	Permissions       json.RawMessage `json:"permissions,omitempty"`
	MembershipVersion int64           `json:"membership_version,omitempty"`
}

const spaceAgentMembershipColumns = `g.id,g.space_id,g.agent_id,a.owner_user_id,v.name,v.role,v.description,v.icon,v.avatar,v.instructions,v.model_id,v.reasoning_effort,g.enabled,
	COALESCE(g.role_id,''),g.capability_grants,COALESCE(agent_role.permissions,'[]'::jsonb),
	g.approved_version_id,v.version,latest.id,latest.version,(g.approved_version_id<>latest.id),g.space_role,g.space_instructions,
	g.permissions,COALESCE(g.managed_by_user_id,''),g.version,g.created_at,g.updated_at,
	CASE WHEN NOT g.enabled OR NOT a.enabled THEN 'disabled'
		WHEN g.approved_version_id<>latest.id THEN 'update_available'
		WHEN COALESCE(run_summary.awaiting_count,0)>0 THEN 'needs_approval'
		WHEN COALESCE(run_summary.working_count,0)>0 THEN 'working'
		WHEN run_summary.latest_state IN ('failed','completed_with_errors') THEN 'failed'
		ELSE 'ready' END,
	COALESCE(run_summary.awaiting_count,0)+CASE WHEN run_summary.latest_state IN ('failed','completed_with_errors') THEN 1 ELSE 0 END,run_summary.last_activity_at,
	COALESCE(run_summary.current_task_id,'')`

func scanSpaceAgentMembership(row scanner, out *SpaceAgentMembership) error {
	return row.Scan(&out.ID, &out.SpaceID, &out.AgentID, &out.OwnerUserID, &out.Name, &out.Role, &out.Description, &out.Icon, &out.Avatar, &out.Instructions, &out.ModelID, &out.ReasoningEffort,
		&out.Enabled, &out.RoleID, &out.CapabilityGrants, &out.RolePermissions, &out.ApprovedVersionID, &out.ApprovedVersion, &out.LatestVersionID, &out.LatestVersion,
		&out.UpdateAvailable, &out.SpaceRole, &out.SpaceInstructions, &out.Permissions, &out.ManagedByUserID, &out.MembershipVersion,
		&out.CreatedAt, &out.UpdatedAt, &out.WorkState, &out.AttentionCount, &out.LastActivityAt, &out.CurrentTaskID)
}

const spaceAgentMembershipJoins = ` FROM personal_agent_space_grants g
	JOIN personal_agents a ON a.id=g.agent_id AND a.deleted_at IS NULL
	JOIN personal_agent_versions v ON v.id=g.approved_version_id AND v.agent_id=g.agent_id
	JOIN personal_agent_versions latest ON latest.agent_id=a.id AND latest.version=a.version
	LEFT JOIN space_roles agent_role ON agent_role.id=g.role_id AND agent_role.space_id=g.space_id
	LEFT JOIN LATERAL (
		SELECT COUNT(*) FILTER (WHERE r.state='awaiting_approval') AS awaiting_count,
			COUNT(*) FILTER (WHERE r.state IN ('queued','running','cooldown')) AS working_count,
			(ARRAY_AGG(r.state ORDER BY r.updated_at DESC))[1] AS latest_state,
			MAX(r.updated_at) AS last_activity_at,
			(ARRAY_AGG(r.source_task_id ORDER BY r.updated_at DESC) FILTER (
				WHERE r.source_task_id IS NOT NULL AND r.state IN ('queued','running','cooldown','awaiting_approval','failed','completed_with_errors')
			))[1] AS current_task_id
		FROM space_runs r
		WHERE r.space_id=g.space_id AND r.agent_id=g.agent_id
			AND r.source_type IN ('task','schedule','group_mention')
	) run_summary ON TRUE `

func (db *Database) SpaceAgentMemberships(ctx context.Context, userID, spaceID string) ([]SpaceAgentMembership, error) {
	items := []SpaceAgentMembership{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		canManage, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsManage)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceAgentMembershipColumns+spaceAgentMembershipJoins+`
			WHERE g.space_id=$1 AND g.removed_at IS NULL ORDER BY lower(v.name),g.id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceAgentMembership
			if err := scanSpaceAgentMembership(rows, &item); err != nil {
				return err
			}
			if !canManage {
				item.Instructions, item.SpaceInstructions, item.ModelID, item.ReasoningEffort = "", "", "", ""
			}
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
			WHERE g.space_id=$1 AND g.agent_id=$2 AND g.removed_at IS NULL`, spaceID, agentID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	return out, err
}

func (db *Database) AddSpaceAgentMembership(ctx context.Context, userID, spaceID string, input SpaceAgentMembershipInput) (*SpaceAgentMembership, error) {
	input.AgentID = strings.TrimSpace(input.AgentID)
	spaceRole := ""
	if input.SpaceRole != nil {
		spaceRole = strings.TrimSpace(*input.SpaceRole)
	}
	input.SpaceInstructions = strings.TrimSpace(input.SpaceInstructions)
	permissions, err := normalizeAgentSpacePermissions(input.Permissions)
	if err != nil || input.AgentID == "" || len([]rune(spaceRole)) > 80 || len([]rune(input.SpaceInstructions)) > 16_000 {
		return nil, ErrSpaceInvalid
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsManage); err != nil {
			return err
		}
		var owner string
		var latestVersionID string
		var latestRole string
		var requestedCapabilities json.RawMessage
		if err := tx.QueryRowContext(ctx, `SELECT a.owner_user_id,v.id,v.role,v.tool_permissions FROM personal_agents a JOIN personal_agent_versions v ON v.agent_id=a.id AND v.version=a.version
			WHERE a.id=$1 AND a.enabled AND a.deleted_at IS NULL`, input.AgentID).Scan(&owner, &latestVersionID, &latestRole, &requestedCapabilities); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPersonalAgentNotFound
			}
			return err
		}
		if owner != userID {
			return ErrSpaceForbidden
		}
		if input.SpaceRole == nil {
			spaceRole = latestRole
		}
		roleID := ""
		if input.RoleID != nil {
			roleID = strings.TrimSpace(*input.RoleID)
		}
		if roleID == "" {
			roleID, err = ensureAgentMemberRoleTx(ctx, tx, spaceID)
			if err != nil {
				return err
			}
		}
		if err := validateAgentMemberRoleTx(ctx, tx, spaceID, roleID); err != nil {
			return err
		}
		capabilityGrants, err := normalizeMembershipCapabilityGrants(input.CapabilityGrants, requestedCapabilities)
		if err != nil {
			return err
		}
		id := "agentgrant_" + uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO personal_agent_space_grants(
			id,agent_id,space_id,all_members,created_by_user_id,enabled,approved_version_id,space_role,space_instructions,permissions,managed_by_user_id,role_id,capability_grants,removed_at
		) VALUES($1,$2,$3,TRUE,$4,TRUE,$5,$6,$7,$8,$4,$9,$10,NULL)
		ON CONFLICT(agent_id,space_id) DO UPDATE SET all_members=TRUE,enabled=TRUE,approved_version_id=$5,
			space_role=$6,space_instructions=$7,permissions=$8,managed_by_user_id=$4,role_id=$9,capability_grants=$10,removed_at=NULL,version=personal_agent_space_grants.version+1,updated_at=NOW()`,
			id, input.AgentID, spaceID, userID, latestVersionID, spaceRole, input.SpaceInstructions, permissions, roleID, capabilityGrants)
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.membership.added", input.AgentID, map[string]any{"agent_id": input.AgentID})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceAgentMembership(ctx, userID, spaceID, input.AgentID)
}

func (db *Database) UpdateSpaceAgentMembership(ctx context.Context, userID, spaceID, agentID string, input SpaceAgentMembershipInput) (*SpaceAgentMembership, error) {
	var spaceRole any
	if input.SpaceRole != nil {
		normalized := strings.TrimSpace(*input.SpaceRole)
		if len([]rune(normalized)) > 80 {
			return nil, ErrSpaceInvalid
		}
		spaceRole = normalized
	}
	input.SpaceInstructions = strings.TrimSpace(input.SpaceInstructions)
	permissions, err := normalizeAgentSpacePermissions(input.Permissions)
	if err != nil || input.MembershipVersion < 1 || len([]rune(input.SpaceInstructions)) > 16_000 {
		return nil, ErrSpaceInvalid
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsManage); err != nil {
			return err
		}
		var roleID any
		if input.RoleID != nil {
			normalized := strings.TrimSpace(*input.RoleID)
			if err := validateAgentMemberRoleTx(ctx, tx, spaceID, normalized); err != nil {
				return err
			}
			roleID = normalized
		}
		var capabilityGrants any
		if len(input.CapabilityGrants) > 0 {
			var requested json.RawMessage
			if err := tx.QueryRowContext(ctx, `SELECT v.tool_permissions FROM personal_agent_space_grants g JOIN personal_agent_versions v ON v.id=g.approved_version_id WHERE g.space_id=$1 AND g.agent_id=$2 AND g.removed_at IS NULL`, spaceID, agentID).Scan(&requested); err != nil {
				return err
			}
			capabilityGrants, err = normalizeMembershipCapabilityGrants(input.CapabilityGrants, requested)
			if err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE personal_agent_space_grants SET enabled=$1,space_role=COALESCE($2,space_role),space_instructions=$3,permissions=$4,
			managed_by_user_id=$5,role_id=COALESCE($9,role_id),capability_grants=COALESCE($10,capability_grants),version=version+1,updated_at=NOW() WHERE space_id=$6 AND agent_id=$7 AND removed_at IS NULL AND version=$8`,
			enabled, spaceRole, input.SpaceInstructions, permissions, userID, spaceID, agentID, input.MembershipVersion, roleID, capabilityGrants)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrSpaceConflict
		}
		if !enabled {
			_, _ = tx.ExecContext(ctx, `UPDATE space_runs SET state='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW()
				WHERE space_id=$1 AND agent_id=$2 AND state IN ('queued','running','cooldown','awaiting_approval')`, spaceID, agentID)
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.membership.updated", agentID, map[string]any{"enabled": enabled})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceAgentMembership(ctx, userID, spaceID, agentID)
}

func (db *Database) ApproveSpaceAgentVersion(ctx context.Context, userID, spaceID, agentID string) (*SpaceAgentMembership, error) {
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE personal_agent_space_grants g SET approved_version_id=v.id,managed_by_user_id=$1,
			capability_grants=COALESCE((SELECT jsonb_agg(existing.value)
				FROM jsonb_array_elements(g.capability_grants) existing(value)
				WHERE EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(v.tool_permissions->'grants','[]'::jsonb)) requested
					WHERE requested->>'capability'=existing.value->>'capability' AND requested->>'risk'=existing.value->>'risk')),'[]'::jsonb),
			version=g.version+1,updated_at=NOW() FROM personal_agents a JOIN personal_agent_versions v ON v.agent_id=a.id AND v.version=a.version
			WHERE g.space_id=$2 AND g.agent_id=$3 AND g.agent_id=a.id AND g.removed_at IS NULL`, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrPersonalAgentNotFound
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.membership.version_approved", agentID, map[string]any{})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceAgentMembership(ctx, userID, spaceID, agentID)
}

func (db *Database) RemoveSpaceAgentMembership(ctx context.Context, userID, spaceID, agentID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsManage); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE personal_agent_space_grants SET enabled=FALSE,removed_at=NOW(),managed_by_user_id=$1,
			version=version+1,updated_at=NOW() WHERE space_id=$2 AND agent_id=$3 AND removed_at IS NULL`, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return ErrPersonalAgentNotFound
		}
		_, _ = tx.ExecContext(ctx, `UPDATE space_runs SET state='canceled',canceled_at=NOW(),completed_at=NOW(),updated_at=NOW()
			WHERE space_id=$1 AND agent_id=$2 AND state IN ('queued','running','cooldown','awaiting_approval')`, spaceID, agentID)
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "agent.membership.removed", agentID, map[string]any{})
		return err
	})
}

func activePersonalAgentMembershipTx(ctx context.Context, tx *sql.Tx, userID, spaceID, agentID string) (*SpaceAgentMembership, error) {
	if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
		return nil, err
	}
	out := &SpaceAgentMembership{}
	err := scanSpaceAgentMembership(tx.QueryRowContext(ctx, `SELECT `+spaceAgentMembershipColumns+spaceAgentMembershipJoins+`
		WHERE g.space_id=$1 AND g.agent_id=$2 AND g.removed_at IS NULL AND g.enabled AND a.enabled AND
			(g.all_members OR a.owner_user_id=$3 OR EXISTS(SELECT 1 FROM personal_agent_member_grants mg WHERE mg.grant_id=g.id AND mg.user_id=$3))`, spaceID, agentID, userID), out)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	return out, err
}

func (db *Database) EffectiveAgentSpacePermission(ctx context.Context, userID, spaceID, agentID, permission string) (bool, error) {
	allowed := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		memberAllowed, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, permission)
		if err != nil || !memberAllowed {
			return err
		}
		membership, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		allowed = agentRolePermission(membership, permission)
		return nil
	})
	return allowed, err
}

func (db *Database) EffectivePersonalAgentToolPermissions(ctx context.Context, userID, spaceID, agentID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		membership, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		var requested json.RawMessage
		if err := tx.QueryRowContext(ctx, `SELECT v.tool_permissions FROM personal_agent_versions v WHERE v.id=$1 AND v.agent_id=$2`, membership.ApprovedVersionID, agentID).Scan(&requested); err != nil {
			return err
		}
		var policy struct {
			Read         bool                   `json:"read"`
			Write        bool                   `json:"write"`
			Integrations []string               `json:"integrations"`
			Grants       []AgentCapabilityGrant `json:"grants"`
		}
		var approved []AgentCapabilityGrant
		if json.Unmarshal(requested, &policy) != nil || json.Unmarshal(membership.CapabilityGrants, &approved) != nil {
			return ErrSpaceInvalid
		}
		allowed := map[string]bool{}
		for _, grant := range approved {
			allowed[grant.Capability+"\x00"+grant.Risk] = true
		}
		filtered := make([]AgentCapabilityGrant, 0, len(policy.Grants))
		for _, grant := range policy.Grants {
			if allowed[grant.Capability+"\x00"+grant.Risk] {
				filtered = append(filtered, grant)
			}
		}
		policy.Grants = filtered
		raw, err = json.Marshal(policy)
		return err
	})
	return raw, err
}

// PersonalAgentToolPermissionsForSpace returns the owner-authored action policy
// to the server after proving the requester can see the Agent in this Space.
// Callers use it to build a public capability manual; the raw policy is never
// returned to Space members.
func (db *Database) PersonalAgentToolPermissionsForSpace(ctx context.Context, userID, spaceID, agentID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := personalAgentAllowedTx(ctx, tx, userID, spaceID, agentID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT a.tool_permissions FROM personal_agents a
			JOIN personal_agent_space_grants g ON g.agent_id=a.id
			WHERE a.id=$1 AND g.space_id=$2 AND g.removed_at IS NULL`, agentID, spaceID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPersonalAgentNotFound
		}
		return err
	})
	return raw, err
}

// EffectivePersonalAgentContextPermissions returns the owner-configured
// readable-context policy only after rechecking that both the member and Agent
// still have an active grant to the Space. Context is authorization too: a
// revoked or disabled Agent must lose it on the very next turn.
func (db *Database) EffectivePersonalAgentContextPermissions(ctx context.Context, userID, spaceID, agentID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		membership, err := activePersonalAgentMembershipTx(ctx, tx, userID, spaceID, agentID)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT context_permissions FROM personal_agents WHERE id=$1 AND enabled AND deleted_at IS NULL`, agentID).Scan(&raw); err != nil {
			return err
		}
		var allowed map[string]bool
		if json.Unmarshal(raw, &allowed) != nil || allowed == nil {
			return ErrSpaceInvalid
		}
		if !agentMembershipPermission(membership.Permissions, PermissionMessagesRead) {
			allowed["space_chat"] = false
		}
		if !agentMembershipPermission(membership.Permissions, PermissionTasksView) {
			allowed["tasks"], allowed["task_notes"], allowed["notes"] = false, false, false
		}
		raw, err = json.Marshal(allowed)
		return err
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
