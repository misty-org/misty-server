package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

type AISteeringMessage struct {
	Sequence int64  `json:"sequence"`
	Text     string `json:"text"`
}
type AISteeringBatch struct {
	Messages []AISteeringMessage `json:"messages"`
	Closed   bool                `json:"closed"`
}

// TakeAIInvocationSteering claims an immutable batch at a durable runtime boundary.
// Closing the intake and checking for new messages share the invocation row lock
// with admission, so a last-second follow-up cannot be silently dropped.
func (db *Database) TakeAIInvocationSteering(ctx context.Context, userID, runID, key string, closeIntake bool) (AISteeringBatch, error) {
	out := AISteeringBatch{Messages: []AISteeringMessage{}}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var invocationID, state string
		if err := tx.QueryRowContext(ctx, `SELECT id,state FROM ai_invocations WHERE user_id=$1 AND (id=$2 OR agent_run_id=$2) FOR UPDATE`, userID, runID).Scan(&invocationID, &state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var previous []byte
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM ai_invocation_events WHERE invocation_id=$1 AND receipt_key=$2`, invocationID, "steering-boundary:"+key).Scan(&previous); err == nil {
			return json.Unmarshal(previous, &out)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if state == "completed" || state == "failed" || state == "canceled" {
			return ErrSpaceConflict
		}
		rows, err := tx.QueryContext(ctx, `SELECT sequence,payload->>'text' FROM ai_invocation_events WHERE invocation_id=$1 AND event_type='user.steering' AND sequence>COALESCE((SELECT MAX((payload->>'through')::bigint) FROM ai_invocation_events WHERE invocation_id=$1 AND event_type='steering.received'),0) ORDER BY sequence`, invocationID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var m AISteeringMessage
			if err := rows.Scan(&m.Sequence, &m.Text); err != nil {
				rows.Close()
				return err
			}
			out.Messages = append(out.Messages, m)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		out.Closed = closeIntake && len(out.Messages) == 0
		through := int64(0)
		if len(out.Messages) > 0 {
			through = out.Messages[len(out.Messages)-1].Sequence
		}
		eventType := "steering.received"
		if out.Closed {
			eventType = "steering.closed"
		}
		var sequence int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM ai_invocation_events WHERE invocation_id=$1`, invocationID).Scan(&sequence); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"id": strconv.FormatInt(sequence, 10), "type": eventType, "messages": out.Messages, "closed": out.Closed, "through": through})
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,receipt_key) VALUES($1,$2,$3,$4,$5)`, invocationID, sequence, eventType, payload, "steering-boundary:"+key)
		return err
	})
	return out, err
}

// AIConversationReview returns only user input and the live review, never tool traces.
func (db *Database) AIConversationReview(ctx context.Context, userID, invocationID string) ([]AIInvocationEventRecord, json.RawMessage, error) {
	items := []AIInvocationEventRecord{}
	var artifactID string
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT e.sequence,e.event_type,e.payload,e.created_at
            FROM ai_invocation_events e JOIN ai_invocations i ON i.id=e.invocation_id
            WHERE i.id=$1 AND i.user_id=$2 AND e.event_type='user.steering' ORDER BY e.sequence`, invocationID, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var item AIInvocationEventRecord
			if err := rows.Scan(&item.Sequence, &item.EventType, &item.Payload, &item.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT id FROM ai_artifacts WHERE invocation_id=$1 AND user_id=$2 AND state='proposed' AND expires_at>NOW() ORDER BY created_at DESC LIMIT 1`, invocationID, userID).Scan(&artifactID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil || artifactID == "" {
		return items, nil, err
	}
	artifact, err := db.AIArtifactByID(ctx, userID, artifactID)
	return items, artifact, err
}
