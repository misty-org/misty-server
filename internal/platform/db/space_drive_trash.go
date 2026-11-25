package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	DriveTrashName   = ".trash"
	driveRetireBatch = 32
)

func isRootDriveTrash(parentID *string, name string) bool {
	return parentID == nil && strings.EqualFold(name, DriveTrashName)
}

func driveTrashFolderTx(ctx context.Context, tx *sql.Tx, spaceID string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM space_drive_items
		WHERE space_id=$1 AND parent_id IS NULL AND kind='folder' AND deleted_at IS NULL
		AND misty_drive_name_fold(name)=misty_drive_name_fold($2::text) LIMIT 1`, spaceID, DriveTrashName).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func ensureDriveTrashFolderTx(ctx context.Context, tx *sql.Tx, spaceID, userID string) (string, error) {
	id, err := driveTrashFolderTx(ctx, tx, spaceID)
	if err != nil || id != "" {
		return id, err
	}
	holder, err := driveNameHolderTx(ctx, tx, spaceID, nil, DriveTrashName, "")
	if err != nil {
		return "", err
	}
	if holder != nil {
		name, err := driveFreeNameTx(ctx, tx, spaceID, nil, DriveTrashName+" (renamed)", DriveKindFolder, holder.ID)
		if err != nil {
			return "", err
		}
		seq, err := nextDriveSeqsTx(ctx, tx, spaceID, 1)
		if err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_drive_items SET name=$2,version=version+1,seq=$3 WHERE id=$1`, holder.ID, name, seq); err != nil {
			return "", err
		}
		if err := recordDriveChangedTx(ctx, tx, spaceID, userID, holder.ID, seq); err != nil {
			return "", err
		}
	}
	id = "drv_" + uuid.NewString()
	seq, err := nextDriveSeqsTx(ctx, tx, spaceID, 1)
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_drive_items(id,space_id,parent_id,name,kind,seq,modified_by)
		VALUES($1,$2,NULL,$3,'folder',$4,$5)`, id, spaceID, DriveTrashName, seq, userID); err != nil {
		return "", err
	}
	return id, recordDriveChangedTx(ctx, tx, spaceID, userID, id, seq)
}

func driveTopTx(ctx context.Context, tx *sql.Tx, parentID *string) (string, error) {
	if parentID == nil {
		return "", nil
	}
	var top string
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE up(id,parent_id,depth) AS (
			SELECT i.id,i.parent_id,0 FROM space_drive_items i WHERE i.id=$1
			UNION ALL
			SELECT p.id,p.parent_id,up.depth+1 FROM space_drive_items p JOIN up ON p.id=up.parent_id WHERE up.depth<$2)
		SELECT id FROM up WHERE parent_id IS NULL LIMIT 1`, *parentID, maxDriveDepth).Scan(&top)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return top, err
}

func driveInTrashTx(ctx context.Context, tx *sql.Tx, spaceID string, parentID *string) (bool, error) {
	if parentID == nil {
		return false, nil
	}
	trash, err := driveTrashFolderTx(ctx, tx, spaceID)
	if err != nil || trash == "" {
		return false, err
	}
	top, err := driveTopTx(ctx, tx, parentID)
	return top == trash, err
}

func driveNumberedName(name, kind string, n int) string {
	if n < 2 {
		return name
	}
	base, ext := name, ""
	if kind == driveKindFile {
		if dot := strings.LastIndex(name, "."); dot > 0 {
			base, ext = name[:dot], name[dot:]
		}
	}
	suffix := fmt.Sprintf(" %d", n)
	for len(base)+len(suffix)+len(ext) > 255 && base != "" {
		_, size := utf8.DecodeLastRuneInString(base)
		base = base[:len(base)-size]
	}
	return base + suffix + ext
}

