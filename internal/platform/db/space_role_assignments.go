package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/misty-org/misty-server/internal/spaces"
)

func (db *Database) ReorderSpaceRoles(ctx context.Context, userID, spaceID string, roleIDs []string) ([]SpaceRole, error) {
	var out []SpaceRole
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := requireRolesManagerTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		roles, err := spaceRolesTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		before := map[string]int{}
		for _, role := range roles {
			if !role.IsDefault {
				before[role.ID] = role.Position
			}
		}
		after := make(map[string]int, len(roleIDs))
		for index, roleID := range roleIDs {
			roleID = strings.TrimSpace(roleID)
			if _, ok := before[roleID]; !ok {
				return ErrSpaceInvalid
			}
			if _, seen := after[roleID]; seen {
				return ErrSpaceInvalid
			}
			after[roleID] = len(roleIDs) - index
		}
		if len(after) != len(before) {
			return ErrSpaceInvalid
		}
		if !spaces.ReorderAllowed(access.owner, access.highest, before, after) {
			return ErrSpaceForbidden
		}
		for roleID, position := range after {
			if before[roleID] == position {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_roles SET position=$2,updated_at=NOW() WHERE id=$1`, roleID, position); err != nil {
				return err
			}
		}
		if out, err = spaceRolesTx(ctx, tx, spaceID); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "roles.reordered", spaceID, map[string]any{"roles": out})
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (db *Database) SetSpaceMemberRoles(ctx context.Context, userID, spaceID, memberID string, roleIDs []string) (*SpaceMember, error) {
	desired := map[string]bool{}
	for _, roleID := range roleIDs {
		desired[strings.TrimSpace(roleID)] = true
	}
	var out *SpaceMember
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := requireRolesManagerTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		target, err := memberAccessTx(ctx, tx, spaceID, memberID)
		if errors.Is(err, ErrSpaceForbidden) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		roles, err := spaceRolesTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		positions := map[string]int{}
		for _, role := range roles {
			if !role.IsDefault {
				positions[role.ID] = role.Position
			}
		}
		for roleID := range desired {
			if _, ok := positions[roleID]; !ok {
				return ErrSpaceInvalid
			}
		}
		readers, err := channelReadersTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		changed, err := applyMemberRolesTx(ctx, tx, access, spaceID, memberID, target.roleIDs, desired, positions)
		if err != nil {
			return err
		}
		if out, err = spaceMemberTx(ctx, tx, spaceID, memberID); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "member.updated", memberID,
			map[string]any{"user_id": memberID, "role_ids": out.RoleIDs}); err != nil {
			return err
		}
		return announceChannelAccessTx(ctx, tx, spaceID, userID, readers, "")
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func applyMemberRolesTx(ctx context.Context, tx *sql.Tx, access *memberAccess, spaceID, memberID string, held []string, desired map[string]bool, positions map[string]int) (bool, error) {
	current := map[string]bool{}
	for _, roleID := range held {
		current[roleID] = true
	}
	changed := false
	for roleID := range desired {
		if current[roleID] || !spaces.CanManageRole(access.owner, access.highest, positions[roleID]) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_member_roles(space_id,user_id,role_id) VALUES($1,$2,$3)`, spaceID, memberID, roleID); err != nil {
			return false, err
		}
		changed = true
	}
	for roleID := range current {
		if desired[roleID] || !spaces.CanManageRole(access.owner, access.highest, positions[roleID]) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_member_roles WHERE space_id=$1 AND user_id=$2 AND role_id=$3`, spaceID, memberID, roleID); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func spaceMemberRoleIDsTx(ctx context.Context, tx *sql.Tx, spaceID string) (map[string][]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT mr.user_id,mr.role_id FROM space_member_roles mr
		JOIN space_roles r ON r.id=mr.role_id
		WHERE mr.space_id=$1 AND NOT r.is_default ORDER BY r.position DESC`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var userID, roleID string
		if err := rows.Scan(&userID, &roleID); err != nil {
			return nil, err
		}
		out[userID] = append(out[userID], roleID)
	}
	return out, rows.Err()
}

func spaceMemberTx(ctx context.Context, tx *sql.Tx, spaceID, memberID string) (*SpaceMember, error) {
	out := &SpaceMember{}
	err := tx.QueryRowContext(ctx, `SELECT m.space_id,m.user_id,u.username,u.email,m.role,m.joined_at,m.read_message_seq,u.avatar_version
		FROM space_members m JOIN users u ON u.id=m.user_id
		WHERE m.space_id=$1 AND m.user_id=$2`, spaceID, memberID).
		Scan(&out.SpaceID, &out.UserID, &out.Name, &out.Email, &out.Role, &out.JoinedAt, &out.ReadSeq, &out.AvatarVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	roleIDs, err := spaceMemberRoleIDsTx(ctx, tx, spaceID)
	if err != nil {
		return nil, err
	}
	out.RoleIDs = roleIDs[memberID]
	if out.RoleIDs == nil {
		out.RoleIDs = []string{}
	}
	return out, nil
}

func requireMemberRemovalTx(ctx context.Context, tx *sql.Tx, actorID, spaceID, memberID string) error {
	access, err := memberAccessTx(ctx, tx, spaceID, actorID)
	if err != nil {
		return err
	}
	if !access.base.Has(PermissionMembersRemove) {
		return ErrSpaceForbidden
	}
	if actorID == memberID {
		return ErrSpaceInvalid
	}
	target, err := memberAccessTx(ctx, tx, spaceID, memberID)
	if errors.Is(err, ErrSpaceForbidden) {
		return ErrSpaceNotFound
	}
	if err != nil {
		return err
	}
	if !spaces.CanRemoveMember(access.owner, access.highest, target.owner, target.highest) {
		return ErrSpaceForbidden
	}
	return nil
}
