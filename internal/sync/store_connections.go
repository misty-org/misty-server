package browsersync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kannachi323/misty/server/internal/platform/transport"
	"time"
)

type SyncDevice struct {
	SyncDeviceGrant
	LastCounter         int64      `json:"last_counter"`
	RevokedAt           *time.Time `json:"revoked_at"`
	DisplayName         string     `json:"display_name"`
	Platform            string     `json:"platform"`
	ControlVersion      int        `json:"control_version"`
	FullSync            bool       `json:"full_sync"`
	ActivationRequest   *string    `json:"activation_request"`
	ActivationExpiresAt int64      `json:"activation_expires_at"`
	OSVersion           string     `json:"os_version"`
	ActivationTreeID    *string    `json:"activation_tree_id"`
	TreeLastCounter     int64      `json:"tree_last_counter"`
	// Enrollment time, so clients number unnamed devices stably.
	CreatedAt time.Time `json:"created_at"`
}
type SyncConnectionIdentity struct {
	UserID, WorkspaceID, DeviceID string
	PublicKey                     []byte
}
type SyncPresence struct {
	DeviceID        string     `json:"device_id"`
	Online          bool       `json:"online"`
	Ready           bool       `json:"ready"`
	AppliedSequence int64      `json:"applied_sequence"`
	LastSeenAt      *time.Time `json:"last_seen_at"`
	Active          bool       `json:"active"`
}

