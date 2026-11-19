package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// What a made item's room reports after each save: its title and plain text for search, the size
// of its snapshot and kept history for storage, and for decks and workbooks their slide or cell
// count for the level's limit. The room is the only place that sees the document, so these are
// the server's only view of it.

// itemContentSourceKind is the storage contribution a made item's room content counts as.
const itemContentSourceKind = "item_content"

// maxItemContentBytes bounds what one room may claim to store: its snapshot is at most 8 MiB, and
// its history a bounded number of those.
const maxItemContentBytes = int64(1) << 40

// SpaceItemLimits are the limits of a Space's level that a made item's room enforces or reports
// against. Nil is unlimited.
type SpaceItemLimits struct {
	HistoryDays   *int64 `json:"history_days"`
	DeckSlides    *int64 `json:"deck_slides"`
	WorkbookCells *int64 `json:"workbook_cells"`
}

func (l SpaceItemLimits) countLimit(kind SpaceItemKind) *int64 {
	switch kind.Name {
	case SpaceItemDeck:
		return l.DeckSlides
	case SpaceItemWorkbook:
		return l.WorkbookCells
	}
	return nil
}

// spaceItemLimitsTx reads the Space level's item limits: all unlimited without levels.
func spaceItemLimitsTx(ctx context.Context, tx *sql.Tx, spaceID string) (SpaceItemLimits, error) {
	level, err := spaceLevelTx(ctx, tx, spaceID)
	if err != nil || level == nil {
		return SpaceItemLimits{}, err
	}
	return SpaceItemLimits{
		HistoryDays:   level.Limits.HistoryDays,
		DeckSlides:    level.Limits.DeckSlides,
		WorkbookCells: level.Limits.WorkbookCells,
	}, nil
}

// SpaceItemLimitsForItem reads the limits that apply to one item, for its room's callback
// answer. A missing item has none.
func (db *Database) SpaceItemLimitsForItem(ctx context.Context, kindName, itemID string) (SpaceItemLimits, error) {
	kind, ok := SpaceItemKindNamed(kindName)
	if !ok {
		return SpaceItemLimits{}, ErrSpaceInvalid
	}
	var limits SpaceItemLimits
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID string
		err := tx.QueryRowContext(ctx, `SELECT space_id FROM `+kind.Table+` WHERE id=$1`, itemID).Scan(&spaceID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		limits, err = spaceItemLimitsTx(ctx, tx, spaceID)
		return err
	})
	return limits, err
}

