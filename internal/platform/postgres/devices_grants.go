package db

import (
	"context"
	"database/sql"
	"errors"
)

// AIInvocationContextGrant returns the signed run grant a context was attached
// with. A device inbox grant travels to the sending device, which presents it
// to the destination over the LAN.
func (db *Database) AIInvocationContextGrant(ctx context.Context, userID, contextID string) ([]byte, string, error) {
	var payload []byte
	var signature sql.NullString
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT run_grant_payload,run_grant_signature FROM ai_invocation_contexts
			WHERE id=$1 AND user_id=$2 AND state='attached' AND expires_at>NOW()`, contextID, userID).Scan(&payload, &signature)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrDeviceNotFound
	}
	return payload, signature.String, err
}
