package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func normalizeAgentSpacePermissions(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return append(json.RawMessage(nil), defaultAgentSpacePermissions...), nil
	}
	var values map[string]bool
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return nil, ErrSpaceInvalid
	}
	allowed := map[string]bool{
		PermissionMessagesRead: true, PermissionMessagesWrite: true,
		PermissionTasksView: true, PermissionTasksManage: true,
		"attached_files.read": true,
	}
	for key := range values {
		if !allowed[key] {
			return nil, ErrSpaceInvalid
		}
	}
	if !values[PermissionMessagesRead] {
		values[PermissionMessagesWrite] = false
	}
	if !values[PermissionTasksView] {
		values[PermissionTasksManage] = false
	}
	return json.Marshal(values)
}

func TestingNormalizeAgentSpacePermissions(raw json.RawMessage) (json.RawMessage, error) {
	return normalizeAgentSpacePermissions(raw)
}

func normalizeMembershipCapabilityGrants(raw json.RawMessage, requested json.RawMessage) (json.RawMessage, error) {
	requestedSet := map[string]bool{}
	var policy struct {
		Grants []AgentCapabilityGrant `json:"grants"`
	}
	if len(requested) > 0 && json.Unmarshal(requested, &policy) != nil {
		return nil, ErrSpaceInvalid
	}
	for _, grant := range policy.Grants {
		if value := strings.TrimSpace(grant.Capability); value != "" {
			requestedSet[value+"\x00"+strings.TrimSpace(grant.Risk)] = true
		}
	}
	values := []AgentCapabilityGrant{}
	if len(raw) == 0 {
		values = append(values, policy.Grants...)
	} else if json.Unmarshal(raw, &values) != nil {
		return nil, ErrSpaceInvalid
	}
	seen := map[string]bool{}
	normalized := make([]AgentCapabilityGrant, 0, len(values))
	for _, grant := range values {
		grant.Capability = strings.TrimSpace(grant.Capability)
		grant.Risk = strings.TrimSpace(grant.Risk)
		key := grant.Capability + "\x00" + grant.Risk
		if grant.Capability == "" || seen[key] || !requestedSet[key] {
			if grant.Capability == "" || !requestedSet[key] {
				return nil, ErrSpaceInvalid
			}
			continue
		}
		seen[key] = true
		normalized = append(normalized, grant)
	}
	return json.Marshal(normalized)
}

func ensureAgentMemberRoleTx(ctx context.Context, tx *sql.Tx, spaceID string) (string, error) {
	var roleID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM space_roles WHERE space_id=$1 AND name='Agent member' ORDER BY created_at LIMIT 1`, spaceID).Scan(&roleID)
	if err == nil {
		return roleID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	roleID = "role_agent_member_" + uuid.NewString()
	permissions := `["space.view","messages.read","messages.write","attachments.upload","library.view","library.upload","library.add","library.edit","library.download","library.import","tasks.view","tasks.manage","agents.run"]`
	_, err = tx.ExecContext(ctx, `INSERT INTO space_roles(id,space_id,name,is_everyone,permissions) VALUES($1,$2,'Agent member',FALSE,$3::jsonb)`, roleID, spaceID, permissions)
	return roleID, err
}

func validateAgentMemberRoleTx(ctx context.Context, tx *sql.Tx, spaceID, roleID string) error {
	var permissions json.RawMessage
	if err := tx.QueryRowContext(ctx, `SELECT permissions FROM space_roles WHERE id=$1 AND space_id=$2`, roleID, spaceID).Scan(&permissions); err != nil {
		return ErrSpaceInvalid
	}
	var values []string
	if json.Unmarshal(permissions, &values) != nil {
		return ErrSpaceInvalid
	}
	for _, value := range values {
		switch value {
		case PermissionAgentsManage, PermissionIntegrationsManage, PermissionStorageManage, PermissionStudioManage:
			return ErrSpaceForbidden
		}
	}
	return nil
}

func agentMembershipPermission(raw json.RawMessage, permission string) bool {
	var values map[string]bool
	return json.Unmarshal(raw, &values) == nil && values[permission]
}

func agentRolePermission(membership *SpaceAgentMembership, permission string) bool {
	switch permission {
	case PermissionAgentsManage, PermissionIntegrationsManage, PermissionStorageManage, PermissionStudioManage:
		return false
	}
	var values []string
	if json.Unmarshal(membership.RolePermissions, &values) != nil {
		return false
	}
	for _, value := range values {
		if value == permission {
			return true
		}
	}
	return false
}
