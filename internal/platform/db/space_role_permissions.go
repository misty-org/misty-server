package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	"github.com/misty-org/misty-server/internal/spaces"
)

const (
	PermissionAdministrator  = spaces.PermissionAdministrator
	PermissionRolesManage    = spaces.PermissionRolesManage
	PermissionChannelsManage = spaces.PermissionChannelsManage
	PermissionMessagesManage = spaces.PermissionMessagesManage
	PermissionMembersRemove  = spaces.PermissionMembersRemove
	PermissionSpaceInvite    = spaces.PermissionSpaceInvite
	PermissionSpaceRename    = spaces.PermissionSpaceRename
)

type memberAccess struct {
	owner          bool
	everyoneRoleID string
	roleIDs        []string
	highest        int
	base           spaces.PermissionSet
}

func memberAccessTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) (*memberAccess, error) {
	role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.position,r.permissions,r.is_default FROM space_roles r
		WHERE r.space_id=$1 AND (r.is_default OR EXISTS(SELECT 1 FROM space_member_roles mr
		  WHERE mr.space_id=r.space_id AND mr.user_id=$2 AND mr.role_id=r.id))`, spaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	access := &memberAccess{owner: role == "owner"}
	roles := []spaces.Role{}
	for rows.Next() {
		var item spaces.Role
		if err := rows.Scan(&item.ID, &item.Position, pq.Array(&item.Permissions), &item.IsDefault); err != nil {
			return nil, err
		}
		if item.IsDefault {
			access.everyoneRoleID = item.ID
		} else {
			access.roleIDs = append(access.roleIDs, item.ID)
		}
		roles = append(roles, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if access.everyoneRoleID == "" {
		roles = append(roles, spaces.Role{IsDefault: true, Permissions: spaces.DefaultEveryonePermissions()})
	}
	access.highest = spaces.HighestPosition(roles)
	if access.owner {
		access.base = spaces.OwnerPermissions()
	} else {
		access.base = spaces.BasePermissions(roles)
	}
	return access, nil
}

func requireSpaceAuthorityTx(ctx context.Context, tx *sql.Tx, spaceID, userID, permission string) error {
	access, err := memberAccessTx(ctx, tx, spaceID, userID)
	if err != nil {
		return err
	}
	if !access.base.Has(permission) {
		return ErrSpaceForbidden
	}
	return nil
}

func (db *Database) RequireSpacePermission(ctx context.Context, userID, spaceID, permission string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return requireSpaceAuthorityTx(ctx, tx, spaceID, userID, permission)
	})
}

func ownerPermissionList() []string { return spaces.OwnerPermissions().List() }

func spacePermissionListTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) ([]string, error) {
	access, err := memberAccessTx(ctx, tx, spaceID, userID)
	if err != nil {
		return nil, err
	}
	return access.base.List(), nil
}

func channelPermissionsForTx(ctx context.Context, tx *sql.Tx, access *memberAccess, userID, spaceID, conversationID string) (spaces.PermissionSet, string, error) {
	var kind string
	var open bool
	err := tx.QueryRowContext(ctx, `SELECT c.kind,c.visible_to_space OR EXISTS(
		SELECT 1 FROM space_conversation_members cm WHERE cm.conversation_id=c.id AND cm.user_id=$3)
		FROM space_conversations c WHERE c.id=$1 AND c.space_id=$2`, conversationID, spaceID, userID).Scan(&kind, &open)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrSpaceForbidden
	}
	if err != nil {
		return nil, "", err
	}
	if !open {
		return nil, "", ErrSpaceForbidden
	}
	if kind != "standard" {
		return spaces.DirectPermissions(access.base), kind, nil
	}
	overwrites, err := conversationOverwritesTx(ctx, tx, conversationID)
	if err != nil {
		return nil, "", err
	}
	return spaces.ChannelPermissions(access.base, access.everyoneRoleID, access.roleIDs, domainOverwrites(overwrites)), kind, nil
}

func channelPermissionsTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) (spaces.PermissionSet, string, error) {
	access, err := memberAccessTx(ctx, tx, spaceID, userID)
	if err != nil {
		return nil, "", err
	}
	return channelPermissionsForTx(ctx, tx, access, userID, spaceID, conversationID)
}

func requireSpaceChannelPermissionTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID, permission string) error {
	if conversationID == "" {
		return requireSpacePermissionTx(ctx, tx, userID, spaceID, permission)
	}
	permissions, _, err := channelPermissionsTx(ctx, tx, userID, spaceID, conversationID)
	if err != nil {
		return err
	}
	if !permissions.Has(PermissionMessagesRead) {
		return ErrSpaceNotFound
	}
	if !permissions.Has(permission) {
		return ErrSpaceForbidden
	}
	return nil
}

func requireSpaceChannelWriteTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) error {
	if conversationID == "" {
		return requireSpaceMessageWriteTx(ctx, tx, userID, spaceID)
	}
	return requireSpaceChannelPermissionTx(ctx, tx, userID, spaceID, conversationID, PermissionMessagesWrite)
}

func messagePermissionsTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID string) (spaces.PermissionSet, error) {
	if conversationID == "" {
		access, err := memberAccessTx(ctx, tx, spaceID, userID)
		if err != nil {
			return nil, err
		}
		return access.base, nil
	}
	permissions, _, err := channelPermissionsTx(ctx, tx, userID, spaceID, conversationID)
	if err != nil {
		return nil, err
	}
	if !permissions.Has(PermissionMessagesRead) {
		return nil, ErrSpaceNotFound
	}
	return permissions, nil
}

func spaceChannelOverwritesTx(ctx context.Context, tx *sql.Tx, spaceID string, spaceWideOnly bool) (map[string][]spaces.Overwrite, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.id,COALESCE(o.role_id::text,''),COALESCE(o.allow,'{}'::text[]),COALESCE(o.deny,'{}'::text[])
		FROM space_conversations c LEFT JOIN space_conversation_overwrites o ON o.conversation_id=c.id
		WHERE c.space_id=$1 AND c.kind='standard' AND (NOT $2 OR c.visible_to_space)`, spaceID, spaceWideOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]spaces.Overwrite{}
	for rows.Next() {
		var conversationID string
		var overwrite spaces.Overwrite
		if err := rows.Scan(&conversationID, &overwrite.RoleID, pq.Array(&overwrite.Allow), pq.Array(&overwrite.Deny)); err != nil {
			return nil, err
		}
		if _, ok := out[conversationID]; !ok {
			out[conversationID] = []spaces.Overwrite{}
		}
		if overwrite.RoleID != "" {
			out[conversationID] = append(out[conversationID], overwrite)
		}
	}
	return out, rows.Err()
}

func conversationReadableSQL(conversation, user string) string {
	return "public.misty_can_read_space_conversation(" + conversation + "," + user + ")"
}
