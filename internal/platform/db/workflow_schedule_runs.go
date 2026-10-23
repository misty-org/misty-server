package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ClaimDueWorkflowSchedules leases due schedules so exactly one server runs each
// occurrence. A run whose lease lapses (a crashed server) becomes claimable again.
// A turned-off workflow keeps its schedule but never runs.
func (db *Database) ClaimDueWorkflowSchedules(ctx context.Context, now time.Time, limit int) ([]WorkflowSchedule, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	var items []WorkflowSchedule
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		var err error
		items, err = queryWorkflowSchedules(ctx, tx, `
			UPDATE workflow_schedules t SET state='running',lease_until=$1+INTERVAL '15 minutes',updated_at=NOW()
			WHERE t.id IN (
				SELECT t2.id FROM workflow_schedules t2 JOIN agent_methods m ON m.id=t2.method_id AND m.user_id=t2.user_id
				WHERE t2.enabled AND m.enabled AND t2.next_run_at<=$1
				  AND COALESCE((SELECT s.enabled FROM ai_user_settings s WHERE s.user_id=t2.user_id),TRUE)
				  AND (t2.state<>'running' OR t2.lease_until<=$1)
				  AND t2.state<>'device_claimed'
				ORDER BY t2.next_run_at FOR UPDATE OF t2 SKIP LOCKED LIMIT $2
			) RETURNING `+workflowScheduleColumns, now, limit)
		return err
	})
	return items, err
}

// BindWorkflowScheduleRun records which conversation and invocation a claimed run
// uses, and keeps that conversation from expiring while the schedule is alive.
func (db *Database) BindWorkflowScheduleRun(ctx context.Context, item WorkflowSchedule, conversationID, invocationID string) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE workflow_schedules SET conversation_id=$2,
			last_invocation_id=NULLIF($3,''),updated_at=NOW() WHERE id=$1 AND state='running'`,
			item.ID, conversationID, invocationID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE misty_ask_conversations
			SET retention_expires_at=GREATEST(retention_expires_at,NOW()+INTERVAL '30 days')
			WHERE id=$1 AND user_id=$2`, conversationID, item.UserID)
		return err
	})
}

// CompleteWorkflowScheduleRun releases the lease and plans the next occurrence.
// A schedule whose rules have no more runs stays on with no next run.
func (db *Database) CompleteWorkflowScheduleRun(ctx context.Context, item WorkflowSchedule, runErr error, now time.Time) error {
	state, message := "idle", ""
	if runErr != nil {
		state, message = "failed", runErr.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	next, err := nextWorkflowRun(item, now)
	if err != nil {
		next = nil
	}
	return db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE workflow_schedules SET state=$2,next_run_at=$3,lease_until=NULL,last_error=$4,last_run_at=$5,
				run_count=run_count+1,due_at=NULL,missed_count=0,device_claim_hash=NULL,device_claim_until=NULL,updated_at=NOW()
			WHERE id=$1 AND state='running'`, item.ID, state, next, message, now)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("%w: workflow schedule lease", ErrSpaceConflict)
		}
		return nil
	})
}

// WorkflowScheduleByInvocation finds the running schedule that started an invocation.
func (db *Database) WorkflowScheduleByInvocation(ctx context.Context, invocationID string) (*WorkflowSchedule, error) {
	out := &WorkflowSchedule{}
	err := db.WithRLSContext(ctx, ServiceRLSSettings(), func(tx *sql.Tx) error {
		return scanWorkflowSchedule(tx.QueryRowContext(ctx, `SELECT `+workflowScheduleColumns+`
			FROM workflow_schedules WHERE last_invocation_id=$1`, invocationID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// UpcomingWorkflowRun is a schedule with its workflow's name, for overviews
// such as Home that list runs across agents.
type UpcomingWorkflowRun struct {
	WorkflowSchedule
	Title   string `json:"title"`
	AgentID string `json:"agent_id"`
}

// UpcomingWorkflowRuns lists the account's schedules on turned-on workflows,
// soonest first.
func (db *Database) UpcomingWorkflowRuns(ctx context.Context, userID string) ([]UpcomingWorkflowRun, error) {
	items := []UpcomingWorkflowRun{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		schedules, err := queryWorkflowSchedules(ctx, tx, `SELECT `+workflowScheduleColumns+` FROM workflow_schedules
			WHERE user_id=$1 ORDER BY next_run_at NULLS LAST, created_at`, userID)
		if err != nil {
			return err
		}
		names := map[string][2]string{}
		rows, err := tx.QueryContext(ctx, `SELECT m.id,m.agent_id,v.definition->>'title' FROM agent_methods m
			JOIN agent_method_versions v ON v.method_id=m.id AND v.version=m.current_version
			WHERE m.user_id=$1 AND m.kind='workflow' AND m.enabled`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, agent, title string
			if err := rows.Scan(&id, &agent, &title); err != nil {
				return err
			}
			names[id] = [2]string{agent, title}
		}
		for _, schedule := range schedules {
			if name, ok := names[schedule.MethodID]; ok {
				items = append(items, UpcomingWorkflowRun{WorkflowSchedule: schedule, AgentID: name[0], Title: name[1]})
			}
		}
		return rows.Err()
	})
	return items, err
}
