package browsersync

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/kannachi323/misty/server/internal/platform/transport"
	"github.com/lib/pq"
)

// Discard reasons tell a client why a signed op consumed its counter without
// changing the tree, so its outbox keeps draining in counter order.
const (
	SyncDiscardTreeVersion = "tree_version"
	SyncDiscardNotDriver   = "not_driver"
	SyncDiscardStaleClaim  = "stale_claim"
	SyncDiscardPaused      = "full_sync_off"
)

type syncSigned struct {
	workspace, operation, device string
	counter, keyEpoch            int64
	signed, signature            []byte
}

type syncWriteTx struct {
	tx       *sql.Tx
	head     int64
	treeMode bool
	fullSync bool
	digest   [32]byte
}

// beginSignedWrite locks the workspace, verifies the device signature and
// returns an existing receipt for a retried operation. Tree ops and claims
// share the device's tree counter; the credential log keeps its own.
func (db *Store) beginSignedWrite(ctx context.Context, userID string, s syncSigned) (*syncWriteTx, *SyncReceipt, error) {
	tx, err := db.Conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	w := &syncWriteTx{tx: tx}
	var epoch int64
	err = tx.QueryRowContext(ctx, `SELECT head_sequence,key_epoch,tree_mode FROM browser_sync_workspaces WHERE user_id=$1 AND workspace_id=$2 FOR UPDATE`, userID, s.workspace).Scan(&w.head, &epoch, &w.treeMode)
	if errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return nil, nil, ErrSyncForbidden
	}
	if err != nil {
		tx.Rollback()
		return nil, nil, err
	}
	var public []byte
	var counter int64
	err = tx.QueryRowContext(ctx, `SELECT public_key,tree_last_counter,full_sync FROM browser_sync_devices WHERE workspace_id=$1 AND device_id=$2 AND revoked_at IS NULL FOR UPDATE`, s.workspace, s.device).Scan(&public, &counter, &w.fullSync)
	if errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return nil, nil, ErrSyncForbidden
	}
	if err != nil {
		tx.Rollback()
		return nil, nil, err
	}
	if len(public) != ed25519.PublicKeySize || !ed25519.Verify(public, s.signed, s.signature) {
		tx.Rollback()
		return nil, nil, ErrSyncForbidden
	}
	w.digest = sha256.Sum256(s.signed)
	var oldHash []byte
	var old SyncReceipt
	err = tx.QueryRowContext(ctx, `SELECT content_hash,sequence,discarded FROM browser_sync_tree_receipts WHERE workspace_id=$1 AND operation_id=$2`, s.workspace, s.operation).Scan(&oldHash, &old.Sequence, &old.Discarded)
	if err == nil {
		tx.Rollback()
		if string(oldHash) != string(w.digest[:]) {
			return nil, nil, ErrSyncOperationConflict
		}
		old.OperationID = s.operation
		return nil, &old, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return nil, nil, err
	}
	err = nil
	switch {
	case epoch != s.keyEpoch:
		err = ErrSyncEpoch
	case s.counter <= counter:
		err = ErrSyncCompacted
	case s.counter != counter+1:
		err = ErrSyncCounterGap
	case !w.treeMode:
		err = ErrSyncTreeMode
	}
	if err != nil {
		tx.Rollback()
		return nil, nil, err
	}
	return w, nil, nil
}

