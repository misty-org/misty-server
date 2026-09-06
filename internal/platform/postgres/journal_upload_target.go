package db

import (
	"context"
	"database/sql"
	"errors"
)

// ValidateJournalUploadTarget binds the immutable upload parent to the HTTP
// route. A generic Library route cannot finalize a Journal asset with a broader
// library.upload grant. The completion transaction rechecks current access too.
func (db *Database) ValidateJournalUploadTarget(ctx context.Context, userID, spaceID, uploadID, noteID, drawingID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var purpose, storedNote, storedDrawing string
		err := tx.QueryRowContext(ctx, `SELECT purpose,COALESCE(note_id,''),COALESCE(drawing_id,'')
			FROM space_library_uploads WHERE id=$1 AND space_id=$2 AND user_id=$3`, uploadID, spaceID, userID).Scan(&purpose, &storedNote, &storedDrawing)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		}
		if err != nil {
			return err
		}
		if noteID != storedNote || drawingID != storedDrawing ||
			(noteID != "" && purpose != UploadPurposeNoteAttachment) ||
			(drawingID != "" && purpose != UploadPurposeDrawingAsset) {
			return ErrLibraryNotFound
		}
		return requireJournalUploadAccessTx(ctx, tx, userID, uploadID, purpose)
	})
}

func requireJournalUploadAccessTx(ctx context.Context, tx *sql.Tx, userID, uploadID, purpose string) error {
	switch purpose {
	case UploadPurposeNoteAttachment:
		var noteID string
		if err := tx.QueryRowContext(ctx, `SELECT note_id FROM space_library_uploads WHERE id=$1`, uploadID).Scan(&noteID); err != nil {
			return err
		}
		access, err := noteAccessForTx(ctx, tx, userID, noteID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrLibraryNotFound
		}
	case UploadPurposeDrawingAsset:
		var drawingID string
		if err := tx.QueryRowContext(ctx, `SELECT drawing_id FROM space_library_uploads WHERE id=$1`, uploadID).Scan(&drawingID); err != nil {
			return err
		}
		access, err := drawingAccessForTx(ctx, tx, userID, drawingID)
		if err != nil {
			return err
		}
		if !access.CanEdit {
			return ErrLibraryNotFound
		}
	}
	return nil
}
