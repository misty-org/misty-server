package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/misty-org/misty-server/internal/spaces"
)

type DriveFileRoomAccess struct {
	Item           DriveItem
	CanWrite       bool
	RoomGeneration string
}

func (db *Database) DriveFileRoomAccess(ctx context.Context, userID, spaceID, itemID, appID string) (*DriveFileRoomAccess, error) {
	if !spaceAppIDPattern.MatchString(appID) {
		return nil, ErrSpaceNotFound
	}
	out := &DriveFileRoomAccess{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		generation, err := spaceAppGrantTx(ctx, tx, spaceID, appID, "rooms", "files")
		if err != nil {
			return err
		}
		out.RoomGeneration = generation
		item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, false)
		if err != nil {
			return err
		}
		if item.Kind != driveKindFile || item.Trashed {
			return ErrDriveNotFound
		}
		out.Item = item
		out.CanWrite, err = hasSpacePermissionTx(ctx, tx, userID, spaceID, spaces.PermissionDriveWrite)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (db *Database) CheckDriveFileRoomSave(ctx context.Context, userID, spaceID, itemID string, base, size int64, sha string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		item, err := liveDriveItemTx(ctx, tx, spaceID, itemID, false)
		if err != nil {
			return err
		}
		if item.Kind != driveKindFile || item.Trashed {
			return ErrDriveNotFound
		}
		if size == 0 {
			return nil
		}
		if err := requireSpaceUploadSizeTx(ctx, tx, userID, spaceID, size); err != nil {
			return err
		}
		current := ""
		if item.SHA256 != nil {
			current = *item.SHA256
		}
		counted := item.ID
		if base < item.ContentVersion && sha != current && item.Size > 0 {
			counted = ""
		}
		return requireDriveRoomTx(ctx, tx, userID, spaceID, counted, size, 0)
	})
}

func (db *Database) CreateDriveFileRoomCopy(ctx context.Context, userID, spaceID, itemID string, device DriveDevice, sha string, size int64) (*DriveItem, error) {
	if !ValidDriveDevice(device) || !validDriveContent(sha, size) {
		return nil, ErrLibraryInvalid
	}
	var out DriveItem
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if err := lockDriveTx(ctx, tx, spaceID); err != nil {
			return err
		}
		item, err := driveItemTx(ctx, tx, spaceID, itemID, false)
		if err != nil {
			return err
		}
		if item.Kind != driveKindFile {
			return ErrDriveNotFound
		}
		parentID := item.ParentID
		if parentID != nil {
			parent, err := liveDriveItemTx(ctx, tx, spaceID, *parentID, false)
			if errors.Is(err, ErrDriveNotFound) || (err == nil && (parent.Trashed || parent.Kind != DriveKindFolder)) {
				parentID = nil
			} else if err != nil {
				return err
			}
		}
		name, err := driveReplacedNameTx(ctx, tx, spaceID, parentID, item.Name, time.Now())
		if err != nil {
			return err
		}
		key, held, _, err := resolveDriveContentTx(ctx, tx, userID, spaceID, driveContent{sha: sha, size: size})
		if err != nil {
			return err
		}
		if err := requireDriveRoomTx(ctx, tx, userID, spaceID, "", size, held); err != nil {
			return err
		}
		var blobKey any
		if key != "" {
			blobKey = key
		}
		seq, err := nextDriveSeqsTx(ctx, tx, spaceID, 1)
		if err != nil {
			return err
		}
		id := "drv_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_drive_items(id,space_id,parent_id,name,kind,sha256,byte_size,mtime_ms,blob_key,seq,modified_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			id, spaceID, driveNullable(parentID), name, driveKindFile, sha, size, time.Now().UnixMilli(), blobKey, seq, userID); err != nil {
			return err
		}
		if err := setDriveContributionTx(ctx, tx, spaceID, id, userID, size); err != nil {
			return err
		}
		if err := recordDriveChangedTx(ctx, tx, spaceID, userID, id, seq); err != nil {
			return err
		}
		out, err = driveItemTx(ctx, tx, spaceID, id, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func NewDriveObjectKey() string { return newDriveObjectKey() }

func (db *Database) EnsureDriveBlob(ctx context.Context, spaceID, objectKey, sha string, size int64) (string, error) {
	if !validDriveContent(sha, size) || size == 0 {
		return "", ErrLibraryInvalid
	}
	var key string
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		domainID, err := spaceSecurityDomainTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,client_declared_mime_type,server_detected_mime_type,scan_status,processing_status,lifecycle_state)
			VALUES($1,$2,$3,$4,$5,$6,$6,'skipped','ready','ready')
			ON CONFLICT (security_domain_id,sha256,byte_size) WHERE lifecycle_state<>'deleted' DO NOTHING
			RETURNING r2_object_key`, "blob_"+uuid.NewString(), domainID, objectKey, sha, size, DriveMIMEType).Scan(&key)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		key, err = driveBlobKeyTx(ctx, tx, domainID, sha, size)
		return err
	})
	if err != nil {
		return "", err
	}
	return key, nil
}
