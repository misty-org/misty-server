package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type DriveCreateInput struct {
	UserID, SpaceID string
	Device          DriveDevice
	ID              string
	ParentID        *string
	Name, Kind      string
	SHA256          string
	Size, MTimeMS   int64
	UploadID        string
}

type DriveWriteResult struct {
	Item             DriveItem
	Created          bool
	Revived          bool
	Replaced         *DriveItem
	DeleteObjectKeys []string
	RetireKeys       []string
}

func driveParentTx(ctx context.Context, tx *sql.Tx, spaceID string, parentID *string) (int64, error) {
	if parentID == nil {
		return 0, nil
	}
	parent, err := liveDriveItemTx(ctx, tx, spaceID, *parentID, false)
	if errors.Is(err, ErrDriveNotFound) {
		return 0, ErrDriveParentMissing
	}
	if err != nil {
		return 0, err
	}
	if parent.Kind != DriveKindFolder {
		return 0, ErrDriveParentNotFolder
	}
	return reviveDriveItemTx(ctx, tx, spaceID, parent)
}

func driveNullable(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func sameParent(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (db *Database) CreateDriveItem(ctx context.Context, in DriveCreateInput) (*DriveWriteResult, error) {
	if !ValidDriveDevice(in.Device) {
		return nil, ErrLibraryInvalid
	}
	switch in.Kind {
	case driveKindFile:
		if !validDriveContent(in.SHA256, in.Size) {
			return nil, ErrLibraryInvalid
		}
	case DriveKindFolder:
		if in.SHA256 != "" || in.Size != 0 || in.MTimeMS != 0 || in.UploadID != "" {
			return nil, ErrLibraryInvalid
		}
	default:
		return nil, ErrLibraryInvalid
	}
	if in.ID != "" && !ValidDriveItemID(in.ID) {
		return nil, ErrLibraryInvalid
	}
	result := &DriveWriteResult{Created: true, DeleteObjectKeys: []string{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, in.UserID, in.SpaceID); err != nil {
			return err
		}
		if err := lockDriveTx(ctx, tx, in.SpaceID); err != nil {
			return err
		}
		name, err := normalizeDriveNameTx(ctx, tx, in.Name)
		if err != nil {
			return err
		}
		if isRootDriveIgnore(in.ParentID, name) {
			if err := requireDriveIgnoreEditTx(ctx, tx, in.UserID, in.SpaceID); err != nil {
				return err
			}
		}
		id := in.ID
		if id == "" {
			id = "drv_" + uuid.NewString()
		} else if retried, err := driveCreateRetryTx(ctx, tx, in, name); err != nil || retried != nil {
			if retried != nil {
				result.Item, result.Created = *retried, false
			}
			return err
		}
		revivedSeq, err := driveParentTx(ctx, tx, in.SpaceID, in.ParentID)
		if err != nil {
			return err
		}
		result.Revived = revivedSeq > 0
		holder, err := driveNameHolderTx(ctx, tx, in.SpaceID, in.ParentID, name, "")
		if err != nil {
			return err
		}
		if holder != nil {
			return DriveConflictError{Code: "name_taken", Item: *holder}
		}
		var blobKey any
		var sha any
		if in.Kind == driveKindFile {
			key, held, discard, err := resolveDriveContentTx(ctx, tx, in.UserID, in.SpaceID, driveContent{sha: in.SHA256, size: in.Size, uploadID: in.UploadID})
			if err != nil {
				return err
			}
			result.DeleteObjectKeys = append(result.DeleteObjectKeys, discard...)
			if err := requireDriveRoomTx(ctx, tx, in.UserID, in.SpaceID, "", in.Size, held); err != nil {
				return err
			}
			if key != "" {
				blobKey = key
			}
			sha = in.SHA256
		}
		seq, err := nextDriveSeqsTx(ctx, tx, in.SpaceID, 1)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_drive_items(id,space_id,parent_id,name,kind,sha256,byte_size,mtime_ms,blob_key,seq,modified_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			id, in.SpaceID, driveNullable(in.ParentID), name, in.Kind, sha, in.Size, in.MTimeMS, blobKey, seq, in.UserID); err != nil {
			return err
		}
		if in.Kind == driveKindFile {
			if err := setDriveContributionTx(ctx, tx, in.SpaceID, id, in.UserID, in.Size); err != nil {
				return err
			}
		}
		if err := recordDriveChangedTx(ctx, tx, in.SpaceID, in.UserID, id, seq); err != nil {
			return err
		}
		result.Item, err = driveItemTx(ctx, tx, in.SpaceID, id, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func driveCreateRetryTx(ctx context.Context, tx *sql.Tx, in DriveCreateInput, name string) (*DriveItem, error) {
	var spaceID string
	err := tx.QueryRowContext(ctx, `SELECT space_id FROM space_drive_items WHERE id=$1`, in.ID).Scan(&spaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if spaceID != in.SpaceID {
		return nil, ErrDriveIDTaken
	}
	item, err := driveItemTx(ctx, tx, in.SpaceID, in.ID, false)
	if err != nil {
		return nil, err
	}
	sha := ""
	if item.SHA256 != nil {
		sha = *item.SHA256
	}
	if item.Deleted || !sameParent(item.ParentID, in.ParentID) || item.Name != name || item.Kind != in.Kind || sha != in.SHA256 {
		return nil, ErrDriveIDTaken
	}
	return &item, nil
}

type DrivePatchInput struct {
	UserID, SpaceID, ItemID string
	Device                  DriveDevice
	IfMatch                 int64
	BaseVersion             int64
	Name                    *string
	MoveParent              bool
	ParentID                *string
	Content                 bool
	SHA256                  string
	Size                    int64
	UploadID                string
	MTimeMS                 *int64
	Replace                 bool
}

func driveReplacedNameTx(ctx context.Context, tx *sql.Tx, spaceID string, parentID *string, name string, at time.Time) (string, error) {
	label := "conflicted copy " + at.UTC().Format("2006-01-02 1504")
	parent := ""
	if parentID != nil {
		parent = *parentID
	}
	for n := 1; ; n++ {
		candidate := driveLabeledName(name, driveKindFile, label, n)
		var used bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_drive_items
			WHERE space_id=$1 AND COALESCE(parent_id,'')=$2 AND misty_drive_name_fold(name)=misty_drive_name_fold($3::text) AND deleted_at IS NULL)`,
			spaceID, parent, candidate).Scan(&used); err != nil {
			return "", err
		}
		if !used {
			return candidate, nil
		}
	}
}

func keepReplacedDriveContentTx(ctx context.Context, tx *sql.Tx, spaceID string, item DriveItem) (DriveItem, error) {
	name, err := driveReplacedNameTx(ctx, tx, spaceID, item.ParentID, item.Name, time.Now())
	if err != nil {
		return DriveItem{}, err
	}
	seq, err := nextDriveSeqsTx(ctx, tx, spaceID, 1)
	if err != nil {
		return DriveItem{}, err
	}
	id := "drv_" + uuid.NewString()
	var blobKey any
	if item.BlobKey != "" {
		blobKey = item.BlobKey
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_drive_items(id,space_id,parent_id,name,kind,sha256,byte_size,mtime_ms,blob_key,seq,
			modified_by,modified_at,trashed_at,trashed_by,trash_hold,replaced_of)
		VALUES($1,$2,$3,$4,'file',$5,$6,$7,$8,$9,$10,$11,NULL,NULL,FALSE,$12)`,
		id, spaceID, driveNullable(item.ParentID), name, *item.SHA256, item.Size, item.MTimeMS, blobKey, seq,
		item.ModifiedBy, item.ModifiedAt, item.ID); err != nil {
		return DriveItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_storage_contributions SET source_id=$3,updated_at=NOW()
		WHERE space_id=$1 AND source_kind='drive' AND source_id=$2 AND state IN ('active','recovery')`, spaceID, item.ID, id); err != nil {
		return DriveItem{}, err
	}
	return driveItemTx(ctx, tx, spaceID, id, false)
}

func (db *Database) PatchDriveItem(ctx context.Context, in DrivePatchInput) (*DriveWriteResult, error) {
	if !ValidDriveDevice(in.Device) || in.IfMatch < 0 || in.BaseVersion < 0 ||
		(in.Name == nil && !in.MoveParent && !in.Content && in.MTimeMS == nil) {
		return nil, ErrLibraryInvalid
	}
	result := &DriveWriteResult{Created: false, DeleteObjectKeys: []string{}, RetireKeys: []string{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, in.UserID, in.SpaceID); err != nil {
			return err
		}
		if err := lockDriveTx(ctx, tx, in.SpaceID); err != nil {
			return err
		}
		item, err := liveDriveItemTx(ctx, tx, in.SpaceID, in.ItemID, true)
		if err != nil {
			return err
		}
		if in.IfMatch > 0 && item.Version != in.IfMatch {
			return DriveConflictError{Code: "version_conflict", Item: item}
		}
		if in.Content && in.BaseVersion > item.Version {
			return DriveConflictError{Code: "version_conflict", Item: item}
		}
		if item.Kind == DriveKindFolder && (in.Content || in.MTimeMS != nil) {
			return ErrLibraryInvalid
		}
		seen := in.BaseVersion
		if seen == 0 {
			seen = in.IfMatch
		}
		seenCurrent := seen >= item.ContentVersion
		revivedSeq, err := reviveDriveItemTx(ctx, tx, in.SpaceID, item)
		if err != nil {
			return err
		}
		if revivedSeq > 0 {
			result.Revived = true
			if item, err = liveDriveItemTx(ctx, tx, in.SpaceID, item.ID, true); err != nil {
				return err
			}
		}
		name, parentID := item.Name, item.ParentID
		if in.Name != nil {
			if name, err = normalizeDriveNameTx(ctx, tx, *in.Name); err != nil {
				return err
			}
		}
		if in.MoveParent && !sameParent(in.ParentID, item.ParentID) {
			if in.ParentID != nil {
				if *in.ParentID == item.ID {
					return ErrDriveInvalidMove
				}
				ancestors, err := driveAncestorsTx(ctx, tx, *in.ParentID)
				if err != nil {
					return err
				}
				for _, ancestor := range ancestors {
					if ancestor.ID == item.ID {
						return ErrDriveInvalidMove
					}
				}
			}
			parentRevived, err := driveParentTx(ctx, tx, in.SpaceID, in.ParentID)
			if err != nil {
				return err
			}
			result.Revived = result.Revived || parentRevived > 0
			parentID = in.ParentID
		}
		wasIgnore := item.Kind == driveKindFile && isRootDriveIgnore(item.ParentID, item.Name)
		becomesIgnore := item.Kind == driveKindFile && isRootDriveIgnore(parentID, name)
		if wasIgnore && !becomesIgnore {
			return ErrDriveProtected
		}
		if becomesIgnore && (!wasIgnore || in.Content) {
			if err := requireDriveIgnoreEditTx(ctx, tx, in.UserID, in.SpaceID); err != nil {
				return err
			}
		}
		if name != item.Name || !sameParent(parentID, item.ParentID) {
			holder, err := driveNameHolderTx(ctx, tx, in.SpaceID, parentID, name, item.ID)
			if err != nil {
				return err
			}
			if holder != nil {
				if in.Replace && holder.Kind == driveKindFile && isRootDriveIgnore(holder.ParentID, holder.Name) {
					return ErrDriveProtected
				}
				if !in.Replace {
					return DriveConflictError{Code: "name_taken", Item: *holder}
				}
				if _, err := liveDriveItemTx(ctx, tx, in.SpaceID, holder.ID, true); err != nil {
					return err
				}
				if _, err := trashDriveItemTx(ctx, tx, in.SpaceID, in.UserID, holder.ID, "", "", false); err != nil {
					return err
				}
			}
		}
		sha, size, blobKey, mtime := "", item.Size, item.BlobKey, item.MTimeMS
		if item.SHA256 != nil {
			sha = *item.SHA256
		}
		if in.MTimeMS != nil {
			mtime = *in.MTimeMS
		} else if in.Content {
			mtime = time.Now().UnixMilli()
		}
		contentChanged, newContent := false, false
		if in.Content {
			key, held, discard, err := resolveDriveContentTx(ctx, tx, in.UserID, in.SpaceID, driveContent{sha: in.SHA256, size: in.Size, uploadID: in.UploadID})
			if err != nil {
				return err
			}
			result.DeleteObjectKeys = append(result.DeleteObjectKeys, discard...)
			keep := !seenCurrent && in.SHA256 != sha && item.Size > 0
			counted := item.ID
			if keep {
				counted = ""
			}
			if err := requireDriveRoomTx(ctx, tx, in.UserID, in.SpaceID, counted, in.Size, held); err != nil {
				return err
			}
			if keep {
				replaced, err := keepReplacedDriveContentTx(ctx, tx, in.SpaceID, item)
				if err != nil {
					return err
				}
				result.Replaced = &replaced
			} else if item.BlobKey != "" && item.BlobKey != key {
				result.RetireKeys = append(result.RetireKeys, item.BlobKey)
			}
			newContent = in.SHA256 != sha
			sha, size, blobKey, contentChanged = in.SHA256, in.Size, key, true
		}
		seq, err := nextDriveSeqsTx(ctx, tx, in.SpaceID, 1)
		if err != nil {
			return err
		}
		var shaValue, keyValue any
		if item.Kind == driveKindFile {
			shaValue = sha
		}
		if blobKey != "" {
			keyValue = blobKey
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_drive_items SET name=$2,parent_id=$3,sha256=$4,byte_size=$5,mtime_ms=$6,blob_key=$7,
			version=version+1,content_version=CASE WHEN $10 THEN version+1 ELSE content_version END,
			seq=$8,modified_by=$9,modified_at=NOW() WHERE id=$1`,
			item.ID, name, driveNullable(parentID), shaValue, size, mtime, keyValue, seq, in.UserID, newContent); err != nil {
			return err
		}
		if contentChanged {
			if err := setDriveContributionTx(ctx, tx, in.SpaceID, item.ID, in.UserID, size); err != nil {
				return err
			}
		}
		if err := recordDriveChangedTx(ctx, tx, in.SpaceID, in.UserID, item.ID, seq); err != nil {
			return err
		}
		result.Item, err = driveItemTx(ctx, tx, in.SpaceID, item.ID, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
