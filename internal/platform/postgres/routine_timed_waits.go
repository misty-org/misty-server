package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	cap "github.com/kannachi323/misty/server/internal/capabilities"
)

type RoutineWaitRecord struct {
	StepID      string     `json:"stepId"`
	WaitID      string     `json:"waitId"`
	State       string     `json:"state"`
	Until       *time.Time `json:"until,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	RemainingMS int64      `json:"remainingMs"`
}

func routineWaitTx(ctx context.Context, tx *sql.Tx, userID, runID, stepID string) (*RoutineWaitRecord, error) {
	var out RoutineWaitRecord
	var now time.Time
	err := tx.QueryRowContext(ctx, `SELECT step_id,wait_id,state,until_at,expires_at,clock_timestamp() FROM misty_routine_waits WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3`, userID, runID, stepID).Scan(&out.StepID, &out.WaitID, &out.State, &out.Until, &out.ExpiresAt, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	if out.State == "waiting" && out.Until != nil && out.Until.After(now) {
		out.RemainingMS = int64((out.Until.Sub(now) + time.Millisecond - 1) / time.Millisecond)
	}
	return &out, nil
}
func (db *Database) RoutineWaits(ctx context.Context, userID, runID string) ([]RoutineWaitRecord, error) {
	out := []RoutineWaitRecord{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT step_id,wait_id,state,until_at,expires_at FROM misty_routine_waits WHERE user_id=$1 AND invocation_id=$2 ORDER BY step_id`, userID, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item RoutineWaitRecord
			if err := rows.Scan(&item.StepID, &item.WaitID, &item.State, &item.Until, &item.ExpiresAt); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	})
	return out, err
}
func routineWaitRunLockTx(ctx context.Context, tx *sql.Tx, userID, runID, runtimeID string) (string, time.Time, bool, error) {
	var state, pinned, space string
	var cancelled, live bool
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT i.state,i.runtime_run_id,COALESCE(i.space_id,''),r.cancel_requested_at IS NOT NULL,i.expires_at>clock_timestamp(),clock_timestamp() FROM ai_invocations i JOIN misty_routine_runs r ON r.invocation_id=i.id AND r.user_id=i.user_id WHERE i.id=$1 AND i.user_id=$2 FOR UPDATE OF i`, runID, userID).Scan(&state, &pinned, &space, &cancelled, &live, &now); err != nil {
		return "", now, false, err
	}
	if runtimeID == "" || pinned != runtimeID || cancelled || state == "completed" || state == "failed" || state == "canceled" {
		return "", now, false, ErrSpaceConflict
	}
	return state, now, live, sdkTargetSpaceAccessTx(ctx, tx, userID, space)
}
func routineTimerEventTx(ctx context.Context, tx *sql.Tx, runID, waitID, phase, message string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key) SELECT $1,n,'assistant.status',jsonb_build_object('id',n::text,'type','assistant.status','phase',$3::text,'text',$4::text),$2 FROM (SELECT COALESCE(MAX(sequence),0)+1 n FROM ai_invocation_events WHERE invocation_id=$1) seq ON CONFLICT(invocation_id,receipt_key) WHERE receipt_key IS NOT NULL DO NOTHING`, runID, "routine-timer:"+waitID+":"+phase, phase, message)
	return err
}

