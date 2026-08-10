package db

import (
	"context"
	"database/sql"
	"errors"
)

// RenameSpaceDrawing updates server-owned metadata. Every current member may
// edit drawings, so every editor may rename them.
func (db *Database) RenameSpaceDrawing(
	ctx context.Context,
	userID, drawingID, title string,
) (*SpaceDrawing, error) {
	title, err := TestingNormalizeDrawingTitle(title)
	if err != nil {
		return nil, err
	}
	drawing := &SpaceDrawing{}
	err = db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := drawingAccessForTx(ctx, tx, userID, drawingID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrSpaceNotFound
		}
		drawing.Role = access.Role
		drawing.CanDelete = access.CanDelete
		err = tx.QueryRowContext(ctx,
			`UPDATE space_drawings SET title=$1,updated_at=NOW()
			 WHERE id=$2 AND lifecycle_state='active'
			 RETURNING id,space_id,creator_user_id,title,lifecycle_state,
			           collaboration_revision,acl_version,created_at,updated_at,
			           audience_kind,COALESCE(audience_conversation_id,'')`,
			title, drawingID,
		).Scan(
			&drawing.ID, &drawing.SpaceID, &drawing.CreatorUserID, &drawing.Title,
			&drawing.LifecycleState, &drawing.CollaborationRevision,
			&drawing.ACLVersion, &drawing.CreatedAt, &drawing.UpdatedAt,
			&drawing.AudienceKind, &drawing.AudienceConversationID,
		)
		if err != nil {
			return err
		}
		return recordDrawingEventTx(
			ctx,
			tx,
			drawing.SpaceID,
			userID,
			"drawing.updated",
			drawingID,
		)
	})
	if err != nil {
		return nil, err
	}
	return drawing, nil
}

// DeleteSpaceDrawing moves a drawing to deleting and queues Durable Object
// cleanup. The row remains until the purge command is confirmed delivered.
func (db *Database) DeleteSpaceDrawing(
	ctx context.Context,
	userID, drawingID string,
) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		access, err := drawingAccessForTx(ctx, tx, userID, drawingID)
		if err != nil {
			return err
		}
		if !access.CanDelete {
			return ErrSpaceNotFound
		}
		var spaceID string
		err = tx.QueryRowContext(ctx,
			`UPDATE space_drawings
			 SET lifecycle_state='deleting',acl_version=acl_version+1,updated_at=NOW()
			 WHERE id=$1 AND lifecycle_state='active' RETURNING space_id`,
			drawingID,
		).Scan(&spaceID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		if err := recordDrawingEventTx(
			ctx,
			tx,
			spaceID,
			userID,
			"drawing.deleted",
			drawingID,
		); err != nil {
			return err
		}
		return enqueueDrawingControlTx(
			ctx,
			tx,
			drawingID,
			"purge",
			nil,
		)
	})
}
