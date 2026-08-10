package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// IsSpaceMember reports whether userID is an active member of spaceID. It
// exists for callers outside the normal request/response flow (the realtime
// WebSocket handler, checking a client-claimed "viewing" space) that need a
// lightweight membership check without an otherwise-unused mutation.
func (db *Database) IsSpaceMember(ctx context.Context, userID, spaceID string) (bool, error) {
	var isMember bool
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, memberErr := requireSpaceMemberTx(ctx, tx, spaceID, userID)
		if errors.Is(memberErr, ErrSpaceForbidden) {
			isMember = false
			return nil
		}
		if memberErr != nil {
			return memberErr
		}
		isMember = true
		return nil
	})
	return isMember, err
}

func requireSpaceOwnerTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
	if err != nil {
		return err
	}
	if role != "owner" {
		return ErrSpaceForbidden
	}
	return nil
}

func recordSpaceEventTx(ctx context.Context, tx *sql.Tx, spaceID, userID, eventType, entityID string, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO space_events(space_id,event_type,actor_user_id,entity_id,payload)
		VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5) RETURNING id`, spaceID, eventType, userID, entityID, raw).Scan(&id)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `SELECT pg_notify('misty_space_events',$1)`, fmt.Sprint(id))
	return id, err
}

func notifySpaceControlTx(ctx context.Context, tx *sql.Tx, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `SELECT pg_notify('misty_space_control',$1)`, string(raw))
	return err
}

func (db *Database) CreateSpace(ctx context.Context, userID, name string) (*Space, error) {
	result, err := db.CreateSpaceWithTemplate(ctx, userID, name, "blank", nil)
	if err != nil {
		return nil, err
	}
	return &result.Space, nil
}

func (db *Database) ListSpaces(ctx context.Context, userID string) ([]Space, error) {
	spaces := []Space{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.security_domain_id,s.owner_user_id,s.name,s.kind,m.role,
			(SELECT count(*) FROM space_members sm WHERE sm.space_id=s.id),
			(SELECT count(*) FROM space_invitations si WHERE si.space_id=s.id AND si.expires_at>NOW()
			  AND si.revoked_at IS NULL AND si.consumed_at IS NULL),
			(EXISTS(SELECT 1 FROM space_members sm WHERE sm.space_id=s.id AND sm.role='member') OR
			 EXISTS(SELECT 1 FROM space_invitations si WHERE si.space_id=s.id AND si.expires_at>NOW() AND si.revoked_at IS NULL AND si.consumed_at IS NULL)),
			s.created_at,s.updated_at
			FROM spaces s JOIN space_members m ON m.space_id=s.id
			WHERE m.user_id=$1 AND s.lifecycle_state='active' ORDER BY s.updated_at DESC`, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var space Space
			if err := rows.Scan(&space.ID, &space.SecurityDomainID, &space.OwnerUserID, &space.Name, &space.Kind, &space.Role, &space.MemberCount, &space.PendingCount, &space.IsShared, &space.CreatedAt, &space.UpdatedAt); err != nil {
				rows.Close()
				return err
			}
			spaces = append(spaces, space)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for index := range spaces {
			if err := populateSpacePermissionsTx(ctx, tx, userID, &spaces[index]); err != nil {
				return err
			}
		}
		return nil
	})
	return spaces, err
}

