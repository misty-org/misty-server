package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (db *Database) ReplacePersonalAgentGrants(ctx context.Context, userID, agentID string, inputs []PersonalAgentGrantInput) ([]PersonalAgentSpaceGrant, error) {
	if len(inputs) > 100 {
		return nil, ErrSpaceInvalid
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM personal_agents WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, agentID).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
			return ErrPersonalAgentNotFound
		} else if err != nil {
			return err
		}
		if owner != userID {
			return ErrSpaceForbidden
		}
		seen := map[string]bool{}
		for _, input := range inputs {
			input.SpaceID = strings.TrimSpace(input.SpaceID)
			if input.SpaceID == "" || seen[input.SpaceID] {
				return ErrSpaceInvalid
			}
			seen[input.SpaceID] = true
			if err := requireSpacePermissionTx(ctx, tx, userID, input.SpaceID, PermissionAgentsRun); err != nil {
				return err
			}
			grantID := "agentgrant_" + uuid.NewString()
			var latestVersionID string
			var latestRole string
			var requestedCapabilities json.RawMessage
			if err := tx.QueryRowContext(ctx, `SELECT id,role,tool_permissions FROM personal_agent_versions WHERE agent_id=$1 ORDER BY version DESC LIMIT 1`, agentID).Scan(&latestVersionID, &latestRole, &requestedCapabilities); err != nil {
				return err
			}
			roleID, err := ensureAgentMemberRoleTx(ctx, tx, input.SpaceID)
			if err != nil {
				return err
			}
			capabilityGrants, err := normalizeMembershipCapabilityGrants(nil, requestedCapabilities)
			if err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `INSERT INTO personal_agent_space_grants(
				id,agent_id,space_id,all_members,created_by_user_id,approved_version_id,space_role,managed_by_user_id,enabled,role_id,capability_grants,removed_at
			) VALUES($1,$2,$3,TRUE,$4,$5,$6,$4,TRUE,$7,$8,NULL)
				ON CONFLICT(agent_id,space_id) DO UPDATE SET all_members=EXCLUDED.all_members,approved_version_id=EXCLUDED.approved_version_id,
					managed_by_user_id=EXCLUDED.managed_by_user_id,enabled=TRUE,role_id=EXCLUDED.role_id,capability_grants=EXCLUDED.capability_grants,removed_at=NULL,version=personal_agent_space_grants.version+1,updated_at=NOW()
				RETURNING id`, grantID, agentID, input.SpaceID, userID, latestVersionID, latestRole, roleID, capabilityGrants).Scan(&grantID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM personal_agent_member_grants WHERE grant_id=$1`, grantID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE personal_agent_space_grants SET enabled=FALSE,removed_at=NOW(),managed_by_user_id=$2,
			version=version+1,updated_at=NOW() WHERE agent_id=$1 AND removed_at IS NULL AND NOT (space_id = ANY($3::text[]))`, agentID, userID, pqStringArray(mapKeys(seen)))
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.PersonalAgentGrants(ctx, userID, agentID)
}

func mapKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func personalAgentAllowedTx(ctx context.Context, tx *sql.Tx, userID, spaceID, agentID string) (*PersonalAgent, error) {
	if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
		return nil, err
	}
	if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
		return nil, err
	}
	out := &PersonalAgent{}
	err := scanPersonalAgent(tx.QueryRowContext(ctx, `SELECT `+personalAgentColumns+` FROM personal_agents a WHERE a.id=$1 AND a.enabled AND a.deleted_at IS NULL AND
		EXISTS(SELECT 1 FROM personal_agent_space_grants g WHERE g.agent_id=a.id AND g.space_id=$2 AND g.enabled AND g.removed_at IS NULL AND
			(g.all_members OR a.owner_user_id=$3 OR EXISTS(SELECT 1 FROM personal_agent_member_grants mg WHERE mg.grant_id=g.id AND mg.user_id=$3)))`, agentID, spaceID, userID), out)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonalAgentNotFound
	}
	return out, err
}

func (db *Database) PersonalAgentForSpace(ctx context.Context, userID, spaceID, agentID string) (*PersonalAgent, error) {
	var out *PersonalAgent
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = personalAgentAllowedTx(ctx, tx, userID, spaceID, agentID)
		return err
	})
	return out, err
}

func (db *Database) AccessiblePersonalAgents(ctx context.Context, userID, spaceID string) ([]PersonalAgent, error) {
	items := []PersonalAgent{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
			return err
		}
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAgentsRun); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+personalAgentColumns+` FROM personal_agents a WHERE a.enabled AND a.deleted_at IS NULL AND EXISTS(
			SELECT 1 FROM personal_agent_space_grants g WHERE g.agent_id=a.id AND g.space_id=$1 AND g.enabled AND g.removed_at IS NULL AND
				(g.all_members OR a.owner_user_id=$2 OR EXISTS(SELECT 1 FROM personal_agent_member_grants mg WHERE mg.grant_id=g.id AND mg.user_id=$2))) ORDER BY lower(a.name),a.id`, spaceID, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item PersonalAgent
			if err := scanPersonalAgent(rows, &item); err != nil {
				return err
			}
			if item.OwnerUserID != userID {
				item.Instructions = ""
				item.ContextPermissions = nil
				item.ToolPermissions = nil
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// PersonalAgentSpaceContext returns a bounded, permission-checked snapshot of
// shared Space material. Conversation-scoped runs use the conversation-aware
// variant so limited-group and direct content cannot cross context boundaries.
func (db *Database) PersonalAgentSpaceContext(ctx context.Context, userID, spaceID string, permissions json.RawMessage) (string, error) {
	return db.PersonalAgentSpaceContextForConversation(ctx, userID, spaceID, "", permissions)
}
