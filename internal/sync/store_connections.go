package browsersync

import (
	"context"
	"database/sql"
	"errors"
	"github.com/kannachi323/misty/server/internal/cloudusage"
	"time"

	"github.com/lib/pq"
)

type SyncDevice struct {
	SyncDeviceGrant
	LastCounter           int64      `json:"last_counter"`
	RevokedAt             *time.Time `json:"revoked_at"`
	DisplayName           string     `json:"display_name"`
	Platform              string     `json:"platform"`
	ControlVersion        int        `json:"control_version"`
	FullSync              bool       `json:"full_sync"`
	ActivationRequest     *string    `json:"activation_request"`
	ActivationExpiresAt   int64      `json:"activation_expires_at"`
	OSVersion             string     `json:"os_version"`
	ActivationWorkspaceID *string    `json:"activation_workspace_id"`
	WorkspaceLastCounter  int64      `json:"workspace_last_counter"`
	// The device keeps bookmarks in the cold record store.
	UsesCollections bool `json:"uses_collections"`
	// Enrollment time, so clients number unnamed devices stably.
	CreatedAt time.Time `json:"created_at"`
}
type SyncConnectionIdentity struct {
	UserID, VaultID, DeviceID string
	PublicKey                 []byte
	// SessionHash names the account session that minted the ticket. Empty for
	// tickets minted before sessions were recorded.
	SessionHash string
}
type SyncPresence struct {
	DeviceID        string     `json:"device_id"`
	Online          bool       `json:"online"`
	Ready           bool       `json:"ready"`
	AppliedSequence int64      `json:"applied_sequence"`
	LastSeenAt      *time.Time `json:"last_seen_at"`
	Active          bool       `json:"active"`
}

func (db *Store) BrowserSyncDevices(ctx context.Context, userID, vaultID string) ([]SyncDevice, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT d.device_id,d.public_key,d.grant_epoch,d.grant_signature,d.last_counter,d.revoked_at,d.display_name,d.platform,d.control_version,d.full_sync,d.activation_request,COALESCE((EXTRACT(EPOCH FROM d.activation_expires_at)*1000)::bigint,0),d.os_version,d.activation_workspace_id,d.workspace_last_counter,d.uses_collections,d.created_at FROM browser_sync_devices d JOIN browser_sync_vaults w USING(vault_id) WHERE w.user_id=$1 AND w.vault_id=$2 ORDER BY d.created_at,d.device_id`, userID, vaultID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncDevice{}
	for rows.Next() {
		d := SyncDevice{}
		d.VaultID = vaultID
		if err = rows.Scan(&d.DeviceID, &d.PublicKey, &d.KeyEpoch, &d.Signature, &d.LastCounter, &d.RevokedAt, &d.DisplayName, &d.Platform, &d.ControlVersion, &d.FullSync, &d.ActivationRequest, &d.ActivationExpiresAt, &d.OSVersion, &d.ActivationWorkspaceID, &d.WorkspaceLastCounter, &d.UsesCollections, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (db *Store) CreateBrowserSyncTicket(ctx context.Context, userID, vaultID, deviceID, hash string, sessionHash ...string) error {
	if !validSyncID(vaultID) || !validSyncID(deviceID) || len(hash) != 64 {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT true FROM browser_sync_devices d JOIN browser_sync_vaults w USING(vault_id) WHERE w.user_id=$1 AND w.vault_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL FOR UPDATE OF d`, userID, vaultID, deviceID).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSyncForbidden
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM browser_sync_tickets WHERE vault_id=$1 AND device_id=$2 AND expires_at<=clock_timestamp()`, vaultID, deviceID)
	if err != nil {
		return err
	}
	var pending int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM browser_sync_tickets WHERE vault_id=$1 AND device_id=$2`, vaultID, deviceID).Scan(&pending)
	if err != nil {
		return err
	}
	if pending >= 8 {
		return ErrSyncInvalid
	}
	session := ""
	if len(sessionHash) > 0 {
		session = sessionHash[0]
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO browser_sync_tickets(token_hash,vault_id,device_id,session_hash) VALUES($1,$2,$3,NULLIF($4,''))`, hash, vaultID, deviceID, session)
	if err != nil {
		return err
	}
	return cloudusage.Commit(ctx, tx)
}
func (db *Store) ConsumeBrowserSyncTicket(ctx context.Context, hash string) (*SyncConnectionIdentity, error) {
	var i SyncConnectionIdentity
	err := db.Conn.QueryRowContext(ctx, `WITH consumed AS (DELETE FROM browser_sync_tickets WHERE token_hash=$1 AND expires_at>clock_timestamp() RETURNING vault_id,device_id,session_hash) SELECT w.user_id,c.vault_id,c.device_id,d.public_key,COALESCE(c.session_hash,'') FROM consumed c JOIN browser_sync_vaults w USING(vault_id) JOIN browser_sync_devices d ON d.vault_id=c.vault_id AND d.device_id=c.device_id WHERE d.revoked_at IS NULL`, hash).Scan(&i.UserID, &i.VaultID, &i.DeviceID, &i.PublicKey, &i.SessionHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	return &i, err
}

// syncConnectionLive is true for a connection whose socket is open on a live
// API process. Rows from processes predating instance leases keep their own
// expiry until they are swept.
const syncConnectionLive = `(CASE WHEN c.instance_id IS NULL THEN c.expires_at>clock_timestamp()
 ELSE EXISTS(SELECT 1 FROM browser_sync_instances s WHERE s.instance_id=c.instance_id AND s.expires_at>clock_timestamp()) END)`

// SyncInstanceLease is how long a process's connections stay live without a
// renewal. Processes renew at a third of it.
const SyncInstanceLease = 45 * time.Second

// BrowserSyncConnect records an open socket. Liveness comes from the owning
// process's lease, so an idle connection writes nothing until it closes. The
// expiry is unbounded for older processes reading this row during a rollout.
func (db *Store) BrowserSyncConnect(ctx context.Context, i SyncConnectionIdentity, connectionID, instanceID string, applied int64, ready bool) error {
	if !validSyncID(connectionID) || !validSyncID(instanceID) || applied < 0 || applied > SyncMaxCounter {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = renewSyncInstance(ctx, tx, instanceID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_connections(connection_id,vault_id,device_id,ready,applied_sequence,instance_id,expires_at)
 SELECT $4,w.vault_id,d.device_id,$6,$5,$7,'infinity' FROM browser_sync_vaults w JOIN browser_sync_devices d USING(vault_id)
 WHERE w.user_id=$1 AND w.vault_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL AND $5<=w.head_sequence`, i.UserID, i.VaultID, i.DeviceID, connectionID, applied, ready, instanceID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrSyncForbidden
	}
	if err = notifySync(ctx, tx, i.UserID, i.VaultID, "browser-presence"); err != nil {
		return err
	}
	return cloudusage.Commit(ctx, tx)
}

