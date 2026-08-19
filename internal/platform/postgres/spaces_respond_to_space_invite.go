package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (db *Database) RespondToSpaceInvite(ctx context.Context, userID, inviteID string, accept bool) (*Space, error) {
	var spaceID string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var expires time.Time
		var invitedEmail, userEmail string
		if err := tx.QueryRowContext(ctx, `SELECT i.space_id,i.expires_at,i.invited_email,u.email
			FROM space_invitations i JOIN users u ON u.id=$2
			WHERE i.id=$1 AND i.revoked_at IS NULL AND i.consumed_at IS NULL
			FOR UPDATE OF i`, inviteID, userID).Scan(&spaceID, &expires, &invitedEmail, &userEmail); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceInviteNotFound
		} else if err != nil {
			return err
		}
		if normalizeEmail(invitedEmail) != normalizeEmail(userEmail) {
			return ErrSpaceInviteNotFound
		}
		if time.Now().After(expires) {
			_, _ = tx.ExecContext(ctx, `DELETE FROM space_invitations WHERE id=$1`, inviteID)
			return ErrSpaceInviteExpired
		}
		if !accept {
			_, err := tx.ExecContext(ctx, `UPDATE space_invitations SET revoked_at=NOW() WHERE id=$1`, inviteID)
			return err
		}
		if err := addSpaceMembershipTx(ctx, tx, spaceID, userID, "member"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_invitations
			SET invited_user_id=$1,consumed_at=NOW() WHERE id=$2`, userID, inviteID); err != nil {
			return err
		}
		// Rejoining the same Space inside the retention window restores the
		// notes archived when this user left.
		if err := handleNoteMembershipRestoreTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "member.joined", userID, map[string]any{})
		return err
	})
	if err != nil || !accept {
		return nil, err
	}
	return db.SpaceByID(ctx, userID, spaceID)
}

func (db *Database) SpaceInvitationPreview(
	ctx context.Context,
	tokenHash string,
) (*SpaceInvitationPreview, error) {
	out := &SpaceInvitationPreview{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT s.name,u.name,i.invited_email,i.expires_at
			FROM space_invitations i
			JOIN spaces s ON s.id=i.space_id
			JOIN users u ON u.id=i.invited_by_user_id
			WHERE i.token_hash=$1 AND i.revoked_at IS NULL AND i.consumed_at IS NULL
			  AND i.expires_at>NOW()`, tokenHash).
			Scan(&out.SpaceName, &out.InviterName, &out.InvitedEmail, &out.ExpiresAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceInviteNotFound
	}
	return out, err
}

func (db *Database) RespondToSpaceInviteToken(
	ctx context.Context,
	userID, tokenHash string,
	accept bool,
) (*Space, error) {
	var inviteID string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id FROM space_invitations
			WHERE token_hash=$1 AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at>NOW()`,
			tokenHash).Scan(&inviteID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	return db.RespondToSpaceInvite(ctx, userID, inviteID, accept)
}

func (db *Database) SetSpaceInvitationDelivery(
	ctx context.Context,
	inviteID, status string,
) error {
	if status != "sent" && status != "failed" {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_invitations
			SET delivery_status=$1,last_sent_at=NOW() WHERE id=$2`, status, inviteID)
		return err
	})
}

func (db *Database) RemoveSpaceMember(ctx context.Context, ownerID, spaceID, memberID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, ownerID); err != nil {
			return err
		}
		if ownerID == memberID {
			return ErrSpaceInvalid
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_conversation_members cm USING space_conversations c WHERE cm.conversation_id=c.id AND c.space_id=$1 AND cm.user_id=$2`, spaceID, memberID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=$1 AND user_id=$2 AND role='member'`, spaceID, memberID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return ErrSpaceNotFound
		}
		if err := cancelCreatorSpaceRunsTx(ctx, tx, memberID, spaceID, "space_membership_revoked"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_agents SET schedules_enabled=FALSE WHERE space_id=$1 AND creator_user_id=$2`, spaceID, memberID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_workflows SET schedules_enabled=FALSE WHERE space_id=$1 AND creator_user_id=$2`, spaceID, memberID); err != nil {
			return err
		}
		// Same transaction as the membership delete: a note must never remain
		// reachable by someone who is no longer a member, not even briefly.
		if err := handleNoteMembershipLossTx(ctx, tx, spaceID, memberID); err != nil {
			return err
		}
		if err := revokeDrawingAccessForSpaceTx(ctx, tx, spaceID); err != nil {
			return err
		}
		if _, err = recordSpaceEventTx(ctx, tx, spaceID, ownerID, "member.removed", memberID, map[string]any{}); err != nil {
			return err
		}
		return notifySpaceControlTx(ctx, tx, map[string]any{"type": "member.removed", "space_id": spaceID, "user_ids": []string{memberID}})
	})
}

func (db *Database) LeaveSpace(ctx context.Context, userID, spaceID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if misty, err := isMistySpaceTx(ctx, tx, spaceID); err != nil {
			return err
		} else if misty {
			return ErrSpaceForbidden
		}
		role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
		if err != nil {
			return err
		}
		if role == "owner" {
			return ErrSpaceInvalid
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_conversation_members cm USING space_conversations c WHERE cm.conversation_id=c.id AND c.space_id=$1 AND cm.user_id=$2`, spaceID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_members WHERE space_id=$1 AND user_id=$2`, spaceID, userID); err != nil {
			return err
		}
		if err := cancelCreatorSpaceRunsTx(ctx, tx, userID, spaceID, "space_membership_revoked"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_agents SET schedules_enabled=FALSE WHERE space_id=$1 AND creator_user_id=$2`, spaceID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_workflows SET schedules_enabled=FALSE WHERE space_id=$1 AND creator_user_id=$2`, spaceID, userID); err != nil {
			return err
		}
		if err := handleNoteMembershipLossTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := revokeDrawingAccessForSpaceTx(ctx, tx, spaceID); err != nil {
			return err
		}
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "member.left", userID, map[string]any{}); err != nil {
			return err
		}
		return notifySpaceControlTx(ctx, tx, map[string]any{"type": "member.left", "space_id": spaceID, "user_ids": []string{userID}})
	})
}
