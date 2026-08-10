package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SpaceMemberAvatarMeta permission-checks the requester and target within a Space
// and returns the member's avatar version (0 when unset). The bytes themselves are
// streamed from the object store (R2).
func (db *Database) SpaceMemberAvatarMeta(ctx context.Context, requestingUserID, spaceID, memberID string) (int64, error) {
	var version int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, requestingUserID); err != nil {
			return err
		}
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, memberID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT avatar_version FROM users WHERE id=$1`, memberID).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return version, err
}

func (db *Database) InviteToSpace(ctx context.Context, ownerID, spaceID, email string) (*SpaceInvitation, error) {
	return db.InviteToSpaceWithToken(
		ctx, ownerID, spaceID, email, "legacy-"+strings.ReplaceAll(uuid.NewString(), "-", ""),
	)
}

func (db *Database) InviteToSpaceWithToken(
	ctx context.Context,
	ownerID, spaceID, email, tokenHash string,
) (*SpaceInvitation, error) {
	email = normalizeEmail(email)
	tokenHash = strings.TrimSpace(tokenHash)
	if email == "" || tokenHash == "" {
		return nil, ErrSpaceInvalid
	}
	out := &SpaceInvitation{
		ID: "invite_" + uuid.NewString(), SpaceID: spaceID, InvitedByUserID: ownerID,
		InvitedEmail: email, DeliveryStatus: "pending",
		ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour),
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:owner:"+ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:people:"+spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_invitations SET revoked_at=COALESCE(revoked_at,NOW())
			WHERE expires_at<=NOW() AND consumed_at IS NULL`); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT id,name,email FROM users WHERE lower(email)=$1`, email).
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
		if err := tx.QueryRowContext(ctx, `SELECT name FROM users WHERE id=$1`, ownerID).Scan(&out.InviterName); err != nil {
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
			(id,space_id,invited_user_id,invited_email,invited_by_user_id,token_hash,delivery_status,expires_at)
			VALUES($1,$2,NULLIF($3,''),$4,$5,$6,'pending',$7)
			RETURNING created_at`, out.ID, spaceID, out.InvitedUserID, email, ownerID, tokenHash, out.ExpiresAt).
			Scan(&out.CreatedAt); err != nil {
			return err
		}
		invitedUserID := ""
		if out.InvitedUserID != nil {
			invitedUserID = *out.InvitedUserID
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, ownerID, "member.invited", invitedUserID, map[string]any{"invite_id": out.ID})
		return err
	})
	return out, err
}

func (db *Database) IncomingSpaceInvites(ctx context.Context, userID string) ([]SpaceInvitation, error) {
	items := []SpaceInvitation{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE space_invitations SET revoked_at=COALESCE(revoked_at,NOW())
			WHERE expires_at<=NOW() AND consumed_at IS NULL`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.space_id,s.name,i.invited_user_id,
			invited.name,i.invited_email,i.invited_by_user_id,inviter.name,
			i.delivery_status,i.expires_at,i.created_at
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
				&item.DeliveryStatus, &item.ExpiresAt, &item.CreatedAt); err != nil {
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
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, ownerID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.space_id,s.name,
			i.invited_user_id,invited.name,i.invited_email,
			i.invited_by_user_id,inviter.name,i.delivery_status,i.expires_at,i.created_at
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
				&item.InvitedUserName, &item.InvitedEmail, &item.InvitedByUserID, &item.InviterName,
				&item.DeliveryStatus, &item.ExpiresAt, &item.CreatedAt); err != nil {
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
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, ownerID); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `UPDATE space_invitations i
			SET token_hash=$1,delivery_status='pending',expires_at=$2,last_sent_at=NULL
			FROM spaces s,users inviter
			WHERE i.id=$3 AND i.space_id=$4 AND i.space_id=s.id
			  AND inviter.id=i.invited_by_user_id
			  AND i.revoked_at IS NULL AND i.consumed_at IS NULL
			RETURNING i.id,i.space_id,s.name,i.invited_user_id,
			  i.invited_email,i.invited_by_user_id,inviter.name,i.delivery_status,
			  i.expires_at,i.created_at`, tokenHash, expiresAt, inviteID, spaceID).
			Scan(&out.ID, &out.SpaceID, &out.SpaceName, &out.InvitedUserID, &out.InvitedEmail,
				&out.InvitedByUserID, &out.InviterName, &out.DeliveryStatus, &out.ExpiresAt,
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
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, ownerID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE space_invitations SET revoked_at=NOW()
			WHERE id=$1 AND space_id=$2 AND revoked_at IS NULL AND consumed_at IS NULL`,
			inviteID, spaceID)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return ErrSpaceInviteNotFound
		}
		return nil
	})
}
