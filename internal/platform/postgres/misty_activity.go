package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// MistyActivity exposes only the caller's execution journal, even in a shared Space.
func (db *Database) MistyActivity(ctx context.Context, userID, spaceID string) (json.RawMessage, error) {
	var result json.RawMessage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := requireSpaceAppTx(ctx, tx, spaceID, "agents"); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `WITH entries AS (
   SELECT i.id, 'invocation' AS kind, i.state, COALESCE(i.conversation_id,'') AS conversation_id,
    COALESCE(i.agent_run_id,'') AS run_id, '' AS parent_run_id, 0 AS delegation_depth,
    LEFT(COALESCE(i.request_payload->>'prompt','Misty task'),240) AS title, i.updated_at,
    LEFT(COALESCE((SELECT string_agg(payload->>'delta','' ORDER BY sequence) FROM ai_invocation_events WHERE invocation_id=i.id AND event_type='response.delta'),''),8000) AS result,
    COALESCE((SELECT jsonb_agg(e.payload ORDER BY e.sequence) FROM
      (SELECT sequence,payload FROM ai_invocation_events WHERE invocation_id=i.id
       AND event_type IN ('assistant.status','tool.started','tool.completed','tool.failed','approval.required','artifact.proposed','invocation.failed') ORDER BY sequence DESC LIMIT 30) e),'[]'::jsonb) AS events
   FROM ai_invocations i WHERE i.user_id=$1 AND i.space_id=$2
   UNION ALL
   SELECT r.id,'run',r.state,COALESCE(r.input->>'ai_conversation_id',''),r.id,COALESCE(r.parent_run_id,NULLIF(r.input->>'parent_invocation_id',''),''),r.delegation_depth,
    LEFT(COALESCE(r.input->>'instruction',r.input->>'prompt','Delegated task'),240),r.updated_at,LEFT(r.result::text,8000),'[]'::jsonb
   FROM space_runs r WHERE r.owner_user_id=$1 AND r.space_id=$2 AND (r.parent_run_id IS NOT NULL OR NULLIF(r.input->>'parent_invocation_id','') IS NOT NULL)
  ) SELECT COALESCE(jsonb_agg(to_jsonb(page) ORDER BY updated_at DESC),'[]'::jsonb)
    FROM (SELECT * FROM entries ORDER BY updated_at DESC LIMIT 100) page`, userID, spaceID).Scan(&result)
	})
	return result, err
}
