package browsersync

import (
	"context"
	"database/sql"
	"errors"
	"github.com/kannachi323/misty/server/internal/cloudusage"

	"github.com/google/uuid"
)

// ClaimBrowserSyncWorkspace moves the sender's single driver seat onto a device
// workspace. The last explicit claim wins: a displaced driver learns from the workspace
// roster and shows its choose screen. A claim answering a remote control
// request is consumed without effect once that request is stale.
func (db *Store) ClaimBrowserSyncWorkspace(ctx context.Context, userID string, c SyncWorkspaceClaim) (*SyncReceipt, error) {
	if !c.Valid() {
		return nil, ErrSyncInvalid
	}
	s := syncSigned{c.VaultID, c.OperationID, c.DeviceID, c.DeviceCounter, c.KeyEpoch, c.SigningBytes(), c.Signature}
	w, existing, err := db.beginSignedWrite(ctx, userID, s)
	if err != nil || existing != nil {
		return existing, err
	}
	defer w.tx.Rollback()
	if !w.fullSync {
		return w.discard(ctx, s, SyncDiscardPaused)
	}
	var stale bool
	err = w.tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_control_requests r JOIN browser_sync_devices d USING(vault_id,device_id)
 WHERE r.vault_id=$1 AND r.operation_id=$2 AND (r.device_id<>$3 OR r.expires_at<=clock_timestamp() OR d.activation_request IS DISTINCT FROM r.operation_id OR r.workspace_id IS DISTINCT FROM $4::uuid))`,
		c.VaultID, c.OperationID, c.DeviceID, c.WorkspaceID).Scan(&stale)
	if err != nil {
		return nil, err
	}
	if stale {
		return w.discard(ctx, s, SyncDiscardStaleClaim)
	}
	// Lock the target and the claimant's current seat in a stable order.
	rows, err := w.tx.QueryContext(ctx, `SELECT workspace_id FROM browser_sync_workspaces WHERE vault_id=$1 AND (workspace_id=$2 OR driver_device_id=$3) ORDER BY workspace_id FOR UPDATE`, c.VaultID, c.WorkspaceID, c.DeviceID)
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
		found = found || id == c.WorkspaceID
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSyncForbidden
	}
	if _, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_workspaces SET driver_device_id=NULL,driver_epoch=NULL,driver_seen_at=NULL WHERE vault_id=$1 AND driver_device_id=$2 AND workspace_id<>$3`, c.VaultID, c.DeviceID, c.WorkspaceID); err != nil {
		return nil, err
	}
	if _, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_workspaces SET driver_device_id=$3,driver_epoch=$4,driver_seen_at=clock_timestamp() WHERE vault_id=$1 AND workspace_id=$2`, c.VaultID, c.WorkspaceID, c.DeviceID, c.OperationID); err != nil {
		return nil, err
	}
	if _, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_devices SET activation_request=NULL,activation_expires_at=NULL,activation_workspace_id=NULL WHERE vault_id=$1 AND device_id=$2 AND activation_request=$3`, c.VaultID, c.DeviceID, c.OperationID); err != nil {
		return nil, err
	}
	if err = w.consume(ctx, s, w.head, false); err != nil {
		return nil, err
	}
	if err = notifySync(ctx, w.tx, userID, c.VaultID, "browser-presence"); err != nil {
		return nil, err
	}
	if err = cloudusage.Commit(ctx, w.tx); err != nil {
		return nil, err
	}
	return &SyncReceipt{OperationID: c.OperationID, Sequence: w.head}, nil
}

// ensureBrowserSyncWorkspaces gives a newly created or enrolled device its own
// workspace, driven by itself, and makes sure the vault's shared workspace exists.
func ensureBrowserSyncWorkspaces(ctx context.Context, tx *sql.Tx, vault, device string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_workspaces(vault_id,workspace_id) VALUES($1,$1) ON CONFLICT DO NOTHING`, vault); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_workspaces(vault_id,workspace_id,driver_device_id,driver_epoch,driver_seen_at)
 SELECT $1,$2,$2,$3,clock_timestamp() WHERE NOT EXISTS(SELECT 1 FROM browser_sync_workspaces WHERE vault_id=$1 AND driver_device_id=$2)
 ON CONFLICT DO NOTHING`, vault, device, uuid.NewString())
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_workspaces(vault_id,workspace_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, vault, device)
	return err
}

// EnableBrowserSyncWorkspaceMode refuses legacy clients from then on; the log
// keeps carrying account-wide credentials for workspace-protocol clients. It is
// idempotent and is called when the first workspace-protocol client connects.
func (db *Store) EnableBrowserSyncWorkspaceMode(ctx context.Context, userID, vault string) error {
	result, err := db.Conn.ExecContext(ctx, `UPDATE browser_sync_vaults SET workspace_mode=true WHERE user_id=$1 AND vault_id=$2`, userID, vault)
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

func (db *Store) BrowserSyncWorkspaceMode(ctx context.Context, userID, vault string) (bool, error) {
	var mode bool
	err := db.Conn.QueryRowContext(ctx, `SELECT workspace_mode FROM browser_sync_vaults WHERE user_id=$1 AND vault_id=$2`, userID, vault).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrSyncForbidden
	}
	return mode, err
}
