package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// In-use marks: who has a drive item open, on which computer, and whether
// they are editing it there (only for showing who is editing). A mark is a
// lease that expires on its own unless its computer renews it, so a crashed or
// offline computer never holds a file for long. Being in use never stops
// anyone from opening, editing or writing a file, on any number of their
// computers; it only refuses moving it (or a folder holding it) to the trash.
// Marks change under the Space's drive lock, so a trash move sees every mark
// committed before it.

// DriveInUseMark is one computer's unexpired mark on an item, as every member
// of the Space sees it: who, on which of their computers, and whether they
// edit it there.
type DriveInUseMark struct {
	ItemID     string    `json:"item_id"`
	UserID     string    `json:"user_id"`
	Name       string    `json:"name"`
	DeviceID   string    `json:"device_id"`
	DeviceName string    `json:"device_name"`
	Editing    bool      `json:"editing"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// DriveInUseMarks lists every unexpired mark in the Space, to any member, so
// each computer can show who else has a file open.
func (db *Database) DriveInUseMarks(ctx context.Context, userID, spaceID string) ([]DriveInUseMark, error) {
	out := []DriveInUseMark{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT m.item_id,m.user_id,COALESCE(u.username,''),m.device_id,m.device_name,m.editing,m.expires_at
			FROM space_drive_in_use m
			JOIN space_drive_items i ON i.id=m.item_id AND i.space_id=m.space_id AND i.deleted_at IS NULL
			LEFT JOIN users u ON u.id=m.user_id
			WHERE m.space_id=$1 AND m.expires_at>NOW()
			ORDER BY m.item_id,3,m.device_name,m.device_id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var mark DriveInUseMark
			if err := rows.Scan(&mark.ItemID, &mark.UserID, &mark.Name, &mark.DeviceID, &mark.DeviceName, &mark.Editing, &mark.ExpiresAt); err != nil {
				return err
			}
			out = append(out, mark)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// recordDriveInUseChangedTx tells every member that who has an item open
// changed (drive.in_use), so their computers fetch the Space's marks again.
func recordDriveInUseChangedTx(ctx context.Context, tx *sql.Tx, spaceID, userID, itemID string) error {
	_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "drive.in_use", itemID, map[string]any{"item_id": itemID})
	return err
}

// driveInUseMarkTx reads this computer's own mark on an item, if it has one.
// Its expired marks were cleared by driveInUseItemTx, so a mark found is live.
func driveInUseMarkTx(ctx context.Context, tx *sql.Tx, itemID, userID, deviceID string) (found bool, deviceName string, editing bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT device_name,editing FROM space_drive_in_use WHERE item_id=$1 AND user_id=$2 AND device_id=$3`,
		itemID, userID, deviceID).Scan(&deviceName, &editing)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", false, nil
	}
	return err == nil, deviceName, editing, err
}

// DriveInUseLeaseResult is a mark's new expiry.
type DriveInUseLeaseResult struct {
	ExpiresAt time.Time `json:"expires_at"`
}

