package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ScheduledTask is a prompt Misty runs in the cloud on a schedule, posting each run into
// its own conversation.
type ScheduledTask struct {
	ID             string `json:"id"`
	UserID         string `json:"-"`
	ConversationID string `json:"conversation_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	Title          string `json:"title"`
	Prompt         string `json:"prompt"`
	ScheduledTaskSchedule
	Enabled          bool       `json:"enabled"`
	State            string     `json:"state"`
	NextRunAt        *time.Time `json:"next_run_at,omitempty"`
	LastInvocationID string     `json:"last_invocation_id,omitempty"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	RunCount         int        `json:"run_count"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const scheduledTaskColumns = `id,user_id,COALESCE(conversation_id,''),title,prompt,cadence,local_time,weekday,
	month_day,COALESCE(to_char(run_on,'YYYY-MM-DD'),''),timezone,enabled,state,next_run_at,
	COALESCE(last_invocation_id,''),last_run_at,last_error,run_count,created_at,updated_at,COALESCE(agent_id,'')`

func scanScheduledTask(scanner interface{ Scan(...any) error }, item *ScheduledTask) error {
	return scanner.Scan(
		&item.ID, &item.UserID, &item.ConversationID, &item.Title, &item.Prompt, &item.Cadence,
		&item.LocalTime, &item.Weekday, &item.MonthDay, &item.RunOn, &item.Timezone, &item.Enabled,
		&item.State, &item.NextRunAt, &item.LastInvocationID, &item.LastRunAt, &item.LastError,
		&item.RunCount, &item.CreatedAt, &item.UpdatedAt, &item.AgentID,
	)
}

func queryScheduledTasks(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]ScheduledTask, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ScheduledTask{}
	for rows.Next() {
		var item ScheduledTask
		if err := scanScheduledTask(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ValidScheduledTask checks the fields a person edits.
func ValidScheduledTask(item ScheduledTask) bool {
	title, prompt := strings.TrimSpace(item.Title), strings.TrimSpace(item.Prompt)
	return title != "" && len([]rune(title)) <= 120 && prompt != "" && len([]rune(prompt)) <= 8000 &&
		item.ScheduledTaskSchedule.Validate() == nil
}

// nextScheduledRun is the stored next_run_at: nil when paused or when a one-time task
// has already passed.
func nextScheduledRun(item ScheduledTask, after time.Time) (any, error) {
	if !item.Enabled {
		return nil, nil
	}
	next, ok, err := item.NextRun(after)
	if err != nil || !ok {
		return nil, err
	}
	return next, nil
}

func (db *Database) ScheduledTasks(ctx context.Context, userID string) ([]ScheduledTask, error) {
	var items []ScheduledTask
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var err error
		items, err = queryScheduledTasks(ctx, tx, `SELECT `+scheduledTaskColumns+` FROM scheduled_tasks
			WHERE user_id=$1 ORDER BY next_run_at NULLS LAST, created_at`, userID)
		return err
	})
	return items, err
}

// CreateScheduledTask stores a new task bound to an existing conversation.
func (db *Database) CreateScheduledTask(ctx context.Context, userID, conversationID string, item ScheduledTask, now time.Time) (*ScheduledTask, error) {
	if !ValidScheduledTask(item) {
		return nil, ErrSpaceInvalid
	}
	next, err := nextScheduledRun(item, now)
	if err != nil {
		return nil, ErrSpaceInvalid
	}
	out := &ScheduledTask{}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanScheduledTask(tx.QueryRowContext(ctx, `
			INSERT INTO scheduled_tasks(id,user_id,conversation_id,title,prompt,cadence,local_time,weekday,
				month_day,run_on,timezone,enabled,next_run_at,agent_id)
			VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,NULLIF($10,'')::date,$11,$12,$13,(SELECT agent_id FROM misty_ask_conversations WHERE id=$3 AND user_id=$2))
			RETURNING `+scheduledTaskColumns,
			"scheduled_task_"+uuid.NewString(), userID, conversationID, strings.TrimSpace(item.Title),
			strings.TrimSpace(item.Prompt), item.Cadence, item.LocalTime, item.Weekday, item.MonthDay,
			item.RunOn, item.Timezone, item.Enabled, next), out)
	})
	return out, err
}

// UpdateScheduledTask replaces the editable fields and reschedules from now.
func (db *Database) UpdateScheduledTask(ctx context.Context, userID string, item ScheduledTask, now time.Time) (*ScheduledTask, error) {
	if !ValidScheduledTask(item) {
		return nil, ErrSpaceInvalid
	}
	next, err := nextScheduledRun(item, now)
	if err != nil {
		return nil, ErrSpaceInvalid
	}
	out := &ScheduledTask{}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanScheduledTask(tx.QueryRowContext(ctx, `
			UPDATE scheduled_tasks SET title=$3,prompt=$4,cadence=$5,local_time=$6,weekday=$7,month_day=$8,
				run_on=NULLIF($9,'')::date,timezone=$10,enabled=$11,next_run_at=$12,
				state=CASE WHEN state='running' THEN state ELSE 'idle' END,updated_at=NOW()
			WHERE id=$1 AND user_id=$2
			RETURNING `+scheduledTaskColumns,
			item.ID, userID, strings.TrimSpace(item.Title), strings.TrimSpace(item.Prompt), item.Cadence,
			item.LocalTime, item.Weekday, item.MonthDay, item.RunOn, item.Timezone, item.Enabled, next), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// RunScheduledTaskNow makes the task due immediately; the scheduler starts it on its next pass.
func (db *Database) RunScheduledTaskNow(ctx context.Context, userID, id string, now time.Time) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET next_run_at=$3,updated_at=NOW()
			WHERE id=$1 AND user_id=$2 AND state<>'running'`, id, userID, now)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return ErrSpaceConflict
		}
		return nil
	})
}

