package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Scheduled runs that use a computer's Misty browser (roadmap C1). The server
// never starts them: the run waits until Misty is open on that computer, which
// claims it and starts it with its own signed grant. Occurrences that pass
// while the computer is away fold into the one waiting run.

const deviceScheduleClaimTTL = `INTERVAL '10 minutes'`

// AwaitWorkflowScheduleDevice parks a claimed occurrence until its computer
// picks it up, and plans the next occurrence so the schedule keeps its rhythm.
func (db *Database) AwaitWorkflowScheduleDevice(ctx context.Context, item WorkflowSchedule, now time.Time) error {
	occurrence := now
	if item.NextRunAt != nil {
		occurrence = *item.NextRunAt
	}
	next, err := nextWorkflowRun(item, now)
	if err != nil {
		next = nil
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE workflow_schedules SET state='awaiting_device',
			missed_count=CASE WHEN due_at IS NULL THEN 0 ELSE missed_count+1 END,
			due_at=COALESCE(due_at,$2),next_run_at=$3,lease_until=NULL,updated_at=NOW()
			WHERE id=$1 AND state='running'`, item.ID, occurrence, next)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("%w: workflow schedule lease", ErrSpaceConflict)
		}
		return nil
	})
}

// DeviceScheduledRun is a run waiting for this computer, with what it needs
// to start: the workflow's latest version and its agent.
type DeviceScheduledRun struct {
	WorkflowSchedule
	Title           string `json:"title"`
	AgentID         string `json:"agent_id"`
	MethodVersionID string `json:"method_version_id"`
}

// DeviceScheduledRuns lists runs waiting for one of the caller's computers.
// A claim that lapsed (the app quit mid-start) is offered again.
func (db *Database) DeviceScheduledRuns(ctx context.Context, userID, deviceID string, now time.Time) ([]DeviceScheduledRun, error) {
	items := []DeviceScheduledRun{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		schedules, err := queryWorkflowSchedules(ctx, tx, `SELECT `+workflowScheduleColumns+` FROM workflow_schedules
			WHERE user_id=$1 AND device_id=$2 AND enabled
			  AND (state='awaiting_device' OR (state='device_claimed' AND device_claim_until<=$3))
			ORDER BY due_at`, userID, deviceID, now)
		if err != nil {
			return err
		}
		for _, schedule := range schedules {
			item := DeviceScheduledRun{WorkflowSchedule: schedule}
			err := tx.QueryRowContext(ctx, `SELECT COALESCE(v.definition->>'title',''),m.agent_id,v.id
				FROM agent_methods m JOIN agent_method_versions v ON v.method_id=m.id AND v.version=m.current_version
				WHERE m.id=$1 AND m.user_id=$2 AND m.enabled`, schedule.MethodID, userID).Scan(&item.Title, &item.AgentID, &item.MethodVersionID)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		return nil
	})
	return items, err
}

// ClaimDeviceScheduledRun lets one computer take a waiting run. Only the named
// computer can, and only one claim is live at a time.
func (db *Database) ClaimDeviceScheduledRun(ctx context.Context, userID, scheduleID, deviceID, claimHash string, now time.Time) (*WorkflowSchedule, error) {
	out := &WorkflowSchedule{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanWorkflowSchedule(tx.QueryRowContext(ctx, `UPDATE workflow_schedules
			SET state='device_claimed',device_claim_hash=$4,device_claim_until=$5::timestamptz+`+deviceScheduleClaimTTL+`,updated_at=NOW()
			WHERE id=$1 AND user_id=$2 AND device_id=$3 AND enabled
			  AND (state='awaiting_device' OR (state='device_claimed' AND device_claim_until<=$5))
			RETURNING `+workflowScheduleColumns, scheduleID, userID, deviceID, claimHash, now), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceConflict
	}
	return out, err
}

// ConsumeDeviceScheduledRunClaim turns a computer's claim into a running
// occurrence, as the run starts. The run must carry that computer's grant.
func (db *Database) ConsumeDeviceScheduledRunClaim(ctx context.Context, userID, scheduleID, claimHash string, deviceIDs []string, now time.Time) (*WorkflowSchedule, error) {
	out := &WorkflowSchedule{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanWorkflowSchedule(tx.QueryRowContext(ctx, `UPDATE workflow_schedules
			SET state='running',lease_until=$4::timestamptz+INTERVAL '15 minutes',device_claim_hash=NULL,device_claim_until=NULL,updated_at=NOW()
			WHERE id=$1 AND user_id=$2 AND state='device_claimed' AND device_claim_hash=$3 AND device_claim_until>$4
			  AND device_id=ANY($5::text[])
			RETURNING `+workflowScheduleColumns, scheduleID, userID, claimHash, now, pqStringArray(deviceIDs)), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceForbidden
	}
	return out, err
}
