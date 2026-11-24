package db

import (
	"context"
	"database/sql"
	"errors"
)

func isDirectSpaceTx(ctx context.Context, tx *sql.Tx, spaceID string) (bool, error) {
	var direct bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM spaces WHERE id=$1 AND kind='direct')`, spaceID).Scan(&direct)
	return direct, err
}

func requireInDirectTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	var member bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM spaces s
		JOIN space_members m ON m.space_id=s.id AND m.user_id=$2
		WHERE s.id=$1 AND s.kind='direct' AND s.lifecycle_state='active')`, spaceID, userID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return ErrLibraryNotFound
	}
	return nil
}

func requireDirectUploadTx(ctx context.Context, tx *sql.Tx, userID, spaceID, conversationID, purpose string) error {
	if purpose != UploadPurposeChatAttachment || conversationID != "" {
		return ErrLibraryInvalid
	}
	if err := requireInDirectTx(ctx, tx, userID, spaceID); err != nil {
		return err
	}
	if err := requireDirectWritableTx(ctx, tx, userID, spaceID); errors.Is(err, ErrNotFriends) {
		return ErrLibraryForbidden
	} else if err != nil {
		return err
	}
	return nil
}

func (db *Database) IsDirectSpace(ctx context.Context, spaceID string) (bool, error) {
	var direct bool
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		direct, err = isDirectSpaceTx(ctx, tx, spaceID)
		return err
	})
	return direct, err
}

func (db *Database) RequireDirectMember(ctx context.Context, userID, spaceID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return requireInDirectTx(ctx, tx, userID, spaceID)
	})
}
