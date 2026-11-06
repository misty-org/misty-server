package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Agent runs work on the person's computer (docs/agents/runtime.md). A run is
// pinned to the computer it was sent from (runtime_endpoint 'device:<id>'), from
// its admission. A run nobody sent from a computer (+misty email, @misty Slack, a
// schedule with no computer) goes to the first of the person's computers to claim it.

// DeviceRuntimeEndpoint is the runtime pin of a run that works on deviceID.
func DeviceRuntimeEndpoint(deviceID string) string { return "device:" + deviceID }

func runOwnerTx(ctx context.Context, tx *sql.Tx, runID string) (string, error) {
	if !invocationRunIdentity(runID) && !strings.HasPrefix(runID, "run_") {
		return "", ErrSpaceInvalid
	}
	var owner sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT user_id FROM ai_invocations WHERE id=$1),(SELECT owner_user_id FROM space_runs WHERE id=$1))`, runID).Scan(&owner)
	if err != nil {
		return "", err
	}
	if !owner.Valid || owner.String == "" {
		return "", ErrSpaceNotFound
	}
	return owner.String, nil
}

// QueueAgentRuntimeStart queues a run's start for its desktop. A run is queued
// to start once, however often this is called.
func (db *Database) QueueAgentRuntimeStart(ctx context.Context, runID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		owner, err := runOwnerTx(ctx, tx, runID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_runtime_deliveries(id,user_id,run_id,operation) VALUES($1,$2,$3,'invocation.start') ON CONFLICT(id) DO NOTHING`, "start:"+runID, owner, runID)
		return err
	})
}

// QueueAgentRuntimeCancel asks the desktop running a run to cancel it.
func (db *Database) QueueAgentRuntimeCancel(ctx context.Context, runID, runtimeID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		owner, err := runOwnerTx(ctx, tx, runID)
		if err != nil {
			return err
		}
		return queueAgentContinuationTx(ctx, tx, owner, runID, "runtime.cancel", runtimeID, AgentContinuation{RuntimeID: runtimeID})
	})
}

// ClaimDeviceAgentRuntimeDeliveries leases the deliveries that deviceID runs.
func (db *Database) ClaimDeviceAgentRuntimeDeliveries(ctx context.Context, userID, deviceID string, limit int) ([]AgentRuntimeDelivery, error) {
	if limit < 1 || limit > 20 {
		limit = 10
	}
	items := []AgentRuntimeDelivery{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE agent_runtime_deliveries d SET state='leased',lease_id=$1,lease_expires_at=NOW()+INTERVAL '90 seconds',attempts=d.attempts+1,updated_at=NOW()
   WHERE d.id IN (SELECT q.id FROM agent_runtime_deliveries q
     LEFT JOIN ai_invocations i ON i.id=q.run_id
     LEFT JOIN space_runs r ON r.id=q.run_id
     WHERE q.user_id=$3 AND q.available_at<=NOW() AND (q.state='pending' OR q.state='leased' AND q.lease_expires_at<NOW())
       AND COALESCE(i.runtime_endpoint,r.runtime_endpoint,$4)=$4
     ORDER BY q.available_at,q.created_at FOR UPDATE OF q SKIP LOCKED LIMIT $2)
   RETURNING id,user_id,run_id,operation,payload,lease_id,attempts`, uuid.NewString(), limit, userID, DeviceRuntimeEndpoint(deviceID))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AgentRuntimeDelivery
			if err := rows.Scan(&item.ID, &item.UserID, &item.RunID, &item.Operation, &item.Payload, &item.LeaseID, &item.Attempts); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// LeasedAgentRuntimeDelivery is a delivery the person's desktop holds under leaseID.
func (db *Database) LeasedAgentRuntimeDelivery(ctx context.Context, userID, id, leaseID string) (AgentRuntimeDelivery, error) {
	var item AgentRuntimeDelivery
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,user_id,run_id,operation,payload,lease_id,attempts FROM agent_runtime_deliveries
   WHERE id=$1 AND lease_id=$2 AND user_id=$3 AND state='leased'`, id, leaseID, userID).Scan(&item.ID, &item.UserID, &item.RunID, &item.Operation, &item.Payload, &item.LeaseID, &item.Attempts)
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrSpaceConflict
	}
	return item, err
}

// AIInvocationRuntimeEndpoint is the computer a run is pinned to, empty if none yet.
// A sub-agent stays on its parent's.
func (db *Database) AIInvocationRuntimeEndpoint(ctx context.Context, userID, invocationID string) (string, error) {
	var endpoint sql.NullString
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT runtime_endpoint FROM ai_invocations WHERE id=$1 AND user_id=$2`, invocationID, userID).Scan(&endpoint)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSpaceNotFound
	}
	return endpoint.String, err
}

// DeviceBelongsToUser reports whether a computer is one of the person's own.
func (db *Database) DeviceBelongsToUser(ctx context.Context, userID, deviceID string) (bool, error) {
	var owned bool
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, deviceID, userID).Scan(&owned)
	})
	return owned, err
}

// AnyDesktopOnline reports whether one of the person's devices was seen in the
// last 90 seconds, so a queued run will start soon.
func (db *Database) AnyDesktopOnline(ctx context.Context, userID string) (bool, error) {
	var online bool
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE user_id=$1 AND revoked_at IS NULL AND last_seen_at>NOW()-INTERVAL '90 seconds')`, userID).Scan(&online)
	})
	return online, err
}