// setItemContentStorageTx records what an item's room stores as one storage contribution of its
// creator's, and moves the Space's counter by the difference. The contribution is the ledger the
// periodic reconciliation rebuilds the counter from, so the two never drift apart for long.
func setItemContentStorageTx(ctx context.Context, tx *sql.Tx, spaceID, creatorUserID, itemID string, total int64) error {
	if total < 0 || total > maxItemContentBytes {
		return ErrSpaceInvalid
	}
	return asServiceTx(ctx, tx, func() error {
		var previous int64
		err := tx.QueryRowContext(ctx, `SELECT logical_bytes FROM space_storage_contributions
			WHERE space_id=$1 AND user_id=$2 AND source_kind=$3 AND source_id=$4 AND state='active'`,
			spaceID, creatorUserID, itemContentSourceKind, itemID).Scan(&previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if total == previous {
			return nil
		}
		if total == 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE space_storage_contributions
				SET state='released',released_at=NOW(),updated_at=NOW()
				WHERE space_id=$1 AND user_id=$2 AND source_kind=$3 AND source_id=$4 AND state='active'`,
				spaceID, creatorUserID, itemContentSourceKind, itemID); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_contributions(
				id,space_id,user_id,source_kind,source_id,logical_bytes,state)
			VALUES($1,$2,$3,$4,$5,$6,'active')
			ON CONFLICT(space_id,user_id,source_kind,source_id) DO UPDATE
			SET logical_bytes=EXCLUDED.logical_bytes,state='active',released_at=NULL,updated_at=NOW()`,
			"contribution_"+uuid.NewString(), spaceID, creatorUserID, itemContentSourceKind, itemID, total); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes+$1),version=version+1,updated_at=NOW()
			WHERE space_id=$2`, total-previous, spaceID)
		return err
	})
}

// releaseItemContentStorageTx stops counting the room content of items whose rows were purged.
func releaseItemContentStorageTx(ctx context.Context, tx *sql.Tx, itemIDs []string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	return asServiceTx(ctx, tx, func() error {
		_, err := tx.ExecContext(ctx, `WITH released AS (
				UPDATE space_storage_contributions SET state='released',released_at=NOW(),updated_at=NOW()
				WHERE source_kind=$1 AND source_id=ANY($2) AND state='active'
				RETURNING space_id,logical_bytes)
			UPDATE space_storage_usage u SET used_bytes=GREATEST(0,u.used_bytes-r.bytes),version=u.version+1,updated_at=NOW()
			FROM (SELECT space_id,sum(logical_bytes) bytes FROM released GROUP BY space_id) r
			WHERE u.space_id=r.space_id`, itemContentSourceKind, pq.Array(itemIDs))
		return err
	})
}

// SpaceMadeItemProjection is what a deck's or workbook's room posts after a save.
type SpaceMadeItemProjection struct {
	Kind         string
	ItemID       string
	Revision     int64
	Title        string
	PlainText    string
	ContentBytes int64
	HistoryBytes int64
	// Count is the deck's slides or the workbook's filled cells.
	Count int64
}

// ApplySpaceMadeItemProjection accepts only a newer room revision, so a late or repeated callback
// changes nothing.
func (db *Database) ApplySpaceMadeItemProjection(ctx context.Context, projection SpaceMadeItemProjection) (bool, error) {
	kind, err := madeItemKind(projection.Kind)
	if err != nil {
		return false, err
	}
	projection.ItemID = strings.TrimSpace(projection.ItemID)
	projection.Title = truncateRunes(strings.TrimSpace(projection.Title), 200)
	if projection.ItemID == "" || projection.Revision < 1 || len([]rune(projection.PlainText)) > 100_000 ||
		projection.ContentBytes < 0 || projection.HistoryBytes < 0 || projection.Count < 0 ||
		projection.Count > 1<<31-1 || projection.ContentBytes+projection.HistoryBytes > maxItemContentBytes {
		return false, ErrSpaceInvalid
	}
	applied := false
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, creatorUserID string
		err := tx.QueryRowContext(ctx, `UPDATE `+kind.Table+`
			SET title=COALESCE(NULLIF($1,''),title),plain_text_projection=$2,content_bytes=$3,history_bytes=$4,
			    `+kind.CountColumn+`=$5,collaboration_revision=$6,updated_at=NOW()
			WHERE id=$7 AND lifecycle_state='active' AND collaboration_revision<$6
			RETURNING space_id,creator_user_id`,
			projection.Title, projection.PlainText, projection.ContentBytes, projection.HistoryBytes,
			projection.Count, projection.Revision, projection.ItemID).Scan(&spaceID, &creatorUserID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		applied = true
		if err := setItemContentStorageTx(ctx, tx, spaceID, creatorUserID, projection.ItemID,
			projection.ContentBytes+projection.HistoryBytes); err != nil {
			return err
		}
		return recordMadeItemEventTx(ctx, tx, kind, spaceID, creatorUserID, "updated", projection.ItemID)
	})
	return applied, err
}

// SpaceDrawingActivity is what a drawing's room posts after a save. The projection fields are
// absent from rooms deployed before drawings were searchable.
type SpaceDrawingActivity struct {
	DrawingID    string
	Revision     int64
	PlainText    *string
	ContentBytes *int64
	HistoryBytes *int64
}

// ApplySpaceDrawingActivityProjection records a drawing's save: its updated time, and when the
// room sent them, its text for search and its size for storage.
func (db *Database) ApplySpaceDrawingActivityProjection(ctx context.Context, activity SpaceDrawingActivity) (bool, error) {
	if activity.DrawingID == "" || activity.Revision < 1 ||
		(activity.PlainText != nil && len([]rune(*activity.PlainText)) > 100_000) ||
		(activity.ContentBytes != nil && (*activity.ContentBytes < 0 || *activity.ContentBytes > maxItemContentBytes)) ||
		(activity.HistoryBytes != nil && (*activity.HistoryBytes < 0 || *activity.HistoryBytes > maxItemContentBytes)) {
		return false, ErrSpaceInvalid
	}
	applied := false
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		var spaceID, creatorUserID string
		var contentBytes, historyBytes int64
		err := tx.QueryRowContext(ctx,
			`UPDATE space_drawings SET collaboration_revision=$2,updated_at=NOW(),
			        plain_text_projection=COALESCE($3,plain_text_projection),
			        content_bytes=COALESCE($4,content_bytes),history_bytes=COALESCE($5,history_bytes)
			 WHERE id=$1 AND lifecycle_state='active' AND collaboration_revision<$2
			 RETURNING space_id,creator_user_id,content_bytes,history_bytes`,
			activity.DrawingID, activity.Revision, activity.PlainText, activity.ContentBytes, activity.HistoryBytes,
		).Scan(&spaceID, &creatorUserID, &contentBytes, &historyBytes)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		applied = true
		if activity.ContentBytes == nil && activity.HistoryBytes == nil {
			return nil
		}
		return setItemContentStorageTx(ctx, tx, spaceID, creatorUserID, activity.DrawingID, contentBytes+historyBytes)
	})
	return applied, err
}

// applyNoteContentBytesTx records a note's room size after its projection was applied.
func applyNoteContentBytesTx(ctx context.Context, tx *sql.Tx, spaceID, creatorUserID, noteID string, contentBytes, historyBytes *int64) error {
	if contentBytes == nil && historyBytes == nil {
		return nil
	}
	var content, history int64
	if err := tx.QueryRowContext(ctx, `UPDATE space_notes
		SET content_bytes=COALESCE($2,content_bytes),history_bytes=COALESCE($3,history_bytes)
		WHERE id=$1 RETURNING content_bytes,history_bytes`, noteID, contentBytes, historyBytes).Scan(&content, &history); err != nil {
		return err
	}
	return setItemContentStorageTx(ctx, tx, spaceID, creatorUserID, noteID, content+history)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
