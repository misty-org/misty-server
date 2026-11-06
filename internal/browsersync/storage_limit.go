package browsersync

import (
	"context"
	"database/sql"
	"errors"

	"github.com/misty-org/misty-server/internal/platform/db"
)

// requireSyncStorageRoom applies the account's storage limit (Library and
// vault together) to a sync write. Accounts without a limit are not measured.
func requireSyncStorageRoom(ctx context.Context, tx *sql.Tx, userID string, adding int64) error {
	err := db.RequireAccountStorageRoomTx(ctx, tx, userID, adding)
	if errors.Is(err, db.ErrLibraryQuota) {
		return ErrSyncStorageLimit
	}
	return err
}

// requireSyncWritable refuses every sync write of a closing account, which is
// read-only until it is deleted (db.ErrAccountClosing, answered as
// account_closing). Pulls still work, so a session keeps what the
// account holds; a refused write stays queued on that computer.
func requireSyncWritable(ctx context.Context, tx *sql.Tx, userID string) error {
	return db.RequireAccountWritableTx(ctx, tx, userID)
}