func (db *Store) BrowserSyncDevices(ctx context.Context, userID, workspaceID string) ([]SyncDevice, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT d.device_id,d.public_key,d.grant_epoch,d.grant_signature,d.last_counter,d.revoked_at,d.display_name,d.platform,d.control_version,d.full_sync,d.activation_request,COALESCE((EXTRACT(EPOCH FROM d.activation_expires_at)*1000)::bigint,0),d.os_version,d.activation_tree_id,d.tree_last_counter,d.created_at FROM browser_sync_devices d JOIN browser_sync_workspaces w USING(workspace_id) WHERE w.user_id=$1 AND w.workspace_id=$2 ORDER BY d.created_at,d.device_id`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncDevice{}
	for rows.Next() {
		d := SyncDevice{}
		d.WorkspaceID = workspaceID
		if err = rows.Scan(&d.DeviceID, &d.PublicKey, &d.KeyEpoch, &d.Signature, &d.LastCounter, &d.RevokedAt, &d.DisplayName, &d.Platform, &d.ControlVersion, &d.FullSync, &d.ActivationRequest, &d.ActivationExpiresAt, &d.OSVersion, &d.ActivationTreeID, &d.TreeLastCounter, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (db *Store) CreateBrowserSyncTicket(ctx context.Context, userID, workspaceID, deviceID, hash string) error {
	if !validSyncID(workspaceID) || !validSyncID(deviceID) || len(hash) != 64 {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT true FROM browser_sync_devices d JOIN browser_sync_workspaces w USING(workspace_id) WHERE w.user_id=$1 AND w.workspace_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL FOR UPDATE OF d`, userID, workspaceID, deviceID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSyncForbidden
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM browser_sync_tickets WHERE workspace_id=$1 AND device_id=$2 AND expires_at<=clock_timestamp()`, workspaceID, deviceID)
	if err != nil {
		return err
	}
	var pending int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM browser_sync_tickets WHERE workspace_id=$1 AND device_id=$2`, workspaceID, deviceID).Scan(&pending)
	if err != nil {
		return err
	}
	if pending >= 8 {
		return ErrSyncInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_tickets(token_hash,workspace_id,device_id) VALUES($1,$2,$3)`, hash, workspaceID, deviceID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (db *Store) ConsumeBrowserSyncTicket(ctx context.Context, hash string) (*SyncConnectionIdentity, error) {
	var i SyncConnectionIdentity
	err := db.Conn.QueryRowContext(ctx, `WITH consumed AS (DELETE FROM browser_sync_tickets WHERE token_hash=$1 AND expires_at>clock_timestamp() RETURNING workspace_id,device_id) SELECT w.user_id,c.workspace_id,c.device_id,d.public_key FROM consumed c JOIN browser_sync_workspaces w USING(workspace_id) JOIN browser_sync_devices d ON d.workspace_id=c.workspace_id AND d.device_id=c.device_id WHERE d.revoked_at IS NULL`, hash).Scan(&i.UserID, &i.WorkspaceID, &i.DeviceID, &i.PublicKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	return &i, err
}
func (db *Store) BrowserSyncHeartbeat(ctx context.Context, i SyncConnectionIdentity, connectionID string, applied int64, ready bool, epochs ...string) error {
	if !validSyncID(connectionID) || applied < 0 || applied > SyncMaxCounter {
		return ErrSyncInvalid
	}
	result, err := db.Conn.ExecContext(ctx, `INSERT INTO browser_sync_connections(connection_id,workspace_id,device_id,ready,applied_sequence)
 SELECT $4,w.workspace_id,d.device_id,$6,$5 FROM browser_sync_workspaces w JOIN browser_sync_devices d USING(workspace_id)
 WHERE w.user_id=$1 AND w.workspace_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL AND $5<=w.head_sequence
 ON CONFLICT(connection_id) DO UPDATE SET ready=EXCLUDED.ready,applied_sequence=EXCLUDED.applied_sequence,expires_at=clock_timestamp()+interval '45 seconds',last_seen_at=clock_timestamp()
 WHERE browser_sync_connections.workspace_id=EXCLUDED.workspace_id AND browser_sync_connections.device_id=EXCLUDED.device_id`, i.UserID, i.WorkspaceID, i.DeviceID, connectionID, applied, ready)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSyncForbidden
	}
	if len(epochs) > 0 && epochs[0] != "" {
		if !validSyncID(epochs[0]) {
			return ErrSyncInvalid
		}
		_, err = db.Conn.ExecContext(ctx, `UPDATE browser_sync_workspaces SET active_seen_at=clock_timestamp() WHERE user_id=$1 AND workspace_id=$2 AND active_device_id=$3 AND active_epoch=$4`, i.UserID, i.WorkspaceID, i.DeviceID, epochs[0])
		if err != nil {
			return err
		}
	}
	return nil
}
func (db *Store) BrowserSyncDisconnect(ctx context.Context, i SyncConnectionIdentity, connectionID string) error {
	// Keep last-seen information, while expiring only this particular connection.
	_, err := db.Conn.ExecContext(ctx, `UPDATE browser_sync_connections SET expires_at=clock_timestamp(),ready=false WHERE connection_id=$1 AND workspace_id=$2 AND device_id=$3`, connectionID, i.WorkspaceID, i.DeviceID)
	return err
}
func (db *Store) BrowserSyncPresence(ctx context.Context, userID, workspaceID string) ([]SyncPresence, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT d.device_id,COALESCE(bool_or(c.expires_at>clock_timestamp()),false),COALESCE(bool_or(c.expires_at>clock_timestamp() AND c.ready AND c.applied_sequence>=w.head_sequence),false),COALESCE(max(c.applied_sequence),0),max(c.last_seen_at),COALESCE(w.active_device_id=d.device_id,false)
 FROM browser_sync_devices d JOIN browser_sync_workspaces w USING(workspace_id) LEFT JOIN browser_sync_connections c ON c.workspace_id=d.workspace_id AND c.device_id=d.device_id
 WHERE w.user_id=$1 AND w.workspace_id=$2 AND d.revoked_at IS NULL GROUP BY d.device_id,w.head_sequence,w.active_device_id ORDER BY d.device_id`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncPresence{}
	for rows.Next() {
		var p SyncPresence
		if err = rows.Scan(&p.DeviceID, &p.Online, &p.Ready, &p.AppliedSequence, &p.LastSeenAt, &p.Active); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (db *Store) NotifyBrowserSyncPresence(ctx context.Context, i SyncConnectionIdentity) error {
	event, _ := json.Marshal(transport.AccountEvent{UserID: i.UserID, Topic: "browser-presence", ID: i.WorkspaceID})
	_, err := db.Conn.ExecContext(ctx, `SELECT pg_notify('misty_account_events',$1)`, string(event))
	return err
}
