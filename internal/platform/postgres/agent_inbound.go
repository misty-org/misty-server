package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Tasks by email or Slack (roadmap C8). A channel watches the person's own
// connected Gmail or Slack for messages they wrote to Misty; each one starts a
// task whose answer goes back to the same thread.

// Each enabled channel is checked once a minute.
const inboundWorkerDeadline = `SELECT min(next_check_at) FROM agent_inbound_channels WHERE enabled`

var InboundChannels = map[string]bool{"gmail": true, "slack": true}

type InboundChannel struct {
	UserID             string          `json:"-"`
	Channel            string          `json:"channel"`
	Enabled            bool            `json:"enabled"`
	ConnectedAccountID string          `json:"-"`
	Identity           json.RawMessage `json:"identity"`
	WatchAfter         time.Time       `json:"watch_after"`
	LastCheckedAt      *time.Time      `json:"last_checked_at,omitempty"`
	LastError          string          `json:"last_error,omitempty"`
}

const inboundChannelColumns = `user_id,channel,enabled,connected_account_id,identity,watch_after,last_checked_at,last_error`

func scanInboundChannel(row interface{ Scan(...any) error }, out *InboundChannel) error {
	return row.Scan(&out.UserID, &out.Channel, &out.Enabled, &out.ConnectedAccountID, &out.Identity, &out.WatchAfter, &out.LastCheckedAt, &out.LastError)
}

// UserInboundChannels lists the caller's channels.
func (db *Database) UserInboundChannels(ctx context.Context, userID string) ([]InboundChannel, error) {
	items := []InboundChannel{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+inboundChannelColumns+` FROM agent_inbound_channels WHERE user_id=$1 ORDER BY channel`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item InboundChannel
			if err := scanInboundChannel(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// EnableInboundChannel turns a channel on for a connected account. Only
// messages from now on start tasks.
func (db *Database) EnableInboundChannel(ctx context.Context, userID, channel, accountID string, identity json.RawMessage) (*InboundChannel, error) {
	if !InboundChannels[channel] || strings.TrimSpace(accountID) == "" || !json.Valid(identity) {
		return nil, ErrSpaceInvalid
	}
	out := &InboundChannel{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanInboundChannel(tx.QueryRowContext(ctx, `INSERT INTO agent_inbound_channels(user_id,channel,enabled,connected_account_id,identity)
			VALUES($1,$2,TRUE,$3,$4)
			ON CONFLICT(user_id,channel) DO UPDATE SET enabled=TRUE,connected_account_id=EXCLUDED.connected_account_id,
				identity=EXCLUDED.identity,watch_after=CASE WHEN agent_inbound_channels.enabled THEN agent_inbound_channels.watch_after ELSE NOW() END,
				next_check_at=NOW(),last_error='',updated_at=NOW()
			RETURNING `+inboundChannelColumns, userID, channel, accountID, identity), out)
	})
	return out, err
}

// DisableInboundChannel stops watching a channel. Tasks it started finish.
func (db *Database) DisableInboundChannel(ctx context.Context, userID, channel string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_inbound_channels SET enabled=FALSE,updated_at=NOW() WHERE user_id=$1 AND channel=$2`, userID, channel)
		return err
	})
}

// ClaimDueInboundChannels leases channels due for a check, so one server
// checks each.
func (db *Database) ClaimDueInboundChannels(ctx context.Context, now time.Time, limit int) ([]InboundChannel, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	items := []InboundChannel{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `UPDATE agent_inbound_channels SET next_check_at=$1::timestamptz+INTERVAL '1 minute'
			WHERE (user_id,channel) IN (SELECT user_id,channel FROM agent_inbound_channels
				WHERE enabled AND next_check_at<=$1 ORDER BY next_check_at FOR UPDATE SKIP LOCKED LIMIT $2)
			RETURNING `+inboundChannelColumns, now, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item InboundChannel
			if err := scanInboundChannel(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

// FinishInboundCheck records a check. A failure is shown to the person in
// plain words; the channel keeps trying.
func (db *Database) FinishInboundCheck(ctx context.Context, userID, channel, failure string) error {
	if len(failure) > 500 {
		failure = failure[:500]
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_inbound_channels SET last_checked_at=NOW(),last_error=$3,
			updated_at=CASE WHEN last_error<>$3 THEN NOW() ELSE updated_at END WHERE user_id=$1 AND channel=$2`, userID, channel, failure)
		return err
	})
}

// InboundTask is one message that started a task.
type InboundTask struct {
	ID             string          `json:"id"`
	UserID         string          `json:"-"`
	Channel        string          `json:"channel"`
	ExternalID     string          `json:"external_id"`
	ThreadRef      json.RawMessage `json:"thread_ref"`
	InvocationID   string          `json:"invocation_id,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	State          string          `json:"state"`
}

// RecordInboundTask claims a message for one task. False means it already
// started one.
func (db *Database) RecordInboundTask(ctx context.Context, userID, channel, externalID string, threadRef json.RawMessage) (*InboundTask, bool, error) {
	out := &InboundTask{ID: "inbound_" + uuid.NewString(), UserID: userID, Channel: channel, ExternalID: externalID, ThreadRef: threadRef, State: "started"}
	created := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO agent_inbound_tasks(id,user_id,channel,external_id,thread_ref) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT(user_id,channel,external_id) DO NOTHING`, out.ID, userID, channel, externalID, threadRef)
		if err != nil {
			return err
		}
		rows, _ := result.RowsAffected()
		created = rows == 1
		return nil
	})
	return out, created, err
}

// BindInboundTask records the task's conversation and invocation.
func (db *Database) BindInboundTask(ctx context.Context, taskID, conversationID, invocationID string) error {
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_inbound_tasks SET conversation_id=$2,invocation_id=$3 WHERE id=$1`, taskID, conversationID, invocationID)
		return err
	})
}

// InboundTaskByInvocation finds the message an invocation is answering.
func (db *Database) InboundTaskByInvocation(ctx context.Context, invocationID string) (*InboundTask, error) {
	out := &InboundTask{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id,user_id,channel,external_id,thread_ref,COALESCE(invocation_id,''),COALESCE(conversation_id,''),state
			FROM agent_inbound_tasks WHERE invocation_id=$1`, invocationID).
			Scan(&out.ID, &out.UserID, &out.Channel, &out.ExternalID, &out.ThreadRef, &out.InvocationID, &out.ConversationID, &out.State)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSpaceNotFound
	}
	return out, err
}

// FinishInboundTask records whether the answer reached the thread. Only a
// started task can finish, so a retried completion replies once.
func (db *Database) FinishInboundTask(ctx context.Context, taskID, state, failure string) (bool, error) {
	if len(failure) > 500 {
		failure = failure[:500]
	}
	finished := false
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE agent_inbound_tasks SET state=$2,error=$3,replied_at=CASE WHEN $2='replied' THEN NOW() END
			WHERE id=$1 AND state='started'`, taskID, state, failure)
		if err != nil {
			return err
		}
		rows, _ := result.RowsAffected()
		finished = rows == 1
		return nil
	})
	return finished, err
}

// MarkInboundReplyFailed records that a claimed reply didn't reach the thread.
func (db *Database) MarkInboundReplyFailed(ctx context.Context, taskID, failure string) error {
	if len(failure) > 500 {
		failure = failure[:500]
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE agent_inbound_tasks SET state='reply_failed',error=$2,replied_at=NULL WHERE id=$1`, taskID, failure)
		return err
	})
}
