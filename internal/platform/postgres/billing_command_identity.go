package db

import (
	"context"
	"database/sql"
)

func (db *Database) BillingCommandForRun(ctx context.Context, user, run string) (string, error) {
	var root, trigger string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH RECURSIVE chain AS (
 SELECT id,parent_run_id,input,trigger_kind,ARRAY[id] path FROM space_runs WHERE id=$2 AND requesting_member_id=$1
 UNION ALL SELECT p.id,p.parent_run_id,p.input,p.trigger_kind,c.path||p.id FROM space_runs p JOIN chain c ON p.id=c.parent_run_id
 WHERE p.requesting_member_id=$1 AND NOT p.id=ANY(c.path) AND cardinality(c.path)<32)
 SELECT COALESCE(i.id,c.id),COALESCE(i.trigger_kind,c.trigger_kind) FROM chain c
 LEFT JOIN ai_invocations i ON i.id=COALESCE(NULLIF(c.input->>'parent_invocation_id',''),NULLIF(c.input->>'ai_invocation_id','')) AND i.user_id=$1
 ORDER BY cardinality(c.path) DESC LIMIT 1`, user, run).Scan(&root, &trigger)
	})
	if err != nil {
		return "", err
	}
	if trigger == "schedule" {
		return "agent-job:" + root, nil
	}
	return "agent-runtime:" + root, nil
}
