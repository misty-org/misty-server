package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (db *Database) SpaceMemberAvatarReference(ctx context.Context, requestingUserID, spaceID, memberID string) (UserAvatarReference, error) {
	var avatar UserAvatarReference
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, requestingUserID); err != nil {
			return err
		}
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, memberID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT avatar_version,COALESCE(avatar_object_key,'avatars/'||id) FROM users WHERE id=$1`, memberID).Scan(&avatar.Version, &avatar.ObjectKey)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return avatar, err
}

func (db *Database) InviteToSpaceWithToken(
	ctx context.Context,
	ownerID, spaceID, email, tokenHash string,
) (*SpaceInvitation, error) {
	return db.inviteToSpace(ctx, ownerID, spaceID, normalizeEmail(email), "", tokenHash)
}

func (db *Database) InviteFriendToSpaceWithToken(
	ctx context.Context,
	ownerID, spaceID, friendID, tokenHash string,
) (*SpaceInvitation, error) {
	friendID = strings.TrimSpace(friendID)
	if friendID == "" {
		return nil, ErrSpaceInvalid
	}
	return db.inviteToSpace(ctx, ownerID, spaceID, "", friendID, tokenHash)
}

func (db *Database) inviteToSpace(
	ctx context.Context,
	ownerID, spaceID, email, friendID, tokenHash string,
) (*SpaceInvitation, error) {
	byAccount := friendID != ""
	tokenHash = strings.TrimSpace(tokenHash)
	if (email == "" && !byAccount) || tokenHash == "" {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceInvitation{
		ID: "invite_" + uuid.NewString(), SpaceID: spaceID, InvitedByUserID: ownerID,
		InvitedEmail: email, ByAccount: byAccount,
		ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour),
	}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceAuthorityTx(ctx, tx, spaceID, ownerID, PermissionSpaceInvite); err != nil {
			return err
		}
		if err := requireShareableSpaceTx(ctx, tx, spaceID); err != nil {
			return err
		}
		var spaceOwnerID string
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM spaces WHERE id=$1`, spaceID).Scan(&spaceOwnerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:owner:"+spaceOwnerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:people:"+spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_invitations SET revoked_at=COALESCE(revoked_at,NOW())
			WHERE expires_at<=NOW() AND consumed_at IS NULL`); err != nil {
			return err
		}
		if byAccount {
			friends, err := areFriendsTx(ctx, tx, ownerID, friendID)
			if err != nil {
				return err
			}
			blocked, err := blockedEitherWayTx(ctx, tx, ownerID, friendID)
			if err != nil {
				return err
			}
			if !friends || blocked {
				return ErrSpaceForbidden
			}
			var found string
			err = tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id=$1 AND lifecycle_state='active'`, friendID).Scan(&found)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrSpaceInvalid
			}
			if err != nil {
				return err
			}
			if email = normalizeEmail(found); email == "" {
				return ErrSpaceInvalid
			}
		}
		err := tx.QueryRowContext(ctx, `SELECT id,username,email FROM users WHERE lower(email)=$1`, email).
			Scan(&out.InvitedUserID, &out.InvitedUserName, &out.InvitedEmail)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var already bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM space_members m JOIN users u ON u.id=m.user_id
			WHERE m.space_id=$1 AND lower(u.email)=$2
		)`, spaceID, email).Scan(&already); err != nil {
			return err
		}
		if already {
			return ErrSpaceConflict
		}
		if err := requireSpaceSharingRoomTx(ctx, tx, spaceOwnerID, spaceID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id=$1`, ownerID).Scan(&out.InviterName); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT name FROM spaces WHERE id=$1`, spaceID).Scan(&out.SpaceName); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_invitations SET revoked_at=NOW()
			WHERE space_id=$1 AND lower(invited_email)=$2 AND revoked_at IS NULL AND consumed_at IS NULL`,
			spaceID, email); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_invitations
			(id,space_id,invited_user_id,invited_email,invited_by_user_id,token_hash,delivery_status,expires_at,by_account)
			VALUES($1,$2,NULLIF($3,''),$4,$5,$6,'pending',$7,$8)
			RETURNING created_at`, out.ID, spaceID, out.InvitedUserID, email, ownerID, tokenHash, out.ExpiresAt, byAccount).
			Scan(&out.CreatedAt); err != nil {
			return err
		}
		invitedUserID := ""
		if out.InvitedUserID != nil {
			invitedUserID = *out.InvitedUserID
		}
		if _, err = recordSpaceEventTx(ctx, tx, spaceID, ownerID, "member.invited", invitedUserID, map[string]any{"invite_id": out.ID}); err != nil {
			return err
		}
		return notifyInviteeTx(ctx, tx, spaceID, invitedUserID)
	})
	if byAccount {
		out.InvitedEmail = ""
	}
	return out, err
}

func notifyInviteeTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	if userID == "" {
		return nil
	}
	return notifySpaceControlTx(ctx, tx, map[string]any{
		"type": "invitation.changed", "space_id": spaceID, "user_ids": []string{userID}, "keep_connection": true,
	})
}

func (db *Database) IncomingSpaceInvites(ctx context.Context, userID string) ([]SpaceInvitation, error) {
	items := []SpaceInvitation{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE space_invitations SET revoked_at=COALESCE(revoked_at,NOW())
			WHERE expires_at<=NOW() AND consumed_at IS NULL`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.space_id,s.name,i.invited_user_id,
			invited.username,i.invited_email,i.invited_by_user_id,inviter.username,
			i.expires_at,i.created_at
			FROM space_invitations i
			JOIN spaces s ON s.id=i.space_id
			JOIN users current_invitee ON current_invitee.id=$1
			JOIN users inviter ON inviter.id=i.invited_by_user_id
			LEFT JOIN users invited ON invited.id=i.invited_user_id
			WHERE (i.invited_user_id=$1 OR lower(i.invited_email)=lower(current_invitee.email))
			  AND i.revoked_at IS NULL AND i.consumed_at IS NULL AND i.expires_at>NOW()
			ORDER BY i.created_at DESC`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceInvitation
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.SpaceName, &item.InvitedUserID,
				&item.InvitedUserName, &item.InvitedEmail, &item.InvitedByUserID, &item.InviterName,
				&item.ExpiresAt, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) PendingSpaceInvitations(
	ctx context.Context,
	ownerID, spaceID string,
) ([]SpaceInvitation, error) {
	items := []SpaceInvitation{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceAuthorityTx(ctx, tx, spaceID, ownerID, PermissionSpaceInvite); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.space_id,s.name,
			i.invited_user_id,invited.username,
			CASE WHEN i.by_account THEN '' ELSE i.invited_email END,i.by_account,
			i.invited_by_user_id,inviter.username,i.expires_at,i.created_at
			FROM space_invitations i
			JOIN spaces s ON s.id=i.space_id
			JOIN users inviter ON inviter.id=i.invited_by_user_id
			LEFT JOIN users invited ON invited.id=i.invited_user_id
			WHERE i.space_id=$1 AND i.revoked_at IS NULL AND i.consumed_at IS NULL
			  AND i.expires_at>NOW()
			ORDER BY i.created_at DESC`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item SpaceInvitation
			if err := rows.Scan(&item.ID, &item.SpaceID, &item.SpaceName, &item.InvitedUserID,
				&item.InvitedUserName, &item.InvitedEmail, &item.ByAccount, &item.InvitedByUserID,
				&item.InviterName, &item.ExpiresAt, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) RefreshSpaceInvitation(
	ctx context.Context,
	ownerID, spaceID, inviteID, tokenHash string,
) (*SpaceInvitation, error) {
	out := &SpaceInvitation{}
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceAuthorityTx(ctx, tx, spaceID, ownerID, PermissionSpaceInvite); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `UPDATE space_invitations i
			SET token_hash=$1,expires_at=$2,last_sent_at=NULL
			FROM spaces s,users inviter
			WHERE i.id=$3 AND i.space_id=$4 AND i.space_id=s.id
			  AND inviter.id=i.invited_by_user_id
			  AND i.revoked_at IS NULL AND i.consumed_at IS NULL
			RETURNING i.id,i.space_id,s.name,i.invited_user_id,
			  CASE WHEN i.by_account THEN '' ELSE i.invited_email END,i.by_account,
			  i.invited_by_user_id,inviter.username,
			  i.expires_at,i.created_at`, tokenHash, expiresAt, inviteID, spaceID).
			Scan(&out.ID, &out.SpaceID, &out.SpaceName, &out.InvitedUserID, &out.InvitedEmail,
				&out.ByAccount, &out.InvitedByUserID, &out.InviterName, &out.ExpiresAt,
				&out.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceInviteNotFound
		}
		return err
	})
	return out, err
}

func (db *Database) RevokeSpaceInvitation(
	ctx context.Context,
	ownerID, spaceID, inviteID string,
) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceAuthorityTx(ctx, tx, spaceID, ownerID, PermissionSpaceInvite); err != nil {
			return err
		}
		var invitedUserID string
		err := tx.QueryRowContext(ctx, `UPDATE space_invitations i SET revoked_at=NOW()
			WHERE i.id=$1 AND i.space_id=$2 AND i.revoked_at IS NULL AND i.consumed_at IS NULL
			RETURNING COALESCE(i.invited_user_id,
			  (SELECT u.id FROM users u WHERE lower(u.email)=lower(i.invited_email)), '')`,
			inviteID, spaceID).Scan(&invitedUserID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceInviteNotFound
		}
		if err != nil {
			return err
		}
		return notifyInviteeTx(ctx, tx, spaceID, invitedUserID)
	})
}
