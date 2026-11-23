package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const itemContentSourceKind = "item_content"

const maxItemContentBytes = int64(1) << 40

type SpaceItemLimits struct {
	HistoryDays *int64 `json:"history_days"`
}

func spaceItemLimitsTx(ctx context.Context, tx *sql.Tx, spaceID string) (SpaceItemLimits, error) {
	level, err := spaceLevelTx(ctx, tx, spaceID)
	if err != nil || level == nil {
		return SpaceItemLimits{}, err
	}
	return SpaceItemLimits{HistoryDays: level.Limits.HistoryDays}, nil
}

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
