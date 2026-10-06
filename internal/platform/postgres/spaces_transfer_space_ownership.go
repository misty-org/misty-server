package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (db *Database) TransferSpaceOwnership(ctx context.Context, ownerID, spaceID, memberID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:owner:"+memberID); err != nil {
			return err
		}
		if err := requireSpaceOwnerTx(ctx, tx, spaceID, ownerID); err != nil {
			return err
		}
		var isDefault bool
		if err := tx.QueryRowContext(ctx, `SELECT is_default FROM spaces WHERE id=$1 FOR UPDATE`, spaceID).Scan(&isDefault); err != nil {
			return err
		}
		if isDefault {
			return ErrDefaultSpaceProtected
		}
		var role string
		if err := tx.QueryRowContext(ctx, `SELECT role FROM space_members WHERE space_id=$1 AND user_id=$2 FOR UPDATE`, spaceID, memberID).Scan(&role); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		if role != "member" {
			return ErrSpaceInvalid
		}
		var activeReservations int
		if err := tx.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM space_upload_reservations WHERE space_id=$1 AND state='active')+
			(SELECT count(*) FROM space_rendition_reservations WHERE space_id=$1 AND state='active')`, spaceID).Scan(&activeReservations); err != nil {
			return err
		}
		if activeReservations > 0 {
			return ErrSpaceConflict
		}
		// Ownership transfer deliberately does not validate current storage.
		// Each contributor keeps their account-owned bytes; Space ownership
		// never transfers cloud usage or introduces a Space allowance.
		if _, err := tx.ExecContext(ctx, `UPDATE space_members SET role='member' WHERE space_id=$1 AND user_id=$2`, spaceID, ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_members SET role='owner' WHERE space_id=$1 AND user_id=$2`, spaceID, memberID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE spaces SET owner_user_id=$1,updated_at=NOW() WHERE id=$2`, memberID, spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE security_domains SET owner_user_id=$1,version=version+1,updated_at=NOW() WHERE space_id=$2 AND kind='space'`, memberID, spaceID); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, ownerID, "owner.transferred", memberID, map[string]any{})
		return err
	})
}

func TestingValidateMessage(content []MessageSpan, fileNodeIDs []string) error {
	return validateMessageWithReferences(content, len(fileNodeIDs))
}

func requireSpaceMessageWriteTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesRead); err != nil {
		return err
	}
	return requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionMessagesWrite)
}

func validateMessageWithReferences(content []MessageSpan, referenceCount int) error {
	if referenceCount > MaxMessageFiles || len(content) == 0 && referenceCount == 0 {
		return ErrSpaceInvalid
	}
	chars := 0
	for _, span := range content {
		switch span.Type {
		case "text":
			chars += len([]rune(span.Text))
		case "mention":
			if (span.UserID == "") == (span.AgentID == "") {
				return ErrSpaceInvalid
			}
			chars += len([]rune(span.Label))
		case "link":
			if strings.TrimSpace(span.Label) == "" || !strings.HasPrefix(span.URL, "/spaces/") {
				return ErrSpaceInvalid
			}
			chars += len([]rune(span.Label))
		default:
			return ErrSpaceInvalid
		}
	}
	if chars > MaxMessageChars || chars < 1 && referenceCount == 0 {
		return ErrSpaceInvalid
	}
	return nil
}

func (db *Database) CreateSpaceMessageWithReferencesAndClientNonce(ctx context.Context, userID, spaceID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID, clientNonce string) (*SpaceMessage, []string, error) {
	return db.createSpaceMessageWithReferences(ctx, userID, spaceID, "", content, fileNodeIDs, attachmentIDs, libraryItemIDs, replyToMessageID, clientNonce)
}

func (db *Database) CreateSpaceConversationMessageWithReferencesAndClientNonce(ctx context.Context, userID, spaceID, conversationID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID, clientNonce string) (*SpaceMessage, []string, error) {
	return db.createSpaceMessageWithReferences(ctx, userID, spaceID, conversationID, content, fileNodeIDs, attachmentIDs, libraryItemIDs, replyToMessageID, clientNonce)
}