// driveInUseHoldersTx lists who has any of these items open now, one entry
// per computer, leaving out the asking computer itself (userID and deviceID):
// it knows what it has open, and is the one asking to trash it. Empty ids
// leave no one out.
func driveInUseHoldersTx(ctx context.Context, tx *sql.Tx, itemIDs []string, userID, deviceID string) ([]DriveInUseHolder, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.user_id,COALESCE(u.username,''),max(m.device_name),bool_or(m.editing)
		FROM space_drive_in_use m LEFT JOIN users u ON u.id=m.user_id
		WHERE m.item_id=ANY($1) AND m.expires_at>NOW() AND NOT (m.user_id=$2 AND m.device_id=$3)
		GROUP BY m.user_id,u.username,m.device_id ORDER BY 2,3`, pq.Array(itemIDs), userID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DriveInUseHolder{}
	for rows.Next() {
		var holder DriveInUseHolder
		if err := rows.Scan(&holder.UserID, &holder.Name, &holder.DeviceName, &holder.Editing); err != nil {
			return nil, err
		}
		out = append(out, holder)
	}
	return out, rows.Err()
}

// driveInUseItemTx opens an in-use call: drive.write, the drive lock, and the
// live item.
func driveInUseItemTx(ctx context.Context, tx *sql.Tx, userID, spaceID, itemID string) (DriveItem, error) {
	if err := requireDriveWriteTx(ctx, tx, userID, spaceID); err != nil {
		return DriveItem{}, err
	}
	if err := lockDriveTx(ctx, tx, spaceID); err != nil {
		return DriveItem{}, err
	}
	item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, false)
	if err != nil {
		return DriveItem{}, err
	}
	// Expired marks of the person's are cleared, so a renewal after a lapse
	// is heard as a new mark.
	_, err = tx.ExecContext(ctx, `DELETE FROM space_drive_in_use WHERE item_id=$1 AND user_id=$2 AND expires_at<=NOW()`, item.ID, userID)
	return item, err
}

// MarkDriveInUse marks an item open on this computer, or renews the mark,
// saying whether it is being edited there; it is never refused for editing. A
// new mark, or one renewed into another state, is heard as drive.in_use; a
// renewal that changes nothing is not.
func (db *Database) MarkDriveInUse(ctx context.Context, userID, spaceID, itemID string, device DriveDevice, editing bool) (*DriveInUseLeaseResult, error) {
	if !ValidDriveDevice(device) {
		return nil, ErrLibraryInvalid
	}
	out := &DriveInUseLeaseResult{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		item, err := driveInUseItemTx(ctx, tx, userID, spaceID, itemID)
		if err != nil {
			return err
		}
		found, oldName, oldEditing, err := driveInUseMarkTx(ctx, tx, item.ID, userID, device.ID)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_drive_in_use(item_id,space_id,user_id,device_id,device_name,editing,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,NOW()+$7::interval)
			ON CONFLICT (item_id,user_id,device_id) DO UPDATE SET device_name=EXCLUDED.device_name,editing=EXCLUDED.editing,
				expires_at=EXCLUDED.expires_at,updated_at=NOW()
			RETURNING expires_at`, item.ID, spaceID, userID, device.ID, device.Name, editing, leaseInterval(DriveInUseLease)).Scan(&out.ExpiresAt); err != nil {
			return err
		}
		if found && oldEditing == editing && oldName == device.Name {
			return nil
		}
		return recordDriveInUseChangedTx(ctx, tx, spaceID, userID, item.ID)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ClearDriveInUse removes this computer's mark on an item, heard as
// drive.in_use when there was one.
func (db *Database) ClearDriveInUse(ctx context.Context, userID, spaceID, itemID string, device DriveDevice) error {
	if !ValidDriveDevice(device) {
		return ErrLibraryInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		item, err := driveInUseItemTx(ctx, tx, userID, spaceID, itemID)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM space_drive_in_use WHERE item_id=$1 AND user_id=$2 AND device_id=$3`, item.ID, userID, device.ID)
		if err != nil {
			return err
		}
		if removed, err := result.RowsAffected(); err != nil || removed == 0 {
			return err
		}
		return recordDriveInUseChangedTx(ctx, tx, spaceID, userID, item.ID)
	})
}

// AskDriveInUse asks every computer of the Space's members whether it has an
// item open (drive.in_use_check). Each answers by marking the item in use, so
// a trash move sent after the wait sees their answers.
func (db *Database) AskDriveInUse(ctx context.Context, userID, spaceID, itemID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, false)
		if err != nil {
			return err
		}
		_, err = recordSpaceEventTx(ctx, tx, spaceID, userID, "drive.in_use_check", item.ID, map[string]any{"item_id": item.ID})
		return err
	})
}

// PruneExpiredDriveInUse removes marks that expired more than an hour ago;
// readers already ignore them.
func (db *Database) PruneExpiredDriveInUse(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 10000 {
		limit = 1000
	}
	var n int64
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM space_drive_in_use WHERE ctid IN (
			SELECT ctid FROM space_drive_in_use WHERE expires_at<NOW()-interval '1 hour' LIMIT $1 FOR UPDATE SKIP LOCKED)`, limit)
		if err != nil {
			return err
		}
		n, err = result.RowsAffected()
		return err
	})
	return int(n), err
}

// leaseInterval writes a duration as a Postgres interval.
func leaseInterval(d time.Duration) string {
	return fmt.Sprintf("%d milliseconds", d.Milliseconds())
}