func driveFreeNameTx(ctx context.Context, tx *sql.Tx, spaceID string, parentID *string, name, kind, exceptID string) (string, error) {
	for n := 1; ; n++ {
		candidate := driveNumberedName(name, kind, n)
		holder, err := driveNameHolderTx(ctx, tx, spaceID, parentID, candidate, exceptID)
		if err != nil {
			return "", err
		}
		if holder == nil {
			return candidate, nil
		}
	}
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

func trashDriveContributionsTx(ctx context.Context, tx *sql.Tx, spaceID string, ids []string) error {
	var freed int64
	if err := tx.QueryRowContext(ctx, `WITH moved AS (
		UPDATE space_storage_contributions SET state='trashed',updated_at=NOW()
		WHERE space_id=$1 AND source_kind='drive' AND source_id=ANY($2) AND state IN ('active','recovery') RETURNING logical_bytes)
		SELECT COALESCE(sum(logical_bytes),0) FROM moved`, spaceID, pq.Array(ids)).Scan(&freed); err != nil {
		return err
	}
	if freed == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, freed, spaceID)
	return err
}

func untrashDriveContributionsTx(ctx context.Context, tx *sql.Tx, spaceID string, ids []string) error {
	rows, err := tx.QueryContext(ctx, `SELECT user_id,sum(logical_bytes) FROM space_storage_contributions
		WHERE space_id=$1 AND source_kind='drive' AND source_id=ANY($2) AND state='trashed' GROUP BY user_id`, spaceID, pq.Array(ids))
	if err != nil {
		return err
	}
	owed := map[string]int64{}
	owners := []string{}
	var total int64
	for rows.Next() {
		var owner string
		var bytes int64
		if err := rows.Scan(&owner, &bytes); err != nil {
			rows.Close()
			return err
		}
		owed[owner] = bytes
		owners = append(owners, owner)
		total += bytes
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	sort.Strings(owners)
	for _, owner := range owners {
		if err := lockAccountStorageTx(ctx, tx, owner); err != nil {
			return err
		}
		if err := RequireStorageRoomTx(ctx, tx, owner, spaceID, owed[owner]); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_storage_contributions SET state='active',updated_at=NOW()
		WHERE space_id=$1 AND source_kind='drive' AND source_id=ANY($2) AND state='trashed'`, spaceID, pq.Array(ids)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=used_bytes+$1,version=version+1,updated_at=NOW() WHERE space_id=$2`, total, spaceID)
	return err
}

func markDriveTrashTopTx(ctx context.Context, tx *sql.Tx, itemID, userID string, from *string, name string) error {
	_, err := tx.ExecContext(ctx, `UPDATE space_drive_items SET trashed_at=NOW(),trashed_by=$2,trashed_from=$3,trashed_name=$4 WHERE id=$1`,
		itemID, userID, driveNullable(from), name)
	return err
}

func clearDriveTrashTopTx(ctx context.Context, tx *sql.Tx, itemID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE space_drive_items SET trashed_at=NULL,trashed_by=NULL,trashed_from=NULL,trashed_name=NULL WHERE id=$1`, itemID)
	return err
}

func driveTrashMoveTx(ctx context.Context, tx *sql.Tx, spaceID, userID string, item DriveItem, from, to *string, exceptUserID, exceptDeviceID string) error {
	trash, err := driveTrashFolderTx(ctx, tx, spaceID)
	if err != nil {
		return err
	}
	if trash == "" {
		return nil
	}
	if item.ID == trash {
		return ErrDriveProtected
	}
	fromTop, err := driveTopTx(ctx, tx, from)
	if err != nil {
		return err
	}
	toTop, err := driveTopTx(ctx, tx, to)
	if err != nil {
		return err
	}
	wasIn, nowIn := fromTop == trash, toTop == trash
	if !wasIn && !nowIn {
		return nil
	}
	subtree, err := driveSubtreeTx(ctx, tx, item.ID)
	if err != nil {
		return err
	}
	switch {
	case !wasIn && nowIn:
		who, err := driveInUseHoldersTx(ctx, tx, subtree, exceptUserID, exceptDeviceID)
		if err != nil {
			return err
		}
		if len(who) > 0 {
			return DriveInUseError{Who: who}
		}
		if err := trashDriveContributionsTx(ctx, tx, spaceID, subtree); err != nil {
			return err
		}
	case wasIn && !nowIn:
		if err := untrashDriveContributionsTx(ctx, tx, spaceID, subtree); err != nil {
			return err
		}
	}
	if nowIn && to != nil && *to == trash {
		if wasIn && from != nil && *from == trash {
			return nil
		}
		origin, name := from, item.Name
		if wasIn {
			origin = nil
		}
		return markDriveTrashTopTx(ctx, tx, item.ID, userID, origin, name)
	}
	return clearDriveTrashTopTx(ctx, tx, item.ID)
}

func trashDriveItemTx(ctx context.Context, tx *sql.Tx, spaceID, userID, itemID, exceptUserID, exceptDeviceID string) (int64, error) {
	item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, true)
	if err != nil {
		return 0, err
	}
	if item.Kind == driveKindFile && isRootDriveIgnore(item.ParentID, item.Name) {
		return 0, ErrDriveProtected
	}
	trash, err := ensureDriveTrashFolderTx(ctx, tx, spaceID, userID)
	if err != nil {
		return 0, err
	}
	if item.ID == trash {
		return 0, ErrDriveProtected
	}
	if top, err := driveTopTx(ctx, tx, item.ParentID); err != nil {
		return 0, err
	} else if top == trash {
		return 0, DriveConflictError{Code: "trashed", Item: item}
	}
	if err := driveTrashMoveTx(ctx, tx, spaceID, userID, item, item.ParentID, &trash, exceptUserID, exceptDeviceID); err != nil {
		return 0, err
	}
	name, err := driveFreeNameTx(ctx, tx, spaceID, &trash, item.Name, item.Kind, item.ID)
	if err != nil {
		return 0, err
	}
	seq, err := nextDriveSeqsTx(ctx, tx, spaceID, 1)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE space_drive_items SET parent_id=$2,name=$3,version=version+1,seq=$4 WHERE id=$1`,
		item.ID, trash, name, seq)
	return seq, err
}

