package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (db *Database) FinishSpaceRun(ctx context.Context, runID, state string, result json.RawMessage, errorCode string) (*SpaceRun, error) {
	if state != "completed" && state != "completed_with_errors" && state != "failed" && state != "canceled" && state != "rejected" {
		return nil, ErrSpaceInvalid
	}
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	out := &SpaceRun{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		progress := 0
		if state == "completed" || state == "completed_with_errors" {
			progress = 100
		}
		if err := scanSpaceRun(tx.QueryRowContext(ctx, `UPDATE space_runs SET state=$1,result=$2,outputs=$2,error_code=NULLIF($3,''),error_message=CASE WHEN $1 IN ('failed','completed_with_errors') THEN COALESCE(($2::jsonb)->>'message','Execution failed') ELSE NULL END,progress=$4,completed_at=NOW(),updated_at=NOW()
			WHERE id=$5 AND state IN ('queued','running','cooldown') RETURNING `+spaceRunColumns, state, result, errorCode, progress, runID), out); errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		} else if err != nil {
			return err
		}
		if out.AgentID != "" {
			// The account event trigger publishes Agent completion independently of content.
			return nil
		}
		_, err := recordSpaceEventTx(ctx, tx, out.SpaceID, out.InitiatedByUserID, out.ResourceKind+".run."+state, out.ID, out)
		return err
	})
	return out, err
}
