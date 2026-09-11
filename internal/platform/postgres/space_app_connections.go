package db

import (
	"context"
	"database/sql"
)

func (db *Database) SpaceAppConnections(ctx context.Context, userID, spaceID, appID string) ([]string, error) {
	ids := []string{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := requireSpaceAppTx(ctx, tx, spaceID, appID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.connection_id FROM space_app_connections c JOIN connected_accounts a ON a.id=c.connection_id AND a.user_id=c.user_id WHERE c.user_id=$1 AND c.space_id=$2 AND c.app_id=$3 AND a.revoked_at IS NULL ORDER BY c.connection_id`, userID, spaceID, appID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

// Selection is always performed by the credential owner, never by a Space manager on their behalf.
func (db *Database) SetSpaceAppConnections(ctx context.Context, userID, spaceID, appID string, ids []string) error {
	if AppAuthorityFromContext(ctx) != nil {
		return ErrAppRuntimeForbidden
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if err := requireSpaceAppTx(ctx, tx, spaceID, appID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "space:connections:"+userID+":"+spaceID+":"+appID); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				return ErrSpaceInvalid
			}
			seen[id] = true
			var owned bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM connected_accounts WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL)`, id, userID).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return ErrAppRuntimeForbidden
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM space_app_connections WHERE user_id=$1 AND space_id=$2 AND app_id=$3`, userID, spaceID, appID); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_app_connections(user_id,space_id,app_id,connection_id) VALUES($1,$2,$3,$4)`, userID, spaceID, appID, id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM app_runtime_sessions WHERE user_id=$1 AND space_id=$2 AND app_id=$3`, userID, spaceID, appID)
		return err
	})
}
