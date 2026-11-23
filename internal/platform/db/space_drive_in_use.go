package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

type DriveInUseMark struct {
	ItemID     string    `json:"item_id"`
	UserID     string    `json:"user_id"`
	Name       string    `json:"name"`
	DeviceID   string    `json:"device_id"`
	DeviceName string    `json:"device_name"`
	Editing    bool      `json:"editing"`
	ExpiresAt  time.Time `json:"expires_at"`
}

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

func recordDriveInUseChangedTx(ctx context.Context, tx *sql.Tx, spaceID, userID, itemID string) error {
	_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "drive.in_use", itemID, map[string]any{"item_id": itemID})
	return err
}

func driveInUseMarkTx(ctx context.Context, tx *sql.Tx, itemID, userID, deviceID string) (found bool, deviceName string, editing bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT device_name,editing FROM space_drive_in_use WHERE item_id=$1 AND user_id=$2 AND device_id=$3`,
		itemID, userID, deviceID).Scan(&deviceName, &editing)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", false, nil
	}
	return err == nil, deviceName, editing, err
}

type DriveInUseLeaseResult struct {
	ExpiresAt time.Time `json:"expires_at"`
}

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
	_, err = tx.ExecContext(ctx, `DELETE FROM space_drive_in_use WHERE item_id=$1 AND user_id=$2 AND expires_at<=NOW()`, item.ID, userID)
	return item, err
}

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

func leaseInterval(d time.Duration) string {
	return fmt.Sprintf("%d milliseconds", d.Milliseconds())
}
