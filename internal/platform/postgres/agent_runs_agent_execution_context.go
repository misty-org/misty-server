package db

import (
	"context"
	"database/sql"
	"errors"
)

func (db *Database) AgentExecutionContext(ctx context.Context, userID, spaceID, agentID string, versionIDs ...string) (*SpaceStudioResource, *WorkflowVersion, error) {
	resource := &SpaceStudioResource{Kind: "agent"}
	var workflow *WorkflowVersion
	agentVersionID, workflowVersionID := "", ""
	if len(versionIDs) == 1 {
		workflowVersionID = versionIDs[0]
	} else if len(versionIDs) == 2 {
		agentVersionID, workflowVersionID = versionIDs[0], versionIDs[1]
	} else {
		return nil, nil, ErrSpaceInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		if agentVersionID == "" {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(published_agent_version_id,'') FROM space_agents WHERE id=$1 AND space_id=$2`, agentID, spaceID).Scan(&agentVersionID); err != nil {
				return err
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT a.id,a.space_id,a.creator_user_id,v.name,v.description,v.icon,v.instructions,a.enabled,a.status,a.runtime_kind,a.version,a.schedules_enabled,COALESCE(a.active_workflow_version_id,''),a.created_at,a.updated_at FROM space_agents a JOIN space_agent_versions v ON v.id=$3 AND v.agent_id=a.id WHERE a.id=$1 AND a.space_id=$2 AND a.enabled AND a.status='available'`, agentID, spaceID, agentVersionID).Scan(&resource.ID, &resource.SpaceID, &resource.CreatorUserID, &resource.Name, &resource.Description, &resource.Icon, &resource.Instructions, &resource.Enabled, &resource.Status, &resource.RuntimeKind, &resource.Version, &resource.SchedulesEnabled, &resource.ActiveWorkflowVersionID, &resource.CreatedAt, &resource.UpdatedAt); err != nil {
			return err
		}
		if workflowVersionID != "" {
			var err error
			workflow, err = loadWorkflowVersionTx(ctx, tx, workflowVersionID)
			if err != nil {
				return err
			}
			var attached bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_agent_version_workflows WHERE agent_version_id=$1 AND workflow_version_id=$2 AND enabled)`, agentVersionID, workflowVersionID).Scan(&attached); err != nil || !attached || workflow.SpaceID != spaceID {
				return ErrSpaceForbidden
			}
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrSpaceNotFound
	}
	return resource, workflow, err
}

func authorizeWorkflowRequirementsTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, metadata WorkflowMetadata) error {
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
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_integrations WHERE space_id=$1 AND provider=$2 AND status='active')`, spaceID, provider).Scan(&available); err != nil {
			return err
		}
		if !available {
			return ErrWorkflowIntegrationRequired
		}
	}
	return nil
}

func TestingWorkflowPermissionSpacePermission(permission string) (string, bool) {
	switch permission {
	case "files.read":
		return PermissionLibraryView, true
	case "files.write":
		return PermissionLibraryEdit, true
	}
	for _, candidate := range configurableSpacePermissions {
		if permission == candidate {
			return candidate, true
		}
	}
	return "", false
}

func selectWorkflowCapability(metadata WorkflowMetadata, requested string) (*WorkflowCapability, error) {
	if requested == "" && len(metadata.Capabilities) == 1 {
		return &metadata.Capabilities[0], nil
	}
	for index := range metadata.Capabilities {
		if metadata.Capabilities[index].ID == requested {
			return &metadata.Capabilities[index], nil
		}
	}
	return nil, ErrSpaceInvalid
}

const RunSourceAgentConsole = "agent_console"

// Every value here must also appear in the space_runs source_type CHECK, or the
// run passes validation and then fails at insert time. "connector" and "task"
// were accepted here long before the constraint listed them, which is why
// 20260916000000_rename_agent_run_source_type.sql adds them.
func validRunSource(value string) bool {
	switch value {
	case "direct",
		"group_mention",
		RunSourceAgentConsole,
		"studio_test",
		"schedule",
		"connector",
		"task",
		"suggestion",
		"follow_up":
		return true
	default:
		return false
	}
}

func sharedSpaceRunVisibleToUserTx(ctx context.Context, tx *sql.Tx, run *SpaceRun, userID string) (bool, error) {
	if run.RequestingMemberID == userID {
		return true, nil
	}
	if run.ConversationScopeKind == ConversationScopePrivate && run.ScopeConversationID != "" {
		var member bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.actor_kind='person' AND cm.user_id=$2 AND c.space_id=$3)`, run.ScopeConversationID, userID, run.SpaceID).Scan(&member); err != nil {
			return false, err
		}
		return member, nil
	}
	switch run.SourceType {
	case "schedule":
		return true, nil
	case "suggestion", "follow_up":
		return run.ConversationScopeKind == ConversationScopeEveryone, nil
	case "group_mention":
		var conversationExists, member bool
		if err := tx.QueryRowContext(ctx, `SELECT
			EXISTS(SELECT 1 FROM space_conversations c WHERE c.id=$1 AND c.space_id=$2),
			EXISTS(SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id WHERE cm.conversation_id=$1 AND cm.user_id=$3 AND c.space_id=$2)`, run.SourceConversationID, run.SpaceID, userID).Scan(&conversationExists, &member); err != nil {
			return false, err
		}
		if conversationExists {
			return member, nil
		}
		return true, nil // Everyone chat stores its source message ID here.
	default:
		return false, nil
	}
}

const sharedSpaceRunListVisibility = `(requesting_member_id=$3 OR source_type='schedule' OR
	(conversation_scope_kind='everyone' AND source_type IN ('group_mention','suggestion','follow_up')) OR
	(conversation_scope_kind='conversation' AND EXISTS(
		SELECT 1 FROM space_conversation_members cm JOIN space_conversations c ON c.id=cm.conversation_id
		WHERE cm.conversation_id=space_runs.scope_conversation_id AND cm.actor_kind='person' AND cm.user_id=$3 AND c.space_id=space_runs.space_id
	)))`

func (db *Database) SpaceRuns(ctx context.Context, userID, spaceID, agentID string, limit int) ([]SpaceRun, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	items := []SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs WHERE space_id=$1 AND ($2='' OR agent_id=$2) AND `+sharedSpaceRunListVisibility+` ORDER BY created_at DESC LIMIT $4`, spaceID, agentID, userID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceRun
			if err := scanSpaceRun(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SpaceWorkflowRuns(ctx context.Context, userID, spaceID, workflowID string, limit int) ([]SpaceRun, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	items := []SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStudioView); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs WHERE space_id=$1 AND resource_kind='workflow' AND resource_id=$2 AND `+sharedSpaceRunListVisibility+` ORDER BY created_at DESC LIMIT $4`, spaceID, workflowID, userID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceRun
			if err := scanSpaceRun(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) SpaceRun(ctx context.Context, userID, runID string) (*SpaceRun, error) {
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `SELECT `+spaceRunColumns+` FROM space_runs WHERE id=$1`, runID), out); err != nil {
			return err
		}
		visible, err := sharedSpaceRunVisibleToUserTx(ctx, tx, out, userID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrSpaceForbidden
		}
		if out.RequestingMemberID == userID {
			return requireSpacePermissionTx(ctx, tx, userID, out.SpaceID, PermissionAgentsRun)
		}
		return requireSpacePermissionTx(ctx, tx, userID, out.SpaceID, PermissionStudioView)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}