func (db *Database) SpaceByID(ctx context.Context, userID, spaceID string) (*Space, error) {
	out := &Space{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT s.id,s.security_domain_id,s.owner_user_id,s.name,s.kind,m.role,
			(SELECT count(*) FROM space_members sm WHERE sm.space_id=s.id),
			(SELECT count(*) FROM space_invitations si WHERE si.space_id=s.id AND si.expires_at>NOW()
			  AND si.revoked_at IS NULL AND si.consumed_at IS NULL),
			(EXISTS(SELECT 1 FROM space_members sm WHERE sm.space_id=s.id AND sm.role='member') OR
			 EXISTS(SELECT 1 FROM space_invitations si WHERE si.space_id=s.id AND si.expires_at>NOW() AND si.revoked_at IS NULL AND si.consumed_at IS NULL)),
			s.created_at,s.updated_at
			FROM spaces s JOIN space_members m ON m.space_id=s.id
			WHERE s.id=$1 AND m.user_id=$2 AND s.lifecycle_state='active'`, spaceID, userID).Scan(&out.ID, &out.SecurityDomainID, &out.OwnerUserID, &out.Name, &out.Kind, &out.Role, &out.MemberCount, &out.PendingCount, &out.IsShared, &out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		return populateSpacePermissionsTx(ctx, tx, userID, out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	return out, err
}

func populateSpacePermissionsTx(ctx context.Context, tx *sql.Tx, userID string, space *Space) error {
	space.Permissions = make(map[string]bool, len(configurableSpacePermissions)+1)
	for _, permission := range configurableSpacePermissions {
		allowed, err := hasSpacePermissionTx(ctx, tx, userID, space.ID, permission)
		if err != nil {
			return err
		}
		space.Permissions[permission] = allowed
	}
	applySpacePermissionDependencies(space.Permissions)
	canDelete, err := canDeleteSpaceTx(ctx, tx, userID, space.ID, space.Role)
	if err != nil {
		return err
	}
	space.Permissions[PermissionSpaceDelete] = canDelete
	return nil
}

func canDeleteSpaceTx(ctx context.Context, tx *sql.Tx, userID, spaceID, role string) (bool, error) {
	if role != "owner" {
		return false, nil
	}
	var isDefaultMisty, isOperator bool
	err := tx.QueryRowContext(ctx, `SELECT
		(s.id LIKE 'space_default_%' AND s.security_domain_id LIKE 'sd_default_%'),
		EXISTS(SELECT 1 FROM misty_space_operators o WHERE o.user_id=$2)
		FROM spaces s WHERE s.id=$1`, spaceID, userID).Scan(&isDefaultMisty, &isOperator)
	if err != nil {
		return false, err
	}
	return !isDefaultMisty || isOperator, nil
}

// EnsureDefaultSpace provisions the user's ordinary personal Space when needed.
func (db *Database) EnsureDefaultSpace(ctx context.Context, userID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT misty_ensure_default_space($1)`, userID).Scan(&spaceID); err != nil {
			return err
		}
		if !spaceID.Valid || spaceID.String == "" {
			return errors.New("default Misty Space could not be provisioned")
		}
		return nil
	})
}

func (db *Database) RenameSpace(ctx context.Context, userID, spaceID, name string) (*Space, error) {
	name, err := normalizeSpaceName(name)
	if err != nil {
		return nil, err
	}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE spaces SET name=$1,updated_at=NOW() WHERE id=$2`, name, spaceID); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "space.updated", spaceID, map[string]any{"name": name})
		return err
	})
	if err != nil {
		return nil, err
	}
	return db.SpaceByID(ctx, userID, spaceID)
}

func (db *Database) DeleteSpace(ctx context.Context, userID, spaceID, confirmation string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var name string
		var isDefaultMisty, isOperator bool
		if err := tx.QueryRowContext(ctx, `SELECT s.name,
			(s.id LIKE 'space_default_%' AND s.security_domain_id LIKE 'sd_default_%'),
			EXISTS(SELECT 1 FROM misty_space_operators o WHERE o.user_id=$2)
			FROM spaces s WHERE s.id=$1 FOR UPDATE`, spaceID, userID).Scan(&name, &isDefaultMisty, &isOperator); err != nil {
			return err
		}
		if isDefaultMisty && !isOperator {
			return ErrSpaceForbidden
		}
		if confirmation != name {
			return ErrSpaceInvalid
		}
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM space_members WHERE space_id=$1`, spaceID)
		if err != nil {
			return err
		}
		memberIDs := []string{}
		for rows.Next() {
			var memberID string
			if err := rows.Scan(&memberID); err != nil {
				rows.Close()
				return err
			}
			memberIDs = append(memberIDs, memberID)
		}
		rows.Close()
		if err := notifySpaceControlTx(ctx, tx, map[string]any{"type": "space.deleted", "space_id": spaceID, "user_ids": memberIDs}); err != nil {
			return err
		}
		if err := revokeDrawingAccessForSpaceTx(ctx, tx, spaceID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "space.deletion_requested", spaceID, map[string]any{"recover_days": 30}); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE spaces SET lifecycle_state='pending_deletion',deletion_requested_at=NOW(),permanent_delete_after=NOW()+INTERVAL '30 days',updated_at=NOW() WHERE id=$1`, spaceID)
		return err
	})
}

func (db *Database) SpaceMembers(ctx context.Context, userID, spaceID string) ([]SpaceMember, error) {
	members := []SpaceMember{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT m.space_id,m.user_id,u.name,u.email,m.role,m.joined_at,m.read_message_seq
			FROM space_members m JOIN users u ON u.id=m.user_id WHERE m.space_id=$1 ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END,u.name`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m SpaceMember
			if err := rows.Scan(&m.SpaceID, &m.UserID, &m.Name, &m.Email, &m.Role, &m.JoinedAt, &m.ReadSeq); err != nil {
				return err
			}
			members = append(members, m)
		}
		return rows.Err()
	})
	return members, err
}
