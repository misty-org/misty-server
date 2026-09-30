package browsersync

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"strings"
	"unicode/utf8"
)

type SyncDeviceControl struct {
	DeviceID       string  `json:"device_id"`
	DisplayName    *string `json:"display_name,omitempty"`
	Platform       *string `json:"platform,omitempty"`
	ControlVersion *int    `json:"control_version,omitempty"`
	FullSync       *bool   `json:"full_sync,omitempty"`
	Activate       bool    `json:"activate,omitempty"`
	OSVersion      *string `json:"os_version,omitempty"`
	// WorkspaceID names the workspace an activated device should claim. In workspace mode it
	// defaults to the device's own workspace.
	WorkspaceID *string `json:"workspace_id,omitempty"`
}

// Device controls are account-owned. Activation is a short-lived request to
// the target; only its signed native mutation can actually take over.
func (db *Store) ControlBrowserSyncDevice(ctx context.Context, user string, c SyncDeviceControl) (string, error) {
	if !validSyncID(c.DeviceID) || (c.DisplayName != nil && (!utf8.ValidString(*c.DisplayName) || len(*c.DisplayName) > 160 || strings.TrimSpace(*c.DisplayName) == "")) || (c.Platform != nil && len(*c.Platform) > 32) || (c.ControlVersion != nil && *c.ControlVersion != 1) || (c.OSVersion != nil && (!utf8.ValidString(*c.OSVersion) || len(*c.OSVersion) > 64)) || (c.WorkspaceID != nil && (!validSyncID(*c.WorkspaceID) || !c.Activate)) {
		return "", ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var vault string
	var version int
	var full, workspaceMode bool
	err = tx.QueryRowContext(ctx, `SELECT d.vault_id,d.control_version,d.full_sync,w.workspace_mode FROM browser_sync_devices d JOIN browser_sync_vaults w USING(vault_id) WHERE w.user_id=$1 AND d.device_id=$2 AND d.revoked_at IS NULL FOR UPDATE OF d`, user, c.DeviceID).Scan(&vault, &version, &full, &workspaceMode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSyncForbidden
	}
	if err != nil {
		return "", err
	}
	if c.FullSync != nil && version < 1 {
		return "", ErrSyncInvalid
	}
	var request any
	if c.Activate {
		if version < 1 || !full || (c.FullSync != nil && !*c.FullSync) {
			return "", ErrSyncInvalid
		}
	}
	if c.Activate || c.FullSync != nil {
		var online bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_connections WHERE vault_id=$1 AND device_id=$2 AND expires_at>clock_timestamp())`, vault, c.DeviceID).Scan(&online)
		if err != nil {
			return "", err
		}
		if !online {
			return "", ErrSyncInvalid
		}
	}
	var workspace any
	if c.Activate && workspaceMode {
		target := c.DeviceID
		if c.WorkspaceID != nil {
			target = *c.WorkspaceID
		}
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM browser_sync_workspaces WHERE vault_id=$1 AND workspace_id=$2 AND workspace_id<>vault_id)`, vault, target).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return "", ErrSyncInvalid
		}
		workspace = target
	} else if c.WorkspaceID != nil {
		return "", ErrSyncInvalid
	}
	if c.Activate {
		request = uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_control_requests(vault_id,device_id,operation_id,workspace_id) VALUES($1,$2,$3,$4)`, vault, c.DeviceID, request, workspace)
		if err != nil {
			return "", err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE browser_sync_devices SET display_name=COALESCE($3,display_name),platform=COALESCE($4,platform),control_version=COALESCE($5,control_version),full_sync=COALESCE($6,full_sync),activation_request=CASE WHEN $7::uuid IS NOT NULL THEN $7::uuid WHEN $6::boolean IS NOT NULL THEN NULL ELSE activation_request END,activation_expires_at=CASE WHEN $7::uuid IS NOT NULL THEN clock_timestamp()+interval '30 seconds' WHEN $6::boolean IS NOT NULL THEN NULL ELSE activation_expires_at END,activation_workspace_id=CASE WHEN $7::uuid IS NOT NULL THEN $9::uuid WHEN $6::boolean IS NOT NULL THEN NULL ELSE activation_workspace_id END,os_version=COALESCE($8,os_version) WHERE vault_id=$1 AND device_id=$2`, vault, c.DeviceID, c.DisplayName, c.Platform, c.ControlVersion, c.FullSync, request, c.OSVersion, workspace)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	_ = db.NotifyBrowserSyncPresence(ctx, SyncConnectionIdentity{UserID: user, VaultID: vault, DeviceID: c.DeviceID})
	id, _ := request.(string)
	return id, nil
}