func (db *Database) TrashDriveItem(ctx context.Context, userID, spaceID, itemID string, device DriveDevice, ifMatch int64) (*DriveWriteResult, error) {
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
		seq, err := trashDriveItemTx(ctx, tx, spaceID, userID, item.ID, userID, device.ID)
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

type driveTrashOrigin struct {
	from *string
	name string
}

func driveTrashOriginTx(ctx context.Context, tx *sql.Tx, itemID string) (driveTrashOrigin, error) {
	var from, name sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT trashed_from,trashed_name FROM space_drive_items WHERE id=$1`, itemID).Scan(&from, &name); err != nil {
		return driveTrashOrigin{}, err
	}
	origin := driveTrashOrigin{name: name.String}
	if from.Valid {
		origin.from = &from.String
	}
	return origin, nil
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
		trash, err := driveTrashFolderTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if trash == "" || item.ParentID == nil || *item.ParentID != trash {
			return ErrDriveNotTrashed
		}
		origin, err := driveTrashOriginTx(ctx, tx, item.ID)
		if err != nil {
			return err
		}
		target := origin.from
		if target != nil {
			parent, err := liveDriveItemTx(ctx, tx, spaceID, *target, false)
			if errors.Is(err, ErrDriveNotFound) || (err == nil && parent.Kind != DriveKindFolder) {
				target = nil
			} else if err != nil {
				return err
			} else if inTrash, err := driveInTrashTx(ctx, tx, spaceID, target); err != nil {
				return err
			} else if inTrash || parent.ID == trash {
				target = nil
			}
		}
		wanted := origin.name
		if wanted == "" {
			wanted = item.Name
		}
		if item.Kind == driveKindFile && (isRootDriveIgnore(target, wanted) || isRootDriveTrash(target, wanted)) {
			wanted = driveRestoredName(wanted, item.Kind, 1)
		}
		name := wanted
		for n := 1; ; n++ {
			holder, err := driveNameHolderTx(ctx, tx, spaceID, target, name, item.ID)
			if err != nil {
				return err
			}
			if holder == nil {
				break
			}
			name = driveRestoredName(wanted, item.Kind, n)
		}
		if err := driveTrashMoveTx(ctx, tx, spaceID, userID, item, item.ParentID, target, userID, device.ID); err != nil {
			return err
		}
		seq, err := nextDriveSeqsTx(ctx, tx, spaceID, 1)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_drive_items SET parent_id=$2,name=$3,version=version+1,seq=$4,
			modified_by=$5,modified_at=NOW() WHERE id=$1`, item.ID, driveNullable(target), name, seq, userID); err != nil {
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
