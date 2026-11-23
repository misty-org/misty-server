package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lib/pq"
)

const driveRetireBatch = 32

func (db *Database) TrashDriveItem(ctx context.Context, userID, spaceID, itemID string, device DriveDevice, ifMatch int64, held bool) (*DriveWriteResult, error) {
	if !ValidDriveDevice(device) || ifMatch < 0 {
		return nil, ErrLibraryInvalid
	}
	result := &DriveWriteResult{DeleteObjectKeys: []string{}, RetireKeys: []string{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if err := lockDriveTx(ctx, tx, spaceID); err != nil {
			return err
		}
		item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, true)
		if err != nil {
			return err
		}
		hidden, err := driveHiddenTx(ctx, tx, item)
		if err != nil {
			return err
		}
		if hidden {
			return DriveConflictError{Code: "trashed", Item: item}
		}
		if item.Kind == driveKindFile && isRootDriveIgnore(item.ParentID, item.Name) {
			return ErrDriveProtected
		}
		if ifMatch > 0 && item.Version != ifMatch {
			return DriveConflictError{Code: "version_conflict", Item: item}
		}
		seq, err := trashDriveItemTx(ctx, tx, spaceID, userID, item.ID, userID, device.ID, held)
		if err != nil {
			return err
		}
		if err := recordDriveChangedTx(ctx, tx, spaceID, userID, item.ID, seq); err != nil {
			return err
		}
		result.Item, err = driveItemTx(ctx, tx, spaceID, item.ID, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func trashDriveItemTx(ctx context.Context, tx *sql.Tx, spaceID, userID, itemID, exceptUserID, exceptDeviceID string, held bool) (int64, error) {
	subtree, err := driveSubtreeTx(ctx, tx, itemID)
	if err != nil {
		return 0, err
	}
	who, err := driveInUseHoldersTx(ctx, tx, subtree, exceptUserID, exceptDeviceID)
	if err != nil {
		return 0, err
	}
	if len(who) > 0 {
		return 0, DriveInUseError{Who: who}
	}
	seq, err := nextDriveSeqsTx(ctx, tx, spaceID, 1)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE space_drive_items SET trashed_at=NOW(),trashed_by=$2,trash_hold=$3,
		version=version+1,seq=$4 WHERE id=$1`, itemID, userID, held, seq)
	return seq, err
}

func driveRestoredName(name, kind string, n int) string {
	return driveLabeledName(name, kind, "restored", n)
}

func driveLabeledName(name, kind, label string, n int) string {
	base, ext := name, ""
	if kind == driveKindFile {
		if dot := strings.LastIndex(name, "."); dot > 0 {
			base, ext = name[:dot], name[dot:]
		}
	}
	suffix := " (" + label + ")"
	if n > 1 {
		suffix = fmt.Sprintf(" (%s %d)", label, n)
	}
	for len(base)+len(suffix)+len(ext) > 255 && base != "" {
		_, size := utf8.DecodeLastRuneInString(base)
		base = base[:len(base)-size]
	}
	return base + suffix + ext
}

func restoreOneDriveItemTx(ctx context.Context, tx *sql.Tx, spaceID string, item DriveItem) (int64, error) {
	name := item.Name
	for n := 1; ; n++ {
		holder, err := driveNameHolderTx(ctx, tx, spaceID, item.ParentID, name, item.ID)
		if err != nil {
			return 0, err
		}
		if holder == nil {
			break
		}
		name = driveRestoredName(item.Name, item.Kind, n)
	}
	seq, err := nextDriveSeqsTx(ctx, tx, spaceID, 1)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE space_drive_items SET name=$2,trashed_at=NULL,trashed_by=NULL,trash_hold=FALSE,
		version=version+1,seq=$3 WHERE id=$1`, item.ID, name, seq)
	return seq, err
}

func reviveDriveItemTx(ctx context.Context, tx *sql.Tx, spaceID string, item DriveItem) (int64, error) {
	ancestors, err := driveAncestorsTx(ctx, tx, item.ID)
	if err != nil {
		return 0, err
	}
	chain := []DriveItem{}
	for i := len(ancestors) - 1; i >= 0; i-- {
		if ancestors[i].Trashed {
			chain = append(chain, ancestors[i])
		}
	}
	if item.Trashed {
		chain = append(chain, item)
	}
	var seq int64
	for _, restoring := range chain {
		locked, err := liveDriveItemTx(ctx, tx, spaceID, restoring.ID, true)
		if err != nil {
			return 0, err
		}
		if seq, err = restoreOneDriveItemTx(ctx, tx, spaceID, locked); err != nil {
			return 0, err
		}
	}
	return seq, nil
}

func (db *Database) RestoreDriveItem(ctx context.Context, userID, spaceID, itemID string, device DriveDevice, ifMatch int64) (*DriveWriteResult, error) {
	if !ValidDriveDevice(device) || ifMatch < 0 {
		return nil, ErrLibraryInvalid
	}
	result := &DriveWriteResult{DeleteObjectKeys: []string{}, RetireKeys: []string{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if err := lockDriveTx(ctx, tx, spaceID); err != nil {
			return err
		}
		item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, true)
		if err != nil {
			return err
		}
		if ifMatch > 0 && item.Version != ifMatch {
			return DriveConflictError{Code: "version_conflict", Item: item}
		}
		seq, err := reviveDriveItemTx(ctx, tx, spaceID, item)
		if err != nil {
			return err
		}
		if seq > 0 {
			if err := recordDriveChangedTx(ctx, tx, spaceID, userID, item.ID, seq); err != nil {
				return err
			}
		}
		result.Item, err = driveItemTx(ctx, tx, spaceID, item.ID, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func purgeDriveSubtreeTx(ctx context.Context, tx *sql.Tx, spaceID, userID, rootID string) ([]string, error) {
	subtree, err := driveSubtreeTx(ctx, tx, rootID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(blob_key,'') FROM space_drive_items
		WHERE id=ANY($1) AND space_id=$2 AND deleted_at IS NULL FOR UPDATE`, pq.Array(subtree), spaceID)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	keys, seen := []string{}, map[string]bool{}
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			rows.Close()
			return nil, err
		}
		live[id] = true
		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, id := range subtree {
		if live[id] {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	released, err := releaseDriveContributionsTx(ctx, tx, spaceID, ids)
	if err != nil {
		return nil, err
	}
	if released > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_drive_in_use WHERE item_id=ANY($1)`, pq.Array(ids)); err != nil {
		return nil, err
	}
	first, err := nextDriveSeqsTx(ctx, tx, spaceID, len(ids))
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_drive_items i SET deleted_at=NOW(),blob_key=NULL,parent_id=NULL,
		version=i.version+1,seq=$2+n.ord-1
		FROM unnest($1::text[]) WITH ORDINALITY AS n(id,ord) WHERE i.id=n.id`, pq.Array(ids), first); err != nil {
		return nil, err
	}
	if err := recordDriveChangedTx(ctx, tx, spaceID, userID, rootID, first+int64(len(ids))-1); err != nil {
		return nil, err
	}
	return keys, insertLibraryAuditTx(ctx, tx, spaceID, "", userID, "drive.item.purged", "drive_item", rootID, "success",
		map[string]any{"items": len(ids), "released_bytes": released})
}

func (db *Database) PurgeDriveItem(ctx context.Context, userID, spaceID, itemID string, device DriveDevice) ([]string, error) {
	if !ValidDriveDevice(device) {
		return nil, ErrLibraryInvalid
	}
	var retire []string
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDrivePurgeTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if err := lockDriveTx(ctx, tx, spaceID); err != nil {
			return err
		}
		item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, true)
		if err != nil {
			return err
		}
		if !item.Trashed {
			return ErrDriveNotTrashed
		}
		retire, err = purgeDriveSubtreeTx(ctx, tx, spaceID, userID, item.ID)
		return err
	})
	return retire, err
}

const driveExpiredTrashCondition = `i.trashed_at IS NOT NULL AND i.trashed_at<=NOW()-$1::interval AND i.deleted_at IS NULL AND NOT i.trash_hold
	AND NOT EXISTS (
		WITH RECURSIVE sub(id,depth) AS (
			SELECT i.id,0
			UNION ALL
			SELECT c.id,sub.depth+1 FROM space_drive_items c JOIN sub ON c.parent_id=sub.id WHERE sub.depth<4096)
		SELECT 1 FROM sub JOIN space_drive_items d ON d.id=sub.id WHERE d.id<>i.id AND d.trash_hold AND d.deleted_at IS NULL)`

func (db *Database) PurgeExpiredDriveTrash(ctx context.Context, limit int) (int, []string, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	type candidate struct{ id, spaceID string }
	candidates := []candidate{}
	retention := leaseInterval(DriveTrashRetention)
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT i.id,i.space_id FROM space_drive_items i WHERE `+driveExpiredTrashCondition+`
			ORDER BY i.trashed_at LIMIT $2`, retention, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.id, &item.spaceID); err != nil {
				return err
			}
			candidates = append(candidates, item)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, nil, err
	}
	purged, retire := 0, []string{}
	for _, item := range candidates {
		err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
			if err := lockDriveTx(ctx, tx, item.spaceID); err != nil {
				return err
			}
			var still bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_drive_items i WHERE i.id=$2 AND `+driveExpiredTrashCondition+`)`,
				retention, item.id).Scan(&still); err != nil || !still {
				return err
			}
			keys, err := purgeDriveSubtreeTx(ctx, tx, item.spaceID, "", item.id)
			if err != nil {
				return err
			}
			retire = append(retire, keys...)
			purged++
			return nil
		})
		if err != nil {
			return purged, retire, err
		}
	}
	return purged, retire, nil
}

func (db *Database) PruneDriveTombstones(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 10000 {
		limit = 1000
	}
	retention := leaseInterval(DriveTombstoneRetention)
	bySpace := map[string][]string{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT space_id,id FROM space_drive_items
			WHERE deleted_at IS NOT NULL AND deleted_at<=NOW()-$1::interval ORDER BY deleted_at LIMIT $2`, retention, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var spaceID, id string
			if err := rows.Scan(&spaceID, &id); err != nil {
				return err
			}
			bySpace[spaceID] = append(bySpace[spaceID], id)
		}
		return rows.Err()
	})
	if err != nil {
		return 0, err
	}
	pruned := 0
	for spaceID, ids := range bySpace {
		err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
			if err := lockDriveTx(ctx, tx, spaceID); err != nil {
				return err
			}
			var count int
			var newest sql.NullInt64
			if err := tx.QueryRowContext(ctx, `WITH gone AS (
					DELETE FROM space_drive_items WHERE id=ANY($1) AND space_id=$2 AND deleted_at IS NOT NULL
					AND deleted_at<=NOW()-$3::interval RETURNING seq)
				SELECT count(*),max(seq) FROM gone`, pq.Array(ids), spaceID, retention).Scan(&count, &newest); err != nil {
				return err
			}
			if newest.Valid {
				if _, err := tx.ExecContext(ctx, `UPDATE space_drives SET drive_tombstone_horizon=GREATEST(drive_tombstone_horizon,$2) WHERE space_id=$1`,
					spaceID, newest.Int64); err != nil {
					return err
				}
			}
			pruned += count
			return nil
		})
		if err != nil {
			return pruned, err
		}
	}
	return pruned, nil
}

func (db *Database) RetireDriveBlobs(ctx context.Context, keys []string) ([]string, error) {
	retired := []string{}
	for start := 0; start < len(keys); start += driveRetireBatch {
		batch := keys[start:min(start+driveRetireBatch, len(keys))]
		var gone []string
		err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `SELECT id FROM library_blobs WHERE r2_object_key=ANY($1) ORDER BY id`, pq.Array(batch))
			if err != nil {
				return err
			}
			ids := []string{}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			gone, err = retireLibraryBlobsIfUnusedTx(ctx, tx, ids)
			return err
		})
		if err != nil {
			return retired, err
		}
		retired = append(retired, gone...)
	}
	return retired, nil
}
