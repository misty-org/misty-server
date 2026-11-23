package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/misty-org/misty-server/internal/spaces"
)

type SpaceRole struct {
	ID          string    `json:"id"`
	SpaceID     string    `json:"space_id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Position    int       `json:"position"`
	Hoist       bool      `json:"hoist"`
	Mentionable bool      `json:"mentionable"`
	Permissions []string  `json:"permissions"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type SpaceRoleInput struct {
	Name        *string   `json:"name"`
	Color       *string   `json:"color"`
	Hoist       *bool     `json:"hoist"`
	Mentionable *bool     `json:"mentionable"`
	Permissions *[]string `json:"permissions"`
}

const spaceRoleColumns = `id,space_id,name,color,position,hoist,mentionable,permissions,is_default,created_at,updated_at`

func scanSpaceRole(scanner interface{ Scan(...any) error }, role *SpaceRole) error {
	if err := scanner.Scan(&role.ID, &role.SpaceID, &role.Name, &role.Color, &role.Position, &role.Hoist,
		&role.Mentionable, pq.Array(&role.Permissions), &role.IsDefault, &role.CreatedAt, &role.UpdatedAt); err != nil {
		return err
	}
	if role.Permissions == nil {
		role.Permissions = []string{}
	}
	return nil
}

func spaceRolesTx(ctx context.Context, tx *sql.Tx, spaceID string) ([]SpaceRole, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+spaceRoleColumns+` FROM space_roles WHERE space_id=$1 ORDER BY position DESC`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SpaceRole{}
	for rows.Next() {
		var role SpaceRole
		if err := scanSpaceRole(rows, &role); err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, rows.Err()
}

func spaceRoleForUpdateTx(ctx context.Context, tx *sql.Tx, spaceID, roleID string) (*SpaceRole, error) {
	role := &SpaceRole{}
	err := scanSpaceRole(tx.QueryRowContext(ctx, `SELECT `+spaceRoleColumns+` FROM space_roles
		WHERE space_id=$1 AND id::text=$2 FOR UPDATE`, spaceID, roleID), role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return role, err
}

func requireRolesManagerTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) (*memberAccess, error) {
	if err := lockSpaceRolesTx(ctx, tx, spaceID); err != nil {
		return nil, err
	}
	access, err := memberAccessTx(ctx, tx, spaceID, userID)
	if err != nil {
		return nil, err
	}
	if !access.base.Has(PermissionRolesManage) {
		return nil, ErrSpaceForbidden
	}
	return access, nil
}

func (db *Database) SpaceRoles(ctx context.Context, userID, spaceID string) ([]SpaceRole, error) {
	var out []SpaceRole
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := ensureEveryoneRoleTx(ctx, tx, spaceID); err != nil {
			return err
		}
		var err error
		out, err = spaceRolesTx(ctx, tx, spaceID)
		return err
	})
	return out, err
}

func (db *Database) CreateSpaceRole(ctx context.Context, userID, spaceID string, input SpaceRoleInput) (*SpaceRole, error) {
	if input.Name == nil {
		return nil, ErrSpaceInvalid
	}
	name, ok := spaces.NormalizeRoleName(*input.Name)
	color := ""
	if input.Color != nil {
		var colorOK bool
		color, colorOK = spaces.NormalizeRoleColor(*input.Color)
		ok = ok && colorOK
	}
	permissions := []string{}
	if input.Permissions != nil {
		var permissionsOK bool
		permissions, permissionsOK = spaces.NormalizeRolePermissions(*input.Permissions)
		ok = ok && permissionsOK
	}
	if !ok {
		return nil, ErrSpaceInvalid
	}
	role := &SpaceRole{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := requireRolesManagerTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		if !access.owner && !spaces.HoldsChanges(access.base, nil, permissions) {
			return ErrSpaceForbidden
		}
		if err := ensureEveryoneRoleTx(ctx, tx, spaceID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_roles WHERE space_id=$1`, spaceID).Scan(&count); err != nil {
			return err
		}
		if count >= spaces.MaxRolesPerSpace {
			return ErrSpaceInvalid
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_roles SET position=position+1,updated_at=NOW()
			WHERE space_id=$1 AND NOT is_default`, spaceID); err != nil {
			return err
		}
		if err := scanSpaceRole(tx.QueryRowContext(ctx, `INSERT INTO space_roles(space_id,name,color,position,hoist,mentionable,permissions)
			VALUES($1,$2,$3,1,$4,$5,$6) RETURNING `+spaceRoleColumns, spaceID, name, color,
			input.Hoist != nil && *input.Hoist, input.Mentionable != nil && *input.Mentionable, pq.Array(permissions)), role); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "role.created", role.ID, role)
		return err
	})
	if err != nil {
		return nil, err
	}
	return role, nil
}

