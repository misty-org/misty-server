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

// ActiveAbuseBlocks returns live blocks. Expired rows have a separate deadline worker.
//
// Startup, committed hints and reconnects refresh this set outside the request
// path. Burst coalescing in the guard bounds snapshot frequency.
func (db *Database) ActiveAbuseBlocks(ctx context.Context) ([]AbuseBlock, error) {
	blocks := []AbuseBlock{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
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

func (db *Database) SubscribeAbuseBlockEvents(ctx context.Context) (<-chan struct{}, func(), error) {
	return db.SubscribeWorkerEvents(ctx, "abuse-blocks")
}

// PurgeExpiredAbuseBlocks retains one day of expired records, using bounded
// locked batches so concurrent servers can cooperate without duplicate scans.
func (db *Database) PurgeExpiredAbuseBlocks(ctx context.Context, limit int) (int, error) {
	var count int64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `WITH expired AS (
   SELECT block_key FROM abuse_blocks WHERE blocked_until<=now()-interval '1 day'
   ORDER BY blocked_until FOR UPDATE SKIP LOCKED LIMIT $1
  ) DELETE FROM abuse_blocks b USING expired e WHERE b.block_key=e.block_key`, min(max(limit, 1), 500))
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		return err
	})
	return int(count), err
}
