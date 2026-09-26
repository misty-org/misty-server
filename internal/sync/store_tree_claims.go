package browsersync

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// ClaimBrowserSyncTree moves the sender's single driver seat onto a device
// tree. The last explicit claim wins: a displaced driver learns from the tree
// roster and shows its choose screen. A claim answering a remote control
// request is consumed without effect once that request is stale.
func (db *Store) ClaimBrowserSyncTree(ctx context.Context, userID string, c SyncTreeClaim) (*SyncReceipt, error) {
	if !c.Valid() {
		return nil, ErrSyncInvalid
	}
	s := syncSigned{c.WorkspaceID, c.OperationID, c.DeviceID, c.DeviceCounter, c.KeyEpoch, c.SigningBytes(), c.Signature}
	w, existing, err := db.beginSignedWrite(ctx, userID, s)
	if err != nil || existing != nil {
		return existing, err
	}
	defer w.tx.Rollback()
	if !w.fullSync {
		return w.discard(ctx, s, SyncDiscardPaused)
	}
	var stale bool
	err = w.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_control_requests r JOIN browser_sync_devices d USING(workspace_id,device_id)
 WHERE r.workspace_id=$1 AND r.operation_id=$2 AND (r.device_id<>$3 OR r.expires_at<=clock_timestamp() OR d.activation_request IS DISTINCT FROM r.operation_id OR r.tree_id IS DISTINCT FROM $4::uuid))`,
		c.WorkspaceID, c.OperationID, c.DeviceID, c.TreeID).Scan(&stale)
	if err != nil {
		return nil, err
	}
	if stale {
		return w.discard(ctx, s, SyncDiscardStaleClaim)
	}
	// Lock the target and the claimant's current seat in a stable order.
	rows, err := w.tx.QueryContext(ctx, `SELECT tree_id FROM browser_sync_trees WHERE workspace_id=$1 AND (tree_id=$2 OR driver_device_id=$3) ORDER BY tree_id FOR UPDATE`, c.WorkspaceID, c.TreeID, c.DeviceID)
	if err != nil {
		return nil, err
	}
	found := false
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		found = found || id == c.TreeID
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSyncForbidden
	}
	if _, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_trees SET driver_device_id=NULL,driver_epoch=NULL,driver_seen_at=NULL WHERE workspace_id=$1 AND driver_device_id=$2 AND tree_id<>$3`, c.WorkspaceID, c.DeviceID, c.TreeID); err != nil {
		return nil, err
	}
	if _, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_trees SET driver_device_id=$3,driver_epoch=$4,driver_seen_at=clock_timestamp() WHERE workspace_id=$1 AND tree_id=$2`, c.WorkspaceID, c.TreeID, c.DeviceID, c.OperationID); err != nil {
		return nil, err
	}
	if _, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_devices SET activation_request=NULL,activation_expires_at=NULL,activation_tree_id=NULL WHERE workspace_id=$1 AND device_id=$2 AND activation_request=$3`, c.WorkspaceID, c.DeviceID, c.OperationID); err != nil {
		return nil, err
	}
	if err = w.consume(ctx, s, w.head, false); err != nil {
		return nil, err
	}
	if err = notifySync(ctx, w.tx, userID, c.WorkspaceID, "browser-presence"); err != nil {
		return nil, err
	}
	if err = w.tx.Commit(); err != nil {
		return nil, err
	}
	return &SyncReceipt{OperationID: c.OperationID, Sequence: w.head}, nil
}

// ensureBrowserSyncTrees gives a newly created or enrolled device its own
// tree, driven by itself, and makes sure the workspace's shared tree exists.
func ensureBrowserSyncTrees(ctx context.Context, tx *sql.Tx, workspace, device string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_trees(workspace_id,tree_id) VALUES($1,$1) ON CONFLICT DO NOTHING`, workspace); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_trees(workspace_id,tree_id,driver_device_id,driver_epoch,driver_seen_at)
 SELECT $1,$2,$2,$3,clock_timestamp() WHERE NOT EXISTS(SELECT 1 FROM browser_sync_trees WHERE workspace_id=$1 AND driver_device_id=$2)
 ON CONFLICT DO NOTHING`, workspace, device, uuid.NewString())
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_trees(workspace_id,tree_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, workspace, device)
	return err
}

// EnableBrowserSyncTreeMode refuses legacy clients from then on; the log
// keeps carrying account-wide credentials for tree-protocol clients. It is
// idempotent and is called when the first tree-protocol client connects.
func (db *Store) EnableBrowserSyncTreeMode(ctx context.Context, userID, workspace string) error {
	result, err := db.Conn.ExecContext(ctx, `UPDATE browser_sync_workspaces SET tree_mode=true WHERE user_id=$1 AND workspace_id=$2`, userID, workspace)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrSyncForbidden
	}
	return nil
}

func (db *Store) BrowserSyncTreeMode(ctx context.Context, userID, workspace string) (bool, error) {
	var mode bool
	err := db.Conn.QueryRowContext(ctx, `SELECT tree_mode FROM browser_sync_workspaces WHERE user_id=$1 AND workspace_id=$2`, userID, workspace).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrSyncForbidden
	}
	return mode, err
}

// TouchBrowserSyncDriver refreshes liveness for the tree a connected device
// drives. It never claims a tree.
func (db *Store) TouchBrowserSyncDriver(ctx context.Context, i SyncConnectionIdentity) error {
	_, err := db.Conn.ExecContext(ctx, `UPDATE browser_sync_trees SET driver_seen_at=clock_timestamp() WHERE workspace_id=$1 AND driver_device_id=$2`, i.WorkspaceID, i.DeviceID)
	return err
}