// The API derives until from the immutable program and prior protected results.
// The row lock makes entering the wait atomic with pausing the execution clock.
func (db *Database) OpenRoutineWait(ctx context.Context, userID, runID, runtimeID, stepID string, until time.Time) (*RoutineWaitRecord, error) {
	if until.IsZero() {
		return nil, ErrSpaceInvalid
	}
	var out *RoutineWaitRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		state, now, live, err := routineWaitRunLockTx(ctx, tx, userID, runID, runtimeID)
		if err != nil {
			return err
		}
		if !live {
			return ErrSpaceConflict
		}
		wait, err := routineWaitTx(ctx, tx, userID, runID, stepID)
		if err != nil {
			return err
		}
		if wait.State != "pending" {
			if wait.Until == nil || !wait.Until.Equal(until) {
				return ErrSpaceConflict
			}
			out = wait
			return nil
		}
		if state != "running" || !until.Before(now.Add(24*time.Hour)) {
			return ErrSpaceInvalid
		}
		var unsettled bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_toolbox_action_journal WHERE user_id=$1 AND run_id=$2 AND state IN ('started','unknown'))`, userID, runID).Scan(&unsettled); err != nil {
			return err
		}
		if unsettled {
			return ErrSpaceConflict
		}
		waitState, phase, message := "waiting", "awaiting_timer", "The routine is waiting until its saved time."
		if !until.After(now) {
			waitState, phase, message = "completed", "timer_resume_pending", "The routine's saved time has arrived."
		}
		if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_waits SET state=$4,until_at=$5,expires_at=$6,updated_at=NOW() WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3 AND state='pending'`, userID, runID, stepID, waitState, until, now.Add(24*time.Hour)); err != nil {
			return err
		}
		if waitState == "waiting" {
			if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='awaiting_timer',expires_at=GREATEST(expires_at,$3),updated_at=NOW() WHERE id=$1 AND user_id=$2`, runID, userID, now.Add(24*time.Hour)); err != nil {
				return err
			}
		}
		if err := routineTimerEventTx(ctx, tx, runID, wait.WaitID, phase, message); err != nil {
			return err
		}
		out, err = routineWaitTx(ctx, tx, userID, runID, stepID)
		return err
	})
	return out, err
}

// The timer wake-up is an observation, not authority. Only this transaction can
// commit its completion; an early, stale or duplicate wake cannot clear a later wait.
func (db *Database) ResumeRoutineWait(ctx context.Context, userID, runID, runtimeID, stepID, waitID string) (*RoutineWaitRecord, error) {
	if !cap.ValidID(waitID) {
		return nil, ErrSpaceInvalid
	}
	var out *RoutineWaitRecord
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		state, now, _, err := routineWaitRunLockTx(ctx, tx, userID, runID, runtimeID)
		if err != nil {
			return err
		}
		wait, err := routineWaitTx(ctx, tx, userID, runID, stepID)
		if err != nil {
			return err
		}
		if wait.WaitID != waitID {
			return ErrSpaceConflict
		}
		if wait.State == "completed" || wait.State == "expired" {
			out = wait
			return nil
		}
		if wait.State != "waiting" || state != "awaiting_timer" || wait.Until == nil || wait.ExpiresAt == nil {
			return ErrSpaceConflict
		}
		if now.Before(*wait.Until) {
			out = wait
			return nil
		}
		next, phase, message := "completed", "timer_resume_pending", "The routine's saved time has arrived."
		if !now.Before(*wait.ExpiresAt) {
			next, phase, message = "expired", "timer_expired", "The routine's timed wait expired before it could resume."
		}
		if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_waits SET state=$4,updated_at=NOW() WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3 AND state='waiting'`, userID, runID, stepID, next); err != nil {
			return err
		}
		// The clock stays paused until the next admitted operation asks to begin it.
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='running',updated_at=NOW() WHERE id=$1 AND user_id=$2 AND state='awaiting_timer'`, runID, userID); err != nil {
			return err
		}
		if err := routineTimerEventTx(ctx, tx, runID, waitID, phase, message); err != nil {
			return err
		}
		out, err = routineWaitTx(ctx, tx, userID, runID, stepID)
		return err
	})
	return out, err
}

// Expiry and interruption delivery commit together, even if the sleeping worker
// is unavailable. The normal cancellation/reconciliation path retains effects.
func (db *Database) ExpireRoutineWaits(ctx context.Context) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.user_id,i.runtime_run_id,w.step_id,w.wait_id FROM ai_invocations i JOIN misty_routine_waits w ON w.invocation_id=i.id AND w.user_id=i.user_id WHERE i.state='awaiting_timer' AND w.state='waiting' AND w.expires_at<=clock_timestamp() ORDER BY w.expires_at FOR UPDATE OF i SKIP LOCKED LIMIT 20`)
		if err != nil {
			return err
		}
		type item struct{ run, user, runtime, step, wait string }
		items := []item{}
		for rows.Next() {
			var value item
			if err := rows.Scan(&value.run, &value.user, &value.runtime, &value.step, &value.wait); err != nil {
				rows.Close()
				return err
			}
			items = append(items, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, i := range items {
			result, err := tx.ExecContext(ctx, `UPDATE misty_routine_waits SET state='expired',updated_at=NOW() WHERE user_id=$1 AND invocation_id=$2 AND step_id=$3 AND state='waiting' AND expires_at<=clock_timestamp()`, i.user, i.run, i.step)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE misty_routine_runs SET cancel_requested_at=COALESCE(cancel_requested_at,NOW()) WHERE user_id=$1 AND invocation_id=$2`, i.user, i.run); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET updated_at=NOW() WHERE id=$1 AND user_id=$2`, i.run, i.user); err != nil {
				return err
			}
			if err := routineTimerEventTx(ctx, tx, i.run, i.wait, "timer_expired", "The routine's timed wait expired before it could resume."); err != nil {
				return err
			}
			if err := queueAgentContinuationTx(ctx, tx, i.user, i.run, "runtime.cancel", i.runtime, AgentContinuation{RuntimeID: i.runtime}); err != nil {
				return err
			}
		}
		return nil
	})
}
