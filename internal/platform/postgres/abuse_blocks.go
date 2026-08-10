package db

import (
	"context"
	"database/sql"
	"time"
)

// AbuseBlock is a caller barred from the API until BlockedUntil.
type AbuseBlock struct {
	Key          string
	BlockedUntil time.Time
	BlockSeconds int
	Reason       string
}

// SaveAbuseBlock records or extends a block.
//
// A repeat offender keeps the longer of the two durations, so re-offending
// after a restart escalates from where the previous block left off instead of
// resetting to the base penalty.
func (db *Database) SaveAbuseBlock(ctx context.Context, block AbuseBlock) error {
	if block.Key == "" || block.BlockSeconds <= 0 {
		return ErrSpaceInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO abuse_blocks(block_key,blocked_until,block_seconds,reason)
			VALUES($1,$2,$3,$4)
			ON CONFLICT (block_key) DO UPDATE SET
				blocked_until=GREATEST(abuse_blocks.blocked_until,EXCLUDED.blocked_until),
				block_seconds=GREATEST(abuse_blocks.block_seconds,EXCLUDED.block_seconds),
				reason=EXCLUDED.reason,
				updated_at=NOW()`,
			block.Key, block.BlockedUntil.UTC(), block.BlockSeconds, block.Reason)
		return err
	})
}

// ActiveAbuseBlocks returns every live block and prunes expired rows.
//
// The set is small by construction — one row per blocked caller — so loading it
// whole keeps the request path free of database work entirely.
func (db *Database) ActiveAbuseBlocks(ctx context.Context) ([]AbuseBlock, error) {
	blocks := []AbuseBlock{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		// Expired rows are cleaned here rather than by a separate job: the
		// refresh already runs on a timer and needs the write anyway.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM abuse_blocks WHERE blocked_until < NOW() - INTERVAL '1 day'`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT block_key,blocked_until,block_seconds,COALESCE(reason,'')
			FROM abuse_blocks WHERE blocked_until > NOW()`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var block AbuseBlock
			if err := rows.Scan(&block.Key, &block.BlockedUntil, &block.BlockSeconds, &block.Reason); err != nil {
				return err
			}
			blocks = append(blocks, block)
		}
		return rows.Err()
	})
	return blocks, err
}

// ClearAbuseBlock lifts a block, for operator intervention when a legitimate
// caller is caught.
func (db *Database) ClearAbuseBlock(ctx context.Context, key string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM abuse_blocks WHERE block_key=$1`, key)
		return err
	})
}
