package browsersync

import (
	"context"
	"crypto/sha256"
	"database/sql"

	"github.com/lib/pq"
)

// checkWorkspaceReferences rejects deletes of absent nodes and slot writes to tabs
// that will not exist, before any statement can abort the transaction.
func checkWorkspaceReferences(ctx context.Context, tx *sql.Tx, op SyncWorkspaceOp) error {
	upserted := map[string]bool{}
	for _, n := range op.Upserts {
		upserted[n.NodeID] = true
	}
	need := append([]string{}, op.Deletes...)
	for _, s := range op.Slots {
		if !upserted[s.ViewNodeID] {
			need = append(need, s.ViewNodeID)
		}
	}
	if len(need) == 0 {
		return nil
	}
	var found int
	err := tx.QueryRowContext(ctx, `SELECT count(DISTINCT node_id) FROM browser_sync_nodes WHERE vault_id=$1 AND workspace_id=$2 AND node_id=ANY($3::uuid[])`, op.VaultID, op.WorkspaceID, pq.Array(need)).Scan(&found)
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
		if deleted[s.ViewNodeID] {
			return ErrSyncInvalid
		}
	}
	if found != len(distinct) {
		return ErrSyncInvalid
	}
	return nil
}

func applyWorkspaceOp(ctx context.Context, tx *sql.Tx, op SyncWorkspaceOp, sequence int64) error {
	if len(op.Deletes) > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM browser_sync_nodes WHERE vault_id=$1 AND workspace_id=$2 AND node_id=ANY($3::uuid[])`, op.VaultID, op.WorkspaceID, pq.Array(op.Deletes)); err != nil {
			return err
		}
	}
	for _, n := range op.Upserts {
		sum := sha256.Sum256(n.Ciphertext)
		_, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_nodes(vault_id,workspace_id,node_id,parent_id,version,key_epoch,ciphertext,content_hash,updated_seq) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
 ON CONFLICT(vault_id,workspace_id,node_id) DO UPDATE SET parent_id=EXCLUDED.parent_id,version=EXCLUDED.version,key_epoch=EXCLUDED.key_epoch,ciphertext=EXCLUDED.ciphertext,content_hash=EXCLUDED.content_hash,updated_seq=EXCLUDED.updated_seq`,
			op.VaultID, op.WorkspaceID, n.NodeID, n.ParentID, op.WorkspaceVersion(), op.KeyEpoch, n.Ciphertext, sum[:], sequence)
		if err != nil {
			return err
		}
	}
	for _, s := range op.Slots {
		if s.Ciphertext == nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM browser_sync_slots WHERE vault_id=$1 AND workspace_id=$2 AND view_node_id=$3 AND slot_kind=$4`, op.VaultID, op.WorkspaceID, s.ViewNodeID, s.Slot); err != nil {
				return err
			}
			continue
		}
		sum := sha256.Sum256(s.Ciphertext)
		_, err := tx.ExecContext(ctx, `INSERT INTO browser_sync_slots(vault_id,workspace_id,view_node_id,slot_kind,version,key_epoch,ciphertext,content_hash,updated_seq) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
 ON CONFLICT(vault_id,workspace_id,view_node_id,slot_kind) DO UPDATE SET version=EXCLUDED.version,key_epoch=EXCLUDED.key_epoch,ciphertext=EXCLUDED.ciphertext,content_hash=EXCLUDED.content_hash,updated_seq=EXCLUDED.updated_seq`,
			op.VaultID, op.WorkspaceID, s.ViewNodeID, s.Slot, op.WorkspaceVersion(), op.KeyEpoch, s.Ciphertext, sum[:], sequence)
		if err != nil {
			return err
		}
	}
	return nil
}

// checkWorkspaceShape requires every node to be reachable from the root, which
// rejects orphans (deleted parents) and parent cycles in one pass.
func checkWorkspaceShape(ctx context.Context, tx *sql.Tx, vault, workspace string) error {
	var total, reachable int
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE reach(node_id) AS (
   SELECT node_id FROM browser_sync_nodes WHERE vault_id=$1 AND workspace_id=$2 AND node_id=$2 AND parent_id IS NULL
   UNION SELECT n.node_id FROM browser_sync_nodes n JOIN reach r ON n.parent_id=r.node_id WHERE n.vault_id=$1 AND n.workspace_id=$2
 ) SELECT (SELECT count(*) FROM browser_sync_nodes WHERE vault_id=$1 AND workspace_id=$2),(SELECT count(*) FROM reach)`, vault, workspace).Scan(&total, &reachable)
	if err != nil {
		return err
	}
	if total != reachable {
		return ErrSyncInvalid
	}
	return nil
}