// BrowserSyncProgress records a changed applied cursor or readiness; callers
// write only on change. It returns whether the device is ready and caught up
// with the vault head, and publishes presence when that differs from current,
// the caller's last known value.
func (db *Store) BrowserSyncProgress(ctx context.Context, i SyncConnectionIdentity, connectionID string, applied int64, ready, current bool) (bool, error) {
	if !validSyncID(connectionID) || applied < 0 || applied > SyncMaxCounter {
		return false, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var caughtUp bool
	err = tx.QueryRowContext(ctx, `UPDATE browser_sync_connections c SET ready=$5,applied_sequence=$4,last_seen_at=clock_timestamp()
 FROM browser_sync_vaults w JOIN browser_sync_devices d USING(vault_id)
 WHERE c.connection_id=$3 AND c.vault_id=w.vault_id AND c.device_id=d.device_id
  AND w.user_id=$1 AND w.vault_id=$2 AND d.device_id=$6 AND d.revoked_at IS NULL AND $4<=w.head_sequence
 RETURNING c.ready AND c.applied_sequence>=w.head_sequence`, i.UserID, i.VaultID, connectionID, applied, ready, i.DeviceID).Scan(&caughtUp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrSyncForbidden
	}
	if err != nil {
		return false, err
	}
	if caughtUp != current {
		if err = notifySync(ctx, tx, i.UserID, i.VaultID, "browser-presence"); err != nil {
			return false, err
		}
	}
	return caughtUp, cloudusage.Commit(ctx, tx)
}

// BrowserSyncDisconnect removes a closed socket's row and keeps the device's
// last-seen time, so the table holds only open connections.
func (db *Store) BrowserSyncDisconnect(ctx context.Context, i SyncConnectionIdentity, connectionID string) error {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = sweepSyncConnections(ctx, tx, `DELETE FROM browser_sync_connections c WHERE c.connection_id=$1 AND c.vault_id=$2 AND c.device_id=$3
 RETURNING c.vault_id,c.device_id,clock_timestamp() AS last_seen_at`, connectionID, i.VaultID, i.DeviceID); err != nil {
		return err
	}
	return cloudusage.Commit(ctx, tx)
}
func (db *Store) BrowserSyncPresence(ctx context.Context, userID, vaultID string) ([]SyncPresence, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT d.device_id,COALESCE(bool_or(`+syncConnectionLive+`),false),
 COALESCE(bool_or(`+syncConnectionLive+` AND c.ready AND c.applied_sequence>=w.head_sequence),false),
 COALESCE(max(c.applied_sequence) FILTER (WHERE `+syncConnectionLive+`),0),
 COALESCE(max(c.last_seen_at) FILTER (WHERE `+syncConnectionLive+`),d.last_seen_at,max(c.last_seen_at)),
 COALESCE(w.active_device_id=d.device_id,false)
 FROM browser_sync_devices d JOIN browser_sync_vaults w USING(vault_id) LEFT JOIN browser_sync_connections c ON c.vault_id=d.vault_id AND c.device_id=d.device_id
 WHERE w.user_id=$1 AND w.vault_id=$2 AND d.revoked_at IS NULL GROUP BY d.device_id,d.last_seen_at,w.head_sequence,w.active_device_id ORDER BY d.device_id`, userID, vaultID)
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

func renewSyncInstance(ctx context.Context, tx *sql.Tx, instanceID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_instances(instance_id,expires_at) VALUES($1,clock_timestamp()+$2::interval)
 ON CONFLICT(instance_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, instanceID, SyncInstanceLease.String())
	return err
}

// RenewBrowserSyncInstance extends this process's lease and removes the
// connections of processes whose lease lapsed, publishing presence for each
// affected vault. It is one bounded pass per process, independent of how many
// devices are connected. Legacy rows expire in bounded batches.
func (db *Store) RenewBrowserSyncInstance(ctx context.Context, instanceID string) (int, error) {
	if !validSyncID(instanceID) {
		return 0, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = renewSyncInstance(ctx, tx, instanceID); err != nil {
		return 0, err
	}
	var dead []string
	rows, err := tx.QueryContext(ctx, `DELETE FROM browser_sync_instances WHERE instance_id IN (
 SELECT instance_id FROM browser_sync_instances WHERE expires_at<=clock_timestamp() FOR UPDATE SKIP LOCKED LIMIT 100)
 RETURNING instance_id`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		dead = append(dead, id)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	removed := 0
	if len(dead) > 0 {
		if removed, err = sweepSyncConnections(ctx, tx, `DELETE FROM browser_sync_connections c WHERE c.instance_id=ANY($1::uuid[])
 RETURNING c.vault_id,c.device_id,c.last_seen_at`, pq.Array(dead)); err != nil {
			return 0, err
		}
	}
	legacy, err := sweepSyncConnections(ctx, tx, `DELETE FROM browser_sync_connections c WHERE c.connection_id IN (
 SELECT connection_id FROM browser_sync_connections WHERE instance_id IS NULL AND expires_at<=clock_timestamp() LIMIT 1000)
 RETURNING c.vault_id,c.device_id,c.last_seen_at`)
	if err != nil {
		return 0, err
	}
	return removed + legacy, cloudusage.Commit(ctx, tx)
}

// ReleaseBrowserSyncInstance ends this process's lease on shutdown so its
// devices go offline at once rather than when the lease lapses.
func (db *Store) ReleaseBrowserSyncInstance(ctx context.Context, instanceID string) error {
	if !validSyncID(instanceID) {
		return ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM browser_sync_instances WHERE instance_id=$1`, instanceID); err != nil {
		return err
	}
	if _, err = sweepSyncConnections(ctx, tx, `DELETE FROM browser_sync_connections c WHERE c.instance_id=$1::uuid
 RETURNING c.vault_id,c.device_id,c.last_seen_at`, instanceID); err != nil {
		return err
	}
	return cloudusage.Commit(ctx, tx)
}