func (db *Database) UpdateSpaceRole(ctx context.Context, userID, spaceID, roleID string, input SpaceRoleInput) (*SpaceRole, error) {
	role := &SpaceRole{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := requireRolesManagerTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		if role, err = spaceRoleForUpdateTx(ctx, tx, spaceID, roleID); err != nil {
			return err
		}
		if !spaces.CanManageRole(access.owner, access.highest, role.Position) {
			return ErrSpaceForbidden
		}
		previous := role.Permissions
		if err := applySpaceRoleInput(role, input); err != nil {
			return err
		}
		if !access.owner && !spaces.HoldsChanges(access.base, previous, role.Permissions) {
			return ErrSpaceForbidden
		}
		readers, err := channelReadersTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if err := scanSpaceRole(tx.QueryRowContext(ctx, `UPDATE space_roles
			SET name=$2,color=$3,hoist=$4,mentionable=$5,permissions=$6,updated_at=NOW()
			WHERE id=$1 RETURNING `+spaceRoleColumns, role.ID, role.Name, role.Color, role.Hoist,
			role.Mentionable, pq.Array(role.Permissions)), role); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "role.updated", role.ID, role); err != nil {
			return err
		}
		return announceChannelAccessTx(ctx, tx, spaceID, userID, readers, "")
	})
	if err != nil {
		return nil, err
	}
	return role, nil
}

func applySpaceRoleInput(role *SpaceRole, input SpaceRoleInput) error {
	if input.Name != nil {
		name, ok := spaces.NormalizeRoleName(*input.Name)
		if role.IsDefault {
			name, ok = role.Name, strings.TrimSpace(*input.Name) == role.Name
		}
		if !ok {
			return ErrSpaceInvalid
		}
		role.Name = name
	}
	if input.Color != nil {
		color, ok := spaces.NormalizeRoleColor(*input.Color)
		if !ok || role.IsDefault && color != role.Color {
			return ErrSpaceInvalid
		}
		role.Color = color
	}
	if input.Hoist != nil {
		if role.IsDefault && *input.Hoist != role.Hoist {
			return ErrSpaceInvalid
		}
		role.Hoist = *input.Hoist
	}
	if input.Mentionable != nil {
		if role.IsDefault && *input.Mentionable != role.Mentionable {
			return ErrSpaceInvalid
		}
		role.Mentionable = *input.Mentionable
	}
	if input.Permissions != nil {
		permissions, ok := spaces.NormalizeRolePermissions(*input.Permissions)
		if !ok {
			return ErrSpaceInvalid
		}
		role.Permissions = permissions
	}
	return nil
}

func (db *Database) DeleteSpaceRole(ctx context.Context, userID, spaceID, roleID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := requireRolesManagerTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		role, err := spaceRoleForUpdateTx(ctx, tx, spaceID, roleID)
		if err != nil {
			return err
		}
		if role.IsDefault {
			return ErrSpaceInvalid
		}
		if !spaces.CanManageRole(access.owner, access.highest, role.Position) {
			return ErrSpaceForbidden
		}
		readers, err := channelReadersTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_roles WHERE id=$1`, role.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_roles SET position=position-1,updated_at=NOW()
			WHERE space_id=$1 AND position>$2 AND NOT is_default`, spaceID, role.Position); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "role.deleted", role.ID, map[string]any{"id": role.ID}); err != nil {
			return err
		}
		return announceChannelAccessTx(ctx, tx, spaceID, userID, readers, "")
	})
}

func validateRoleMentionsTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, content, kept []MessageSpan) error {
	already := map[string]bool{}
	for _, span := range kept {
		if span.RoleID != "" {
			already[span.RoleID] = true
		}
	}
	var access *memberAccess
	for _, span := range content {
		if span.RoleID == "" || already[span.RoleID] {
			continue
		}
		var isDefault, mentionable bool
		err := tx.QueryRowContext(ctx, `SELECT is_default,mentionable FROM space_roles WHERE space_id=$1 AND id::text=$2`,
			spaceID, span.RoleID).Scan(&isDefault, &mentionable)
		if errors.Is(err, sql.ErrNoRows) || err == nil && isDefault {
			return ErrSpaceInvalid
		}
		if err != nil {
			return err
		}
		if mentionable {
			continue
		}
		if access == nil {
			if access, err = memberAccessTx(ctx, tx, spaceID, userID); err != nil {
				return err
			}
		}
		if !access.base.Has(PermissionAdministrator) {
			return ErrSpaceForbidden
		}
	}
	return nil
}
