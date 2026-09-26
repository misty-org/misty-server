package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// MistyActivity exposes only the caller's personal execution journal by default.
// Legacy Space filter arguments are ignored; ownership is always the caller.
func (db *Database) MistyActivity(ctx context.Context, userID, spaceID string, agentIDs ...string) (json.RawMessage, error) {
	if userID == "" {
		return nil, ErrSpaceForbidden
	}
	var result json.RawMessage
	agentID := ""
	if len(agentIDs) > 0 {
		agentID = agentIDs[0]
	}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH entries AS (
   SELECT i.id, 'invocation' AS kind,
    CASE WHEN i.state IN ('running','awaiting_device','awaiting_intervention','awaiting_approval') AND i.request_payload->>'execution_mode' IN ('agent','team') AND NOT EXISTS(SELECT 1 FROM misty_agent_execution_leases l WHERE l.owner_user_id=i.user_id AND l.agent_id=i.request_payload->>'agent_id' AND l.task_id=i.request_payload->>'task_id' AND l.expires_at>NOW()) THEN 'paused' ELSE i.state END AS state,
    COALESCE(i.request_payload->>'agent_id','') AS agent_id, COALESCE(i.conversation_id,'') AS conversation_id,
    COALESCE(i.agent_run_id,'') AS run_id, '' AS parent_run_id, 0 AS delegation_depth,
    LEFT(COALESCE(i.request_payload->>'prompt','Misty task'),240) AS title, i.updated_at,
    LEFT(COALESCE((SELECT string_agg(payload->>'delta','' ORDER BY sequence) FROM ai_invocation_events WHERE invocation_id=i.id AND event_type='response.delta'),''),8000) AS result,
    COALESCE((SELECT jsonb_agg(e.payload ORDER BY e.sequence) FROM
      (SELECT sequence,payload FROM ai_invocation_events WHERE invocation_id=i.id
       AND event_type IN ('assistant.status','tool.started','tool.completed','tool.failed','approval.required','artifact.proposed','invocation.failed') ORDER BY sequence DESC LIMIT 30) e),'[]'::jsonb) AS events
   FROM ai_invocations i WHERE i.user_id=$1 AND ($2='' OR i.request_payload->>'agent_id'=$2)
   UNION ALL
   SELECT r.id,'run',r.state,r.agent_id,COALESCE(r.input->>'ai_conversation_id',''),r.id,COALESCE(r.parent_run_id,NULLIF(r.input->>'parent_invocation_id',''),''),r.delegation_depth,
    LEFT(COALESCE(r.input->>'instruction',r.input->>'prompt','Delegated task'),240),r.updated_at,LEFT(r.result::text,8000),'[]'::jsonb
   FROM space_runs r WHERE r.owner_user_id=$1 AND ($2='' OR r.agent_id=$2) AND (r.parent_run_id IS NOT NULL OR NULLIF(r.input->>'parent_invocation_id','') IS NOT NULL)
  ) SELECT COALESCE(jsonb_agg(to_jsonb(page) ORDER BY updated_at DESC),'[]'::jsonb)
    FROM (SELECT * FROM entries ORDER BY updated_at DESC LIMIT 100) page`, userID, agentID).Scan(&result)
	})
	return result, err
}
