package db

import (
	"context"
	"database/sql"
)

// deleteSpaceVaultsTx deletes the encrypted sync vaults people hold for a
// Space: one member's when they leave or are removed, every member's when the
// Space is deleted (an empty userID). The vault's events, records, devices
// and blobs go with it; the server only ever held their ciphertext.
func deleteSpaceVaultsTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM browser_sync_vaults WHERE space_id=$1 AND ($2='' OR user_id=$2) AND space_key_envelope IS NOT NULL`, spaceID, userID)
	return err
}
