package browsersync

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kannachi323/misty/server/internal/platform/db"
)

// requireSyncStorageRoom applies the account's storage limit (Library and
// vault together) to a sync write. Accounts without a limit are not measured.
// Signed workspace operations are counted but never refused: refusing one
// would stall the device's signed counter chain, so the vault can pass its
// limit by what a workspace holds (tabs and spaces), never by records or blobs.
func requireSyncStorageRoom(ctx context.Context, tx *sql.Tx, userID string, adding int64) error {
	err := db.RequireAccountStorageRoomTx(ctx, tx, userID, adding)
	if errors.Is(err, db.ErrLibraryQuota) {
		return ErrSyncStorageLimit
	}
	return err
}

// requireSyncWritable refuses every sync write of a closing account, which is
// read-only until it is deleted (db.ErrAccountClosing, answered as
// account_closing). Pulls and replays still work, so a device keeps what the
// account holds; a refused write stays queued on the device.
func requireSyncWritable(ctx context.Context, tx *sql.Tx, userID string) error {
	return db.RequireAccountWritableTx(ctx, tx, userID)
}