// consume records the receipt and advances the device counter. A discarded
// write keeps the current head sequence and never reaches the change feed.
func (w *syncWriteTx) consume(ctx context.Context, s syncSigned, sequence int64, discarded bool) error {
	_, err := w.tx.ExecContext(ctx, `INSERT INTO browser_sync_tree_receipts(workspace_id,operation_id,sequence,device_id,device_counter,content_hash,discarded) VALUES($1,$2,$3,$4,$5,$6,$7)`, s.workspace, s.operation, sequence, s.device, s.counter, w.digest[:], discarded)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_devices SET tree_last_counter=$3 WHERE workspace_id=$1 AND device_id=$2`, s.workspace, s.device, s.counter)
	return err
}

func (w *syncWriteTx) discard(ctx context.Context, s syncSigned, reason string) (*SyncReceipt, error) {
	defer w.tx.Rollback()
	if err := w.consume(ctx, s, w.head, true); err != nil {
		return nil, err
	}
	if err := w.tx.Commit(); err != nil {
		return nil, err
	}
	return &SyncReceipt{OperationID: s.operation, Sequence: w.head, Discarded: true, Reason: reason}, nil
}

func notifySync(ctx context.Context, tx *sql.Tx, userID, workspace, topic string) error {
	hint, _ := json.Marshal(transport.AccountEvent{UserID: userID, Topic: topic, ID: workspace})
	_, err := tx.ExecContext(ctx, `SELECT pg_notify('misty_account_events',$1)`, string(hint))
	return err
}

func (db *Store) PublishBrowserSyncTree(ctx context.Context, userID string, op SyncTreeOp) (*SyncReceipt, error) {
	if !op.Valid() {
		return nil, ErrSyncInvalid
	}
	s := syncSigned{op.WorkspaceID, op.OperationID, op.DeviceID, op.DeviceCounter, op.KeyEpoch, op.SigningBytes(), op.Signature}
	w, existing, err := db.beginSignedWrite(ctx, userID, s)
	if err != nil || existing != nil {
		return existing, err
	}
	defer w.tx.Rollback()
	var driver sql.NullString
	var version int64
	err = w.tx.QueryRowContext(ctx, `SELECT driver_device_id,version FROM browser_sync_trees WHERE workspace_id=$1 AND tree_id=$2 FOR UPDATE`, op.WorkspaceID, op.TreeID).Scan(&driver, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSyncForbidden
	}
	if err != nil {
		return nil, err
	}
	shared := op.TreeID == op.WorkspaceID
	switch {
	case !w.fullSync:
		return w.discard(ctx, s, SyncDiscardPaused)
	case !shared && (!driver.Valid || driver.String != op.DeviceID):
		return w.discard(ctx, s, SyncDiscardNotDriver)
	case op.BaseTreeVersion != version:
		return w.discard(ctx, s, SyncDiscardTreeVersion)
	}
	if err = checkTreeReferences(ctx, w.tx, op); err != nil {
		return nil, err
	}
	// Tree changes have their own sequence. The workspace head belongs to the
	// credential log; advancing it here would leave gaps in that log's replay.
	var treeHead int64
	if err = w.tx.QueryRowContext(ctx, `SELECT COALESCE(max(head_sequence),0) FROM browser_sync_trees WHERE workspace_id=$1`, op.WorkspaceID).Scan(&treeHead); err != nil {
		return nil, err
	}
	sequence := treeHead + 1
	if sequence > SyncMaxCounter {
		return nil, ErrSyncInvalid
	}
	if err = applyTreeOp(ctx, w.tx, op, sequence); err != nil {
		return nil, err
	}
	if err = checkTreeShape(ctx, w.tx, op.WorkspaceID, op.TreeID); err != nil {
		return nil, err
	}
	manifest, _ := json.Marshal(op.Manifest())
	_, err = w.tx.ExecContext(ctx, `INSERT INTO browser_sync_changes(workspace_id,tree_id,tree_version,sequence,operation_id,device_id,device_counter,key_epoch,merkle_root,manifest,signature) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		op.WorkspaceID, op.TreeID, op.TreeVersion(), sequence, op.OperationID, op.DeviceID, op.DeviceCounter, op.KeyEpoch, op.MerkleRoot, string(manifest), op.Signature)
	if err != nil {
		return nil, err
	}
	// Bounded, opportunistic retention: keep at least 500 versions or 7 days.
	_, err = w.tx.ExecContext(ctx, `DELETE FROM browser_sync_changes WHERE workspace_id=$1 AND tree_id=$2 AND tree_version<=$3 AND created_at<now()-interval '7 days'`, op.WorkspaceID, op.TreeID, op.TreeVersion()-500)
	if err != nil {
		return nil, err
	}
	if _, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_trees SET version=$3,head_sequence=$4 WHERE workspace_id=$1 AND tree_id=$2`, op.WorkspaceID, op.TreeID, op.TreeVersion(), sequence); err != nil {
		return nil, err
	}
	if len(op.BlobRefs) > 0 {
		if _, err = w.tx.ExecContext(ctx, `UPDATE browser_sync_blobs SET last_ref_at=now() WHERE workspace_id=$1 AND blob_hash=ANY($2)`, op.WorkspaceID, pq.ByteaArray(op.BlobRefs)); err != nil {
			return nil, err
		}
	}
	if err = w.consume(ctx, s, sequence, false); err != nil {
		return nil, err
	}
	if err = notifySync(ctx, w.tx, userID, op.WorkspaceID, "browser-sync"); err != nil {
		return nil, err
	}
	if err = w.tx.Commit(); err != nil {
		return nil, err
	}
	return &SyncReceipt{OperationID: op.OperationID, Sequence: sequence, TreeVersion: op.TreeVersion()}, nil
}

// checkTreeReferences rejects deletes of absent nodes and slot writes to tabs
// that will not exist, before any statement can abort the transaction.
func checkTreeReferences(ctx context.Context, tx *sql.Tx, op SyncTreeOp) error {
	upserted := map[string]bool{}
	for _, n := range op.Upserts {
		upserted[n.NodeID] = true
	}
	need := append([]string{}, op.Deletes...)
	for _, s := range op.Slots {
		if !upserted[s.TabNodeID] {
			need = append(need, s.TabNodeID)
		}
	}
	if len(need) == 0 {
		return nil
	}
	var found int
	err := tx.QueryRowContext(ctx, `SELECT count(DISTINCT node_id) FROM browser_sync_nodes WHERE workspace_id=$1 AND tree_id=$2 AND node_id=ANY($3::uuid[])`, op.WorkspaceID, op.TreeID, pq.Array(need)).Scan(&found)
	if err != nil {
		return err
	}
	distinct := map[string]bool{}
	for _, id := range need {
		distinct[id] = true
	}
	deleted := map[string]bool{}
	for _, id := range op.Deletes {
		deleted[id] = true
	}
	for _, s := range op.Slots {
		if deleted[s.TabNodeID] {
			return ErrSyncInvalid
		}
	}
	if found != len(distinct) {
		return ErrSyncInvalid
	}
	return nil
}

func applyTreeOp(ctx context.Context, tx *sql.Tx, op SyncTreeOp, sequence int64) error {
	if len(op.Deletes) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM browser_sync_nodes WHERE workspace_id=$1 AND tree_id=$2 AND node_id=ANY($3::uuid[])`, op.WorkspaceID, op.TreeID, pq.Array(op.Deletes)); err != nil {
			return err
		}
	}
	for _, n := range op.Upserts {
		sum := sha256.Sum256(n.Ciphertext)
		_, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_nodes(workspace_id,tree_id,node_id,parent_id,version,key_epoch,ciphertext,content_hash,updated_seq) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
 ON CONFLICT(workspace_id,tree_id,node_id) DO UPDATE SET parent_id=EXCLUDED.parent_id,version=EXCLUDED.version,key_epoch=EXCLUDED.key_epoch,ciphertext=EXCLUDED.ciphertext,content_hash=EXCLUDED.content_hash,updated_seq=EXCLUDED.updated_seq`,
			op.WorkspaceID, op.TreeID, n.NodeID, n.ParentID, op.TreeVersion(), op.KeyEpoch, n.Ciphertext, sum[:], sequence)
		if err != nil {
			return err
		}
	}
	for _, s := range op.Slots {
		if s.Ciphertext == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM browser_sync_slots WHERE workspace_id=$1 AND tree_id=$2 AND tab_node_id=$3 AND slot_kind=$4`, op.WorkspaceID, op.TreeID, s.TabNodeID, s.Slot); err != nil {
				return err
			}
			continue
		}
		sum := sha256.Sum256(s.Ciphertext)
		_, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_slots(workspace_id,tree_id,tab_node_id,slot_kind,version,key_epoch,ciphertext,content_hash,updated_seq) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
 ON CONFLICT(workspace_id,tree_id,tab_node_id,slot_kind) DO UPDATE SET version=EXCLUDED.version,key_epoch=EXCLUDED.key_epoch,ciphertext=EXCLUDED.ciphertext,content_hash=EXCLUDED.content_hash,updated_seq=EXCLUDED.updated_seq`,
			op.WorkspaceID, op.TreeID, s.TabNodeID, s.Slot, op.TreeVersion(), op.KeyEpoch, s.Ciphertext, sum[:], sequence)
		if err != nil {
			return err
		}
	}
	return nil
}

// checkTreeShape requires every node to be reachable from the root, which
// rejects orphans (deleted parents) and parent cycles in one pass.
func checkTreeShape(ctx context.Context, tx *sql.Tx, workspace, tree string) error {
	var total, reachable int
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE reach(node_id) AS (
   SELECT node_id FROM browser_sync_nodes WHERE workspace_id=$1 AND tree_id=$2 AND node_id=$2 AND parent_id IS NULL
   UNION SELECT n.node_id FROM browser_sync_nodes n JOIN reach r ON n.parent_id=r.node_id WHERE n.workspace_id=$1 AND n.tree_id=$2
 ) SELECT (SELECT count(*) FROM browser_sync_nodes WHERE workspace_id=$1 AND tree_id=$2),(SELECT count(*) FROM reach)`, workspace, tree).Scan(&total, &reachable)
	if err != nil {
		return err
	}
	if total != reachable {
		return ErrSyncInvalid
	}
	return nil
}
