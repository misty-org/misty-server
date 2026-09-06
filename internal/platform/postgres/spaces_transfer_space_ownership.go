package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
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
		// Reclaim expired AI reservations while the common Space lock is held so
		// they do not unnecessarily block a transfer.
		if _, err := refreshSpaceHostedAIWalletTx(ctx, tx, spaceID, time.Now().UTC()); err != nil {
			return err
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
		entitlements, err := entitlementsForUserTx(ctx, tx, memberID, time.Now())
		if err != nil {
			return err
		}
		var ownedSpaces int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM spaces
			WHERE owner_user_id=$1 AND lifecycle_state<>'deleted'`, memberID).Scan(&ownedSpaces); err != nil {
			return err
		}
		if ownedSpaces >= entitlements.MaxOwnedSpaces {
			return ErrSpaceOwnershipLimit
		}
		var activeReservations int
		if err := tx.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM space_upload_reservations WHERE space_id=$1 AND state='active')+
			(SELECT count(*) FROM space_rendition_reservations WHERE space_id=$1 AND state='active')+
			(SELECT count(*) FROM hosted_ai_reservations WHERE space_id=$1 AND status='reserved')`, spaceID).Scan(&activeReservations); err != nil {
			return err
		}
		if activeReservations > 0 {
			return ErrSpaceConflict
		}
		// Ownership transfer deliberately does not validate current storage.
		// The incoming owner's plan becomes the Space capacity immediately; an
		// oversized Space remains accessible and is reported as over quota.
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
		// Apply the incoming owner's allowance before releasing the Space lock.
		if _, err := refreshSpaceHostedAIWalletTx(ctx, tx, spaceID, time.Now().UTC()); err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, ownerID, "owner.transferred", memberID, map[string]any{})
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

func messagePreview(content []MessageSpan) string {
	var builder strings.Builder
	for _, span := range content {
		if span.Type == "text" {
			builder.WriteString(span.Text)
		} else if span.Type == "mention" {
			builder.WriteString("@")
			builder.WriteString(span.Label)
		} else if span.Type == "link" {
			builder.WriteString(span.Label)
		}
	}
	preview := []rune(strings.TrimSpace(builder.String()))
	if len(preview) > 180 {
		preview = append(preview[:177], '.', '.', '.')
	}
	return string(preview)
}

func (db *Database) CreateSpaceMessage(ctx context.Context, userID, spaceID string, content []MessageSpan, fileNodeIDs []string) (*SpaceMessage, []string, error) {
	return db.CreateSpaceMessageWithReferences(ctx, userID, spaceID, content, fileNodeIDs, nil, nil, "")
}

func (db *Database) CreateSpaceMessageWithReferences(ctx context.Context, userID, spaceID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID string) (*SpaceMessage, []string, error) {
	return db.CreateSpaceMessageWithReferencesAndClientNonce(ctx, userID, spaceID, content, fileNodeIDs, attachmentIDs, libraryItemIDs, replyToMessageID, "")
}

func (db *Database) CreateSpaceMessageWithReferencesAndClientNonce(ctx context.Context, userID, spaceID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID, clientNonce string) (*SpaceMessage, []string, error) {
	return db.createSpaceMessageWithReferences(ctx, userID, spaceID, "", content, fileNodeIDs, attachmentIDs, libraryItemIDs, replyToMessageID, clientNonce)
}

func (db *Database) CreateSpaceConversationMessageWithReferences(ctx context.Context, userID, spaceID, conversationID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID string) (*SpaceMessage, []string, error) {
	return db.CreateSpaceConversationMessageWithReferencesAndClientNonce(ctx, userID, spaceID, conversationID, content, fileNodeIDs, attachmentIDs, libraryItemIDs, replyToMessageID, "")
}

func (db *Database) CreateSpaceConversationMessageWithReferencesAndClientNonce(ctx context.Context, userID, spaceID, conversationID string, content []MessageSpan, fileNodeIDs, attachmentIDs, libraryItemIDs []string, replyToMessageID, clientNonce string) (*SpaceMessage, []string, error) {
	return db.createSpaceMessageWithReferences(ctx, userID, spaceID, conversationID, content, fileNodeIDs, attachmentIDs, libraryItemIDs, replyToMessageID, clientNonce)
}
