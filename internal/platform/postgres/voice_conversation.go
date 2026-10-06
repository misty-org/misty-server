package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// SaveVoiceConversationTurn records speech in the canonical history without
// dispatching a second agent. Only the server's realtime actor calls this;
// neither provider tools nor desktop clients supply ownership or reply text.
// A terminal row can coexist with work already running in the conversation.
func (db *Database) SaveVoiceConversationTurn(ctx context.Context, user, conversation, id, prompt, reply string, interrupted bool, started time.Time) error {
	return db.saveVoiceConversationTurn(ctx, user, conversation, id, prompt, reply, interrupted, started, false)
}

// SaveVoiceConversationFailure records a failed voice turn without dispatching work.
func (db *Database) SaveVoiceConversationFailure(ctx context.Context, user, conversation, id, prompt, message string, started time.Time) error {
	return db.saveVoiceConversationTurn(ctx, user, conversation, id, prompt, message, true, started, true)
}

func (db *Database) saveVoiceConversationTurn(ctx context.Context, user, conversation, id, prompt, reply string, interrupted bool, started time.Time, failed bool) error {
	if prompt == "" {
		return nil
	}
	if id == "" || len(id) > 180 || len(prompt) > 16000 || len(reply) > 32000 {
		return ErrSpaceInvalid
	}
	request, _ := json.Marshal(map[string]any{"prompt": prompt, "trigger": "message", "voice": true, "interrupted": interrupted})
	state := "completed"
	payload, _ := json.Marshal(map[string]any{"text": reply, "interrupted": interrupted})
	if failed {
		state = "failed"
		payload, _ = json.Marshal(map[string]any{"error": reply, "state": state, "code": "voice_session_failed"})
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(user), func(tx *sql.Tx) error {
		var owned string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM misty_ask_conversations WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE`, conversation, user).Scan(&owned); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO ai_invocations(id,user_id,conversation_id,surface_id,mode,trigger_kind,state,idempotency_key,request_payload,created_at,expires_at) VALUES($1,$2,$3,'companion','companion','message',$6,$1,$4,$5,NOW()) ON CONFLICT(user_id,idempotency_key) DO NOTHING`, id, user, conversation, request, started, state)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			// Only this server-created voice record may receive its final speech
			// receipt. Never mutate another invocation or move it between owners.
			var existing string
			if err = tx.QueryRowContext(ctx, `SELECT id FROM ai_invocations WHERE id=$1 AND user_id=$2 AND conversation_id=$3 AND request_payload->>'voice'='true' AND request_payload->>'prompt'=$4`, id, user, conversation, prompt).Scan(&existing); err != nil {
				return err
			}
		}
		sequence, kind := 1, "assistant.status"
		receipt := id + ":input"
		if reply != "" {
			sequence, kind = 2, "assistant.message"
			receipt = id + ":reply"
		}
		if failed {
			sequence, kind, receipt = 2, "invocation.failed", id+":failure"
		}
		inserted, err := tx.ExecContext(ctx, `INSERT INTO ai_invocation_events(invocation_id,sequence,event_type,payload,native_resulting_state,receipt_key) VALUES($1,$2,$3,$4,$6,$5) ON CONFLICT(invocation_id,sequence) DO NOTHING`, id, sequence, kind, payload, receipt, state)
		if err != nil {
			return err
		}
		if failed {
			n, e := inserted.RowsAffected()
			if e != nil {
				return e
			}
			// A later failure cannot overwrite an already persisted successful reply.
			if n > 0 {
				if _, e = tx.ExecContext(ctx, `UPDATE ai_invocations SET state='failed',updated_at=NOW() WHERE id=$1 AND user_id=$2`, id, user); e != nil {
					return e
				}
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE misty_ask_conversations SET updated_at=NOW(),title=CASE WHEN title='' OR title='New conversation' THEN left($3,56) ELSE title END WHERE id=$1 AND user_id=$2`, conversation, user, prompt)
		return err
	})
}
