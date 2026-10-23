package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	Inputs  map[string]any `json:"inputs"`
	Enabled bool           `json:"enabled"`
	// DeviceID names the computer whose Misty browser the run uses. Empty runs
	// on the server with no browser.
	DeviceID string `json:"device_id,omitempty"`
	// DueAt is the occurrence waiting for that computer, and MissedCount the
	// later ones that passed while it waited.
	DueAt            *time.Time `json:"due_at,omitempty"`
	MissedCount      int        `json:"missed_count,omitempty"`
	State            string     `json:"state"`
	NextRunAt        *time.Time `json:"next_run_at,omitempty"`
	LastInvocationID string     `json:"last_invocation_id,omitempty"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	RunCount         int        `json:"run_count"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const MaxWorkflowSchedulesPerUser = 50

const workflowScheduleColumns = `id,user_id,method_id,COALESCE(conversation_id,''),timezone,rules,skip_dates,inputs,
	enabled,state,next_run_at,COALESCE(last_invocation_id,''),last_run_at,last_error,run_count,created_at,updated_at,
	COALESCE(device_id,''),due_at,missed_count`

func scanWorkflowSchedule(scanner interface{ Scan(...any) error }, item *WorkflowSchedule) error {
	var rules, skip, inputs []byte
	err := scanner.Scan(&item.ID, &item.UserID, &item.MethodID, &item.ConversationID, &item.Timezone,
		&rules, &skip, &inputs, &item.Enabled, &item.State, &item.NextRunAt, &item.LastInvocationID,
		&item.LastRunAt, &item.LastError, &item.RunCount, &item.CreatedAt, &item.UpdatedAt,
		&item.DeviceID, &item.DueAt, &item.MissedCount)
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
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		items, err = queryWorkflowSchedules(ctx, tx, `SELECT `+workflowScheduleColumns+` FROM workflow_schedules
			WHERE user_id=$1 ORDER BY next_run_at NULLS LAST, created_at`, userID)
		return err
	})
	return items, err
}

func (db *Database) WorkflowScheduleByMethod(ctx context.Context, userID, methodID string) (*WorkflowSchedule, error) {
	out := &WorkflowSchedule{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
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
	err = db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_schedules WHERE user_id=$1 AND method_id<>$2`, userID, item.MethodID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxWorkflowSchedulesPerUser {
			return ErrSpaceConflict
		}
		if item.DeviceID != "" {
			var usable bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_devices WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, item.DeviceID, userID).Scan(&usable); err != nil {
				return err
			}
			if !usable {
				return ErrSpaceInvalid
			}
		}
		// A run already waiting for a computer keeps waiting, for the newly
		// chosen one; with no computer chosen it is dropped.
		return scanWorkflowSchedule(tx.QueryRowContext(ctx, `
			INSERT INTO workflow_schedules(id,user_id,method_id,conversation_id,timezone,rules,skip_dates,inputs,enabled,next_run_at,device_id)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,NULLIF($11,''))
			ON CONFLICT(method_id) DO UPDATE SET timezone=EXCLUDED.timezone,rules=EXCLUDED.rules,
				skip_dates=EXCLUDED.skip_dates,inputs=EXCLUDED.inputs,enabled=EXCLUDED.enabled,
				next_run_at=EXCLUDED.next_run_at,device_id=EXCLUDED.device_id,
				state=CASE WHEN workflow_schedules.state IN ('running','device_claimed') THEN workflow_schedules.state
					WHEN workflow_schedules.state='awaiting_device' AND EXCLUDED.device_id IS NOT NULL AND EXCLUDED.enabled THEN 'awaiting_device'
					ELSE 'idle' END,
				due_at=CASE WHEN workflow_schedules.state='awaiting_device' AND (EXCLUDED.device_id IS NULL OR NOT EXCLUDED.enabled) THEN NULL ELSE workflow_schedules.due_at END,
				missed_count=CASE WHEN workflow_schedules.state='awaiting_device' AND (EXCLUDED.device_id IS NULL OR NOT EXCLUDED.enabled) THEN 0 ELSE workflow_schedules.missed_count END,
				updated_at=NOW()
			WHERE workflow_schedules.user_id=EXCLUDED.user_id
			RETURNING `+workflowScheduleColumns,
			"workflow_schedule_"+uuid.NewString(), userID, item.MethodID, conversationID, item.Timezone,
			jsonOrEmpty(item.Rules, `[]`), jsonOrEmpty(item.SkipDates, `[]`), jsonOrEmpty(item.Inputs, `{}`),
			item.Enabled, next, item.DeviceID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

func (db *Database) DeleteWorkflowSchedule(ctx context.Context, userID, methodID string) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
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
