package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type AIInvocationEventRecord struct {
	Sequence  int64
	EventType string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// AIConversationTurnRecord is the user-facing projection of a companion
// invocation. The stored request contains the original prompt, while the
// invocation event stream contains the final reply or durable failure. Agent
// runtime messages are intentionally not used here because their user message
// includes the compiled context/security envelope sent to the model.
type AIConversationTurnRecord struct {
	InvocationID    string
	Prompt          string
	State           string
	Reply           string
	Status          string
	Failure         string
	AgentRunID      string
	AgentState      string
	AgentProgress   int
	AgentError      string
	ResultSpaceID   string
	ResultDrawingID string
	CreatedAt       time.Time
	ReplyAt         time.Time
	// Trigger is what started the turn, such as "scheduled_task"; empty for typed turns.
	Trigger string
}

func (db *Database) AIConversationTurns(ctx context.Context, userID, conversationID string) ([]AIConversationTurnRecord, error) {
	items := []AIConversationTurnRecord{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT invocation.id,
				CASE WHEN invocation.idempotency_key LIKE 'voice-%:%' THEN '' ELSE COALESCE(invocation.request_payload->>'prompt','') END,
				invocation.state,
				COALESCE(reply.payload->>'text',''),
				COALESCE(status.payload->>'text',''),
				COALESCE(failure.payload->>'error',''),
				COALESCE(run.id,''),
				COALESCE(run.state,''),
				COALESCE(run.progress,0),
				COALESCE(run.error_message,''),
				COALESCE(drawing.result->>'space_id',''),
				COALESCE(drawing.result->>'id',''),
				invocation.created_at,
				COALESCE(reply.created_at,failure.created_at,status.created_at,invocation.updated_at),
				COALESCE(invocation.request_payload->>'trigger','')
			FROM ai_invocations AS invocation
			LEFT JOIN space_runs AS run ON run.id=invocation.agent_run_id
			LEFT JOIN LATERAL (
				SELECT action.result
				FROM agent_toolbox_action_journal AS action
				WHERE action.run_id=run.id AND action.tool_name='drawing.create' AND action.state='completed'
				ORDER BY action.created_at DESC LIMIT 1
			) AS drawing ON TRUE
			LEFT JOIN LATERAL (
				SELECT event.payload,event.created_at
				FROM ai_invocation_events AS event
				WHERE event.invocation_id=invocation.id AND event.event_type='assistant.message'
				ORDER BY event.sequence DESC LIMIT 1
			) AS reply ON TRUE
			LEFT JOIN LATERAL (
				SELECT event.payload,event.created_at
				FROM ai_invocation_events AS event
				WHERE event.invocation_id=invocation.id AND event.event_type='assistant.status'
				ORDER BY event.sequence DESC LIMIT 1
			) AS status ON TRUE
			LEFT JOIN LATERAL (
				SELECT event.payload,event.created_at
				FROM ai_invocation_events AS event
				WHERE event.invocation_id=invocation.id AND event.event_type='invocation.failed'
				ORDER BY event.sequence DESC LIMIT 1
			) AS failure ON TRUE
			WHERE invocation.user_id=$1 AND invocation.conversation_id=$2
			ORDER BY invocation.created_at,invocation.id
		`, userID, conversationID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIConversationTurnRecord
			if err := rows.Scan(
				&item.InvocationID, &item.Prompt, &item.State, &item.Reply,
				&item.Status, &item.Failure, &item.AgentRunID, &item.AgentState,
				&item.AgentProgress, &item.AgentError, &item.ResultSpaceID,
				&item.ResultDrawingID, &item.CreatedAt, &item.ReplyAt, &item.Trigger,
			); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) AIInvocationEvents(ctx context.Context, userID, invocationID string, after int64) ([]AIInvocationEventRecord, string, error) {
	items := []AIInvocationEventRecord{}
	state := ""
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT state FROM ai_invocations WHERE id=$1 AND user_id=$2`, invocationID, userID).Scan(&state); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT sequence,event_type,payload,created_at FROM ai_invocation_events
			WHERE invocation_id=$1 AND sequence>$2 ORDER BY sequence
		`, invocationID, after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIInvocationEventRecord
			if err := rows.Scan(&item.Sequence, &item.EventType, &item.Payload, &item.CreatedAt); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrSpaceNotFound
	}
	return items, state, err
}
