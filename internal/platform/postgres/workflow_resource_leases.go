package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const resourceLeasePrefix = "resource-lease:"

func resourceLeaseTopic(topic string) bool { return validDigestTopic(topic, resourceLeasePrefix) }
func validDigestTopic(topic, prefix string) bool {
	if !strings.HasPrefix(topic, prefix) || len(topic) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(topic[len(prefix):])
	return err == nil
}
func (db *Database) SubscribeWorkflowResourceLeaseEvents(ctx context.Context, key string) (<-chan struct{}, func(), error) {
	digest := sha256.Sum256([]byte(key))
	return db.subscribeWorkerTopic(ctx, resourceLeasePrefix+hex.EncodeToString(digest[:]))
}

func (db *Database) AcquireWorkflowResourceLease(ctx context.Context, runID, nodeID, key, fingerprint string, duration time.Duration) (bool, error) {
	acquired, _, err := db.TryWorkflowResourceLease(ctx, runID, nodeID, key, fingerprint, duration)
	return acquired, err
}

// TryWorkflowResourceLease either acquires the row or returns its remaining
// database-clock lifetime. Planning and claiming use the same locked row.
// Run-level reentrancy is retained for nested workflow nodes.
func (db *Database) TryWorkflowResourceLease(ctx context.Context, runID, nodeID, key, fingerprint string, duration time.Duration) (bool, time.Duration, error) {
	if duration < time.Second || duration > 10*time.Minute {
		return false, 0, ErrSpaceInvalid
	}
	acquired := false
	var seconds float64
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO space_workflow_resource_leases(resource_key,run_id,node_id,fingerprint,expires_at)
   VALUES($1,$2,$3,$4,clock_timestamp()+$5::interval)
   ON CONFLICT(resource_key) DO UPDATE SET run_id=EXCLUDED.run_id,node_id=EXCLUDED.node_id,
    fingerprint=EXCLUDED.fingerprint,expires_at=clock_timestamp()+$5::interval,created_at=clock_timestamp()
   WHERE space_workflow_resource_leases.expires_at<=clock_timestamp()
    OR space_workflow_resource_leases.run_id=EXCLUDED.run_id`, key, runID, nodeID, fingerprint, duration.String())
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		acquired = count == 1
		if acquired {
			return nil
		}
		err = tx.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM (expires_at-clock_timestamp()))
   FROM space_workflow_resource_leases WHERE resource_key=$1`, key).Scan(&seconds)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	// Cap unexpected historic values to the maximum valid lease duration.
	return acquired, time.Duration(max(0, min(seconds, 600)) * float64(time.Second)), err
}
