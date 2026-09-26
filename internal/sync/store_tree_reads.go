package browsersync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"
)

const syncTreeDeltaLimit = 200

func (db *Store) readTx(ctx context.Context, userID, workspace, device string) (*sql.Tx, error) {
	if !validSyncID(workspace) || !validSyncID(device) {
		return nil, ErrSyncInvalid
	}
	tx, err := db.Conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	var ok bool
	err = tx.QueryRowContext(ctx, `SELECT true FROM browser_sync_workspaces w JOIN browser_sync_devices d USING(workspace_id) WHERE w.user_id=$1 AND w.workspace_id=$2 AND d.device_id=$3 AND d.revoked_at IS NULL`, userID, workspace, device).Scan(&ok)
	if err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSyncForbidden
		}
		return nil, err
	}
	return tx, nil
}

func (db *Store) BrowserSyncTrees(ctx context.Context, userID, workspace string) ([]SyncTree, error) {
	rows, err := db.Conn.QueryContext(ctx, `SELECT t.tree_id,t.tree_id=t.workspace_id,t.driver_device_id,t.driver_epoch,(EXTRACT(EPOCH FROM t.driver_seen_at)*1000)::bigint,t.version
 FROM browser_sync_trees t JOIN browser_sync_workspaces w USING(workspace_id) WHERE w.user_id=$1 AND w.workspace_id=$2 ORDER BY t.tree_id`, userID, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncTree{}
	for rows.Next() {
		var t SyncTree
		if err = rows.Scan(&t.TreeID, &t.Shared, &t.DriverDeviceID, &t.DriverEpoch, &t.DriverSeenAt, &t.Version); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DrivenBrowserSyncTree returns the tree a device currently drives, if any.
func (db *Store) DrivenBrowserSyncTree(ctx context.Context, workspace, device string) (string, error) {
	var tree string
	err := db.Conn.QueryRowContext(ctx, `SELECT tree_id FROM browser_sync_trees WHERE workspace_id=$1 AND driver_device_id=$2`, workspace, device).Scan(&tree)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return tree, err
}

func treeVersion(ctx context.Context, tx *sql.Tx, workspace, tree string) (int64, error) {
	var version int64
	err := tx.QueryRowContext(ctx, `SELECT version FROM browser_sync_trees WHERE workspace_id=$1 AND tree_id=$2`, workspace, tree).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrSyncForbidden
	}
	return version, err
}

func scanNodes(rows *sql.Rows) ([]SyncTreeNode, error) {
	defer rows.Close()
	out := []SyncTreeNode{}
	for rows.Next() {
		var n SyncTreeNode
		if err := rows.Scan(&n.NodeID, &n.ParentID, &n.Version, &n.KeyEpoch, &n.Ciphertext); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func scanSlots(rows *sql.Rows) ([]SyncSlotMeta, error) {
	defer rows.Close()
	out := []SyncSlotMeta{}
	for rows.Next() {
		var s SyncSlotMeta
		if err := rows.Scan(&s.TabNodeID, &s.Slot, &s.Version, &s.ContentHash); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func scanChange(scan func(...any) error) (SyncTreeChange, error) {
	var c SyncTreeChange
	var manifest []byte
	if err := scan(&c.TreeVersion, &c.Sequence, &c.OperationID, &c.DeviceID, &c.DeviceCounter, &c.KeyEpoch, &c.MerkleRoot, &manifest, &c.Signature); err != nil {
		return c, err
	}
	return c, json.Unmarshal(manifest, &c.Manifest)
}

const changeColumns = `tree_version,sequence,operation_id,device_id,device_counter,key_epoch,merkle_root,manifest,signature`

// BrowserSyncTreeDelta returns signed manifests after a client's tree version
// and the current state of everything they touched. A pruned gap requires a
// snapshot instead.
func (db *Store) BrowserSyncTreeDelta(ctx context.Context, userID, workspace, device, tree string, after int64) (*SyncTreeDelta, error) {
	if !validSyncID(tree) || after < 0 {
		return nil, ErrSyncInvalid
	}
	tx, err := db.readTx(ctx, userID, workspace, device)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &SyncTreeDelta{TreeID: tree, Changes: []SyncTreeChange{}, Nodes: []SyncTreeNode{}, Slots: []SyncSlotMeta{}, Deleted: []string{}}
	if out.Version, err = treeVersion(ctx, tx, workspace, tree); err != nil {
		return nil, err
	}
	if after > out.Version {
		return nil, ErrSyncCursor
	}
	if after == out.Version {
		return out, nil
	}
	// A client far behind gets a snapshot: trees are small, and a snapshot is
	// always verifiable against its Merkle root in one step.
	if out.Version-after > syncTreeDeltaLimit {
		return nil, ErrSyncTreeSnapshot
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+changeColumns+` FROM browser_sync_changes WHERE workspace_id=$1 AND tree_id=$2 AND tree_version>$3 ORDER BY tree_version`, workspace, tree, after)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c, err := scanChange(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if c.TreeVersion != after+int64(len(out.Changes))+1 {
			rows.Close()
			return nil, ErrSyncTreeSnapshot
		}
		out.Changes = append(out.Changes, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if int64(len(out.Changes)) != out.Version-after {
		return nil, ErrSyncTreeSnapshot
	}
	touched, slotTabs := map[string]bool{}, map[string]bool{}
	for _, c := range out.Changes {
		for _, u := range c.Manifest.Upserts {
			touched[u[0]] = true
		}
		for _, d := range c.Manifest.Deletes {
			touched[d] = true
			slotTabs[d] = true
		}
		for _, s := range c.Manifest.Slots {
			slotTabs[s[0]] = true
		}
	}
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	nodes, err := tx.QueryContext(ctx, `SELECT node_id,parent_id,version,key_epoch,ciphertext FROM browser_sync_nodes WHERE workspace_id=$1 AND tree_id=$2 AND node_id=ANY($3::uuid[])`, workspace, tree, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	if out.Nodes, err = scanNodes(nodes); err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, n := range out.Nodes {
		present[n.NodeID] = true
	}
	for _, id := range ids {
		if !present[id] {
			out.Deleted = append(out.Deleted, id)
		}
	}
	tabs := make([]string, 0, len(slotTabs))
	for id := range slotTabs {
		tabs = append(tabs, id)
	}
	slots, err := tx.QueryContext(ctx, `SELECT tab_node_id,slot_kind,version,content_hash FROM browser_sync_slots WHERE workspace_id=$1 AND tree_id=$2 AND tab_node_id=ANY($3::uuid[])`, workspace, tree, pq.Array(tabs))
	if err != nil {
		return nil, err
	}
	if out.Slots, err = scanSlots(slots); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func (db *Store) BrowserSyncTreeSnapshot(ctx context.Context, userID, workspace, device, tree string) (*SyncTreeSnapshot, error) {
	if !validSyncID(tree) {
		return nil, ErrSyncInvalid
	}
	tx, err := db.readTx(ctx, userID, workspace, device)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &SyncTreeSnapshot{TreeID: tree}
	if out.Version, err = treeVersion(ctx, tx, workspace, tree); err != nil {
		return nil, err
	}
	nodes, err := tx.QueryContext(ctx, `SELECT node_id,parent_id,version,key_epoch,ciphertext FROM browser_sync_nodes WHERE workspace_id=$1 AND tree_id=$2 ORDER BY node_id`, workspace, tree)
	if err != nil {
		return nil, err
	}
	if out.Nodes, err = scanNodes(nodes); err != nil {
		return nil, err
	}
	slots, err := tx.QueryContext(ctx, `SELECT tab_node_id,slot_kind,version,content_hash FROM browser_sync_slots WHERE workspace_id=$1 AND tree_id=$2 ORDER BY tab_node_id,slot_kind`, workspace, tree)
	if err != nil {
		return nil, err
	}
	if out.Slots, err = scanSlots(slots); err != nil {
		return nil, err
	}
	if out.Version > 0 {
		c, err := scanChange(tx.QueryRowContext(ctx, `SELECT `+changeColumns+` FROM browser_sync_changes WHERE workspace_id=$1 AND tree_id=$2 AND tree_version=$3`, workspace, tree, out.Version).Scan)
		if err != nil {
			return nil, err
		}
		out.LastChange = &c
	}
	return out, tx.Commit()
}

type SyncSlot struct {
	TreeID     string `json:"tree_id"`
	TabNodeID  string `json:"tab_node_id"`
	Slot       int16  `json:"slot"`
	Version    int64  `json:"version"`
	KeyEpoch   int64  `json:"key_epoch"`
	Ciphertext []byte `json:"ciphertext"`
}

// BrowserSyncSlot loads one tab slot on demand; nil means it does not exist.
func (db *Store) BrowserSyncSlot(ctx context.Context, userID, workspace, device, tree, tab string, slot int16) (*SyncSlot, error) {
	if !validSyncID(tree) || !validSyncID(tab) || slot < 1 || slot > SyncTreeMaxSlotKind {
		return nil, ErrSyncInvalid
	}
	tx, err := db.readTx(ctx, userID, workspace, device)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &SyncSlot{TreeID: tree, TabNodeID: tab, Slot: slot}
	err = tx.QueryRowContext(ctx, `SELECT version,key_epoch,ciphertext FROM browser_sync_slots WHERE workspace_id=$1 AND tree_id=$2 AND tab_node_id=$3 AND slot_kind=$4`, workspace, tree, tab, slot).Scan(&out.Version, &out.KeyEpoch, &out.Ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