func (db *Database) DeleteScheduledTask(ctx context.Context, userID, id string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id=$1 AND user_id=$2`, id, userID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}

// ClaimDueScheduledTasks leases due tasks so exactly one server runs each occurrence.
// A run whose lease lapses (a crashed server) becomes claimable again.
func (db *Database) ClaimDueScheduledTasks(ctx context.Context, now time.Time, limit int) ([]ScheduledTask, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	var items []ScheduledTask
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		var err error
		items, err = queryScheduledTasks(ctx, tx, `
			UPDATE scheduled_tasks t SET state='running',lease_until=$1+INTERVAL '15 minutes',updated_at=NOW()
			WHERE t.id IN (
				SELECT t2.id FROM scheduled_tasks t2
				WHERE t2.enabled AND t2.next_run_at<=$1
				  AND COALESCE((SELECT s.enabled FROM ai_user_settings s WHERE s.user_id=t2.user_id),TRUE)
				  AND (t2.state<>'running' OR t2.lease_until<=$1)
				ORDER BY t2.next_run_at FOR UPDATE SKIP LOCKED LIMIT $2
			) RETURNING `+scheduledTaskColumns, now, limit)
		return err
	})
	return items, err
}

// BindScheduledTaskRun records which conversation and invocation a claimed run uses, and
// keeps that conversation from expiring while the task is alive.
func (db *Database) BindScheduledTaskRun(ctx context.Context, item ScheduledTask, conversationID, invocationID string) error {
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET conversation_id=$2,
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

// CompleteScheduledTaskRun releases the lease and schedules the next occurrence. A
// one-time task turns itself off after it runs.
func (db *Database) CompleteScheduledTaskRun(ctx context.Context, item ScheduledTask, runErr error, now time.Time) error {
	state, message := "idle", ""
	if runErr != nil {
		state, message = "failed", runErr.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	next, err := nextScheduledRun(item, now)
	if err != nil {
		next = nil
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE scheduled_tasks SET state=$2,next_run_at=$3,lease_until=NULL,last_error=$4,last_run_at=$5,
				run_count=run_count+1,enabled=CASE WHEN cadence='once' THEN FALSE ELSE enabled END,updated_at=NOW()
			WHERE id=$1 AND state='running'`, item.ID, state, next, message, now)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("%w: scheduled task lease", ErrSpaceConflict)
		}
		return nil
	})
}

// ScheduledTaskByInvocation finds the running task that started an invocation.
func (db *Database) ScheduledTaskByInvocation(ctx context.Context, invocationID string) (*ScheduledTask, error) {
	out := &ScheduledTask{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		return scanScheduledTask(tx.QueryRowContext(ctx, `SELECT `+scheduledTaskColumns+`
			FROM scheduled_tasks WHERE last_invocation_id=$1`, invocationID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}
