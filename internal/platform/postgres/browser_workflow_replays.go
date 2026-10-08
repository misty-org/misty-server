package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// BrowserWorkflowReplay is the browser actions of a workflow's last successful
// run, with controls named by locator, for replaying without model calls.
type BrowserWorkflowReplay struct {
	MethodVersionID    string          `json:"method_version_id"`
	StartURL           string          `json:"start_url"`
	Calls              json.RawMessage `json:"calls"`
	SourceInvocationID string          `json:"source_invocation_id"`
	RecordedAt         time.Time       `json:"recorded_at"`
}

// BrowserJobRecord is one completed browser device job of a run, in order.
type BrowserJobRecord struct {
	Operation string
	Input     json.RawMessage
	Output    json.RawMessage
}

const maxReplayCallsBytes = 256 << 10

func (db *Database) SaveBrowserWorkflowReplay(ctx context.Context, user string, replay BrowserWorkflowReplay) error {
	if user == "" || replay.MethodVersionID == "" || len(replay.MethodVersionID) > 200 || len(replay.StartURL) > 4096 ||
		len(replay.Calls) == 0 || len(replay.Calls) > maxReplayCallsBytes || !json.Valid(replay.Calls) {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO browser_workflow_replays(owner_user_id,method_version_id,start_url,calls,source_invocation_id,recorded_at)
 VALUES($1,$2,$3,$4,$5,now())
 ON CONFLICT(owner_user_id,method_version_id) DO UPDATE SET start_url=EXCLUDED.start_url,calls=EXCLUDED.calls,source_invocation_id=EXCLUDED.source_invocation_id,recorded_at=now()`,
			user, replay.MethodVersionID, replay.StartURL, []byte(replay.Calls), replay.SourceInvocationID)
		return err
	})
}

// BrowserWorkflowReplayFor returns the saved replay, or nil when there is none.
func (db *Database) BrowserWorkflowReplayFor(ctx context.Context, user, methodVersionID string) (*BrowserWorkflowReplay, error) {
	if user == "" || methodVersionID == "" {
		return nil, nil
	}
	var replay BrowserWorkflowReplay
	var calls []byte
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT method_version_id,start_url,calls,source_invocation_id,recorded_at FROM browser_workflow_replays WHERE owner_user_id=$1 AND method_version_id=$2`,
			user, methodVersionID).Scan(&replay.MethodVersionID, &replay.StartURL, &calls, &replay.SourceInvocationID, &replay.RecordedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	replay.Calls = calls
	return &replay, nil
}

func (db *Database) DeleteBrowserWorkflowReplay(ctx context.Context, user, methodVersionID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM browser_workflow_replays WHERE owner_user_id=$1 AND method_version_id=$2`, user, methodVersionID)
		return err
	})
}

// CompletedBrowserJobs lists an invocation's completed browser jobs in the
// order they ran.
func (db *Database) CompletedBrowserJobs(ctx context.Context, user, invocationID string) ([]BrowserJobRecord, error) {
	out := []BrowserJobRecord{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT operation,input,COALESCE(output,'{}'::jsonb) FROM workflow_device_node_jobs
 WHERE user_id=$1 AND invocation_id=$2 AND state='completed' AND operation LIKE 'browser.%' ORDER BY COALESCE(execution_started_at,created_at),created_at`, user, invocationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var record BrowserJobRecord
			var input, output []byte
			if err := rows.Scan(&record.Operation, &input, &output); err != nil {
				return err
			}
			record.Input, record.Output = input, output
			out = append(out, record)
		}
		return rows.Err()
	})
	return out, err
}

// InvocationToolNames lists the tools an invocation started, in order.
func (db *Database) InvocationToolNames(ctx context.Context, user, invocationID string) ([]string, error) {
	out := []string{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT COALESCE(e.payload->>'toolName','') FROM ai_invocation_events e JOIN ai_invocations i ON i.id=e.invocation_id
 WHERE i.id=$1 AND i.user_id=$2 AND e.event_type='tool.started' ORDER BY e.sequence`, invocationID, user)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, name)
			}
		}
		return rows.Err()
	})
	return out, err
}
