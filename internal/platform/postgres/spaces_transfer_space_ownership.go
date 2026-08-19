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
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, ownerID); err != nil {
			return err
		}
		if misty, err := isMistySpaceTx(ctx, tx, spaceID); err != nil {
			return err
		} else if misty {
			operator, operatorErr := isMistyOperatorTx(ctx, tx, memberID)
			if operatorErr != nil {
				return operatorErr
			}
			if !operator {
				return ErrSpaceForbidden
			}
		}
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM spaces WHERE id=$1 FOR UPDATE`, spaceID); err != nil {
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
		storageLockOwners := []string{ownerID, memberID}
		if storageLockOwners[1] < storageLockOwners[0] {
			storageLockOwners[0], storageLockOwners[1] = storageLockOwners[1], storageLockOwners[0]
		}
		for _, storageOwnerID := range storageLockOwners {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "owner-storage:"+storageOwnerID); err != nil {
				return err
			}
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
		incoming, err := ownerStorageUsageTx(ctx, tx, memberID, true)
		if err != nil {
			return err
		}
		// Use the authoritative per-Space rows for the transfer decision. The
		// owner aggregate is maintained by triggers, but transfer must remain
		// correct even if an older deployment left that cache stale.
		var incomingUsed, incomingReserved int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(u.used_bytes),0),COALESCE(SUM(u.reserved_bytes),0)
			FROM spaces s JOIN space_storage_usage u ON u.space_id=s.id
			WHERE s.owner_user_id=$1 AND s.lifecycle_state='active'`, memberID).Scan(&incomingUsed, &incomingReserved); err != nil {
			return err
		}
		var spaceUsed, spaceReserved int64
		if err := tx.QueryRowContext(ctx, `SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1`, spaceID).Scan(&spaceUsed, &spaceReserved); errors.Is(err, sql.ErrNoRows) {
			spaceUsed, spaceReserved = 0, 0
		} else if err != nil {
			return err
		}
		if incomingUsed+incomingReserved+spaceUsed+spaceReserved > incoming.LimitBytes {
			return ErrLibraryQuota
		}
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