// sweepSyncConnections runs a DELETE ... RETURNING vault_id,device_id,last_seen_at,
// keeps each device's latest last-seen time and publishes presence once per vault.
func sweepSyncConnections(ctx context.Context, tx *sql.Tx, deletion string, args ...any) (int, error) {
	rows, err := tx.QueryContext(ctx, `WITH removed AS (`+deletion+`),
 seen AS (UPDATE browser_sync_devices d SET last_seen_at=GREATEST(d.last_seen_at,r.seen)
  FROM (SELECT vault_id,device_id,max(last_seen_at) AS seen FROM removed GROUP BY 1,2) r
  WHERE d.vault_id=r.vault_id AND d.device_id=r.device_id RETURNING d.vault_id)
 SELECT w.user_id,w.vault_id,(SELECT count(*) FROM removed) FROM browser_sync_vaults w WHERE w.vault_id IN (SELECT vault_id FROM removed)`, args...)
	if err != nil {
		return 0, err
	}
	type vault struct{ user, id string }
	var vaults []vault
	removed := 0
	for rows.Next() {
		var v vault
		if err = rows.Scan(&v.user, &v.id, &removed); err != nil {
			rows.Close()
			return 0, err
		}
		vaults = append(vaults, v)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	for _, v := range vaults {
		if err = notifySync(ctx, tx, v.user, v.id, "browser-presence"); err != nil {
			return 0, err
		}
	}
	return removed, nil
}
