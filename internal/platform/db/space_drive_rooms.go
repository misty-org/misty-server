package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/misty-org/misty-server/internal/spaces"
)

type DriveFileRoomAccess struct {
	Item     DriveItem
	CanWrite bool
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
		var permissions []string
		err := tx.QueryRowContext(ctx, `SELECT permissions FROM space_apps WHERE space_id=$1 AND app_id=$2`,
			spaceID, appID).Scan(pq.Array(&permissions))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceNotFound
		}
		if err != nil {
			return err
		}
		rooms := false
		for _, permission := range permissions {
			rooms = rooms || permission == "rooms"
		}
		if !rooms {
			return ErrSpaceForbidden
		}
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
