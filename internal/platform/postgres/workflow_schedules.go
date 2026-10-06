package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// WorkflowSchedule is when a workflow runs on its own. A workflow has at most
// one; each run uses the workflow's latest version and posts into the
// schedule's own conversation.
type WorkflowSchedule struct {
	ID             string `json:"id"`
	UserID         string `json:"-"`
	MethodID       string `json:"method_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	ScheduleTiming
	Inputs           map[string]any `json:"inputs"`
	Enabled          bool           `json:"enabled"`
	State            string         `json:"state"`
	NextRunAt        *time.Time     `json:"next_run_at,omitempty"`
	LastInvocationID string         `json:"last_invocation_id,omitempty"`
	LastRunAt        *time.Time     `json:"last_run_at,omitempty"`
	LastError        string         `json:"last_error,omitempty"`
	RunCount         int            `json:"run_count"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

const MaxWorkflowSchedulesPerUser = 50

const workflowScheduleColumns = `id,user_id,method_id,COALESCE(conversation_id,''),timezone,rules,skip_dates,inputs,
	enabled,state,next_run_at,COALESCE(last_invocation_id,''),last_run_at,last_error,run_count,created_at,updated_at`

func scanWorkflowSchedule(scanner interface{ Scan(...any) error }, item *WorkflowSchedule) error {
	var rules, skip, inputs []byte
	err := scanner.Scan(&item.ID, &item.UserID, &item.MethodID, &item.ConversationID, &item.Timezone,
		&rules, &skip, &inputs, &item.Enabled, &item.State, &item.NextRunAt, &item.LastInvocationID,
		&item.LastRunAt, &item.LastError, &item.RunCount, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return err
	}
	return errors.Join(json.Unmarshal(rules, &item.Rules), json.Unmarshal(skip, &item.SkipDates), json.Unmarshal(inputs, &item.Inputs))
}

func queryWorkflowSchedules(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]WorkflowSchedule, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkflowSchedule{}
	for rows.Next() {
		var item WorkflowSchedule
		if err := scanWorkflowSchedule(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// nextWorkflowRun is the stored next_run_at: nil when paused or finished.
func nextWorkflowRun(item WorkflowSchedule, after time.Time) (any, error) {
	if !item.Enabled {
		return nil, nil
	}
	next, ok, err := item.NextRun(after)
	if err != nil || !ok {
		return nil, err
	}
	return next, nil
}

func jsonOrEmpty(value any, empty string) []byte {
	raw, err := json.Marshal(value)
	if err != nil || string(raw) == "null" {
		return []byte(empty)
	}
	return raw
}

// WorkflowSchedules lists the account's schedules.
func (db *Database) WorkflowSchedules(ctx context.Context, userID string) ([]WorkflowSchedule, error) {
	var items []WorkflowSchedule
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		items, err = queryWorkflowSchedules(ctx, tx, `SELECT `+workflowScheduleColumns+` FROM workflow_schedules
			WHERE user_id=$1 ORDER BY next_run_at NULLS LAST, created_at`, userID)
		return err
	})
	return items, err
}

func (db *Database) WorkflowScheduleByMethod(ctx context.Context, userID, methodID string) (*WorkflowSchedule, error) {
	out := &WorkflowSchedule{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanWorkflowSchedule(tx.QueryRowContext(ctx, `SELECT `+workflowScheduleColumns+`
			FROM workflow_schedules WHERE method_id=$1 AND user_id=$2`, methodID, userID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// SaveWorkflowSchedule creates or replaces a workflow's schedule and plans its
// next run from now. conversationID is used only when the schedule is new.
func (db *Database) SaveWorkflowSchedule(ctx context.Context, userID, conversationID string, item WorkflowSchedule, now time.Time) (*WorkflowSchedule, error) {
	item.Normalize(now)
	if item.Validate() != nil {
		return nil, ErrSpaceInvalid
	}
	next, err := nextWorkflowRun(item, now)
	if err != nil {
		return nil, ErrSpaceInvalid
	}
	out := &WorkflowSchedule{}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_schedules WHERE user_id=$1 AND method_id<>$2`, userID, item.MethodID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxWorkflowSchedulesPerUser {
			return ErrSpaceConflict
		}
		return scanWorkflowSchedule(tx.QueryRowContext(ctx, `
			INSERT INTO workflow_schedules(id,user_id,method_id,conversation_id,timezone,rules,skip_dates,inputs,enabled,next_run_at)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10)
			ON CONFLICT(method_id) DO UPDATE SET timezone=EXCLUDED.timezone,rules=EXCLUDED.rules,
				skip_dates=EXCLUDED.skip_dates,inputs=EXCLUDED.inputs,enabled=EXCLUDED.enabled,
				next_run_at=EXCLUDED.next_run_at,
				state=CASE WHEN workflow_schedules.state='running' THEN 'running' ELSE 'idle' END,updated_at=NOW()
			WHERE workflow_schedules.user_id=EXCLUDED.user_id
			RETURNING `+workflowScheduleColumns,
			"workflow_schedule_"+uuid.NewString(), userID, item.MethodID, conversationID, item.Timezone,
			jsonOrEmpty(item.Rules, `[]`), jsonOrEmpty(item.SkipDates, `[]`), jsonOrEmpty(item.Inputs, `{}`),
			item.Enabled, next), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) DeleteWorkflowSchedule(ctx context.Context, userID, methodID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM workflow_schedules WHERE method_id=$1 AND user_id=$2`, methodID, userID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

// ClaimDueWorkflowSchedules leases due schedules so exactly one server runs each
// occurrence. A run whose lease lapses (a crashed server) becomes claimable again.
// A turned-off workflow keeps its schedule but never runs.
func (db *Database) ClaimDueWorkflowSchedules(ctx context.Context, now time.Time, limit int) ([]WorkflowSchedule, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	var items []WorkflowSchedule
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var err error
		items, err = queryWorkflowSchedules(ctx, tx, `
			UPDATE workflow_schedules t SET state='running',lease_until=$1+INTERVAL '15 minutes',updated_at=NOW()
			WHERE t.id IN (
				SELECT t2.id FROM workflow_schedules t2 JOIN agent_methods m ON m.id=t2.method_id AND m.user_id=t2.user_id
				WHERE t2.enabled AND m.enabled AND t2.next_run_at<=$1
				  AND COALESCE((SELECT s.enabled FROM ai_user_settings s WHERE s.user_id=t2.user_id),TRUE)
				  AND (t2.state<>'running' OR t2.lease_until<=$1)
				ORDER BY t2.next_run_at FOR UPDATE OF t2 SKIP LOCKED LIMIT $2
			) RETURNING `+workflowScheduleColumns, now, limit)
		return err
	})
	return items, err
}

// BindWorkflowScheduleRun records which conversation and invocation a claimed run
// uses, and keeps that conversation from expiring while the schedule is alive.
func (db *Database) BindWorkflowScheduleRun(ctx context.Context, item WorkflowSchedule, conversationID, invocationID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
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
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE workflow_schedules SET state=$2,next_run_at=$3,lease_until=NULL,last_error=$4,last_run_at=$5,
				run_count=run_count+1,updated_at=NOW()
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
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
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
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
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
