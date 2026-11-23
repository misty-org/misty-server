package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/misty-org/misty-server/internal/spaces"
)

const (
	DriveInUseLease         = 120 * time.Second
	DriveTrashRetention     = 30 * 24 * time.Hour
	DriveTombstoneRetention = 90 * 24 * time.Hour
	MaxDriveFileBytes       = int64(5_000_000_000_000)
	MaxDriveChangesPage     = 1000
	DriveMIMEType           = "application/octet-stream"
	EmptyFileSHA256         = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	maxDriveDepth           = 4096
	driveKindFile           = "file"
	DriveKindFolder         = "folder"
)

var (
	driveItemIDPattern   = regexp.MustCompile(`^drv_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	driveStoredIDPattern = regexp.MustCompile(`^drv_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	driveDeviceIDPattern = regexp.MustCompile(`^dev_[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

var (
	ErrDriveNotFound         = errors.New("drive item not found")
	ErrDriveParentMissing    = errors.New("drive parent not found")
	ErrDriveParentNotFolder  = errors.New("drive parent is not a folder")
	ErrDriveInvalidMove      = errors.New("drive item cannot move into itself")
	ErrDriveNotTrashed       = errors.New("drive item is not in the trash")
	ErrDriveBlobMissing      = errors.New("drive content is not on the server")
	ErrDriveUploadIncomplete = errors.New("drive upload has not landed")
	ErrDriveResyncRequired   = errors.New("drive changes since that point were pruned")
	ErrDriveIDTaken          = errors.New("drive item id is taken")
)

type DriveConflictError struct {
	Code string
	Item DriveItem
}

func (e DriveConflictError) Error() string { return "drive " + e.Code + ": " + e.Item.ID }

type DriveInUseHolder struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	DeviceName string `json:"device_name"`
	Editing    bool   `json:"editing"`
}

type DriveInUseError struct{ Who []DriveInUseHolder }

func (e DriveInUseError) Error() string { return fmt.Sprintf("drive item in use by %d", len(e.Who)) }

type DriveDevice struct {
	ID, Name string
}

func ValidDriveDevice(device DriveDevice) bool {
	if !driveDeviceIDPattern.MatchString(device.ID) || !utf8.ValidString(device.Name) {
		return false
	}
	if count := utf8.RuneCountInString(device.Name); count < 1 || count > 64 || strings.TrimSpace(device.Name) == "" {
		return false
	}
	for _, r := range device.Name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func ValidDriveItemID(id string) bool { return driveItemIDPattern.MatchString(id) }

type DriveItem struct {
	ID             string     `json:"id"`
	SpaceID        string     `json:"space_id"`
	ParentID       *string    `json:"parent_id"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	SHA256         *string    `json:"sha256"`
	Size           int64      `json:"size"`
	MTimeMS        int64      `json:"mtime_ms"`
	Version        int64      `json:"version"`
	Seq            int64      `json:"seq"`
	ModifiedBy     string     `json:"modified_by"`
	ModifiedByName string     `json:"modified_by_name"`
	ModifiedAt     time.Time  `json:"modified_at"`
	Trashed        bool       `json:"trashed"`
	TrashedAt      *time.Time `json:"trashed_at"`
	TrashedBy      *string    `json:"trashed_by"`
	TrashHold      bool       `json:"trash_hold"`
	Deleted        bool       `json:"deleted"`
	ReplacedOf     *string    `json:"replaced_of"`
	BlobKey        string     `json:"-"`
	ContentVersion int64      `json:"-"`
}

const driveItemSelect = `SELECT i.id,i.space_id,i.parent_id,i.name,i.kind,i.sha256,i.byte_size,i.mtime_ms,i.version,i.seq,
	i.modified_by,COALESCE(u.username,''),i.modified_at,i.trashed_at,i.trashed_by,i.trash_hold,i.deleted_at IS NOT NULL,COALESCE(i.blob_key,''),i.replaced_of,i.content_version
	FROM space_drive_items i LEFT JOIN users u ON u.id=i.modified_by`

func scanDriveItem(row interface{ Scan(...any) error }) (DriveItem, error) {
	var item DriveItem
	var parent, sha, trashedBy, replacedOf sql.NullString
	var trashedAt sql.NullTime
	err := row.Scan(&item.ID, &item.SpaceID, &parent, &item.Name, &item.Kind, &sha, &item.Size, &item.MTimeMS,
		&item.Version, &item.Seq, &item.ModifiedBy, &item.ModifiedByName, &item.ModifiedAt, &trashedAt, &trashedBy,
		&item.TrashHold, &item.Deleted, &item.BlobKey, &replacedOf, &item.ContentVersion)
	if err != nil {
		return item, err
	}
	if parent.Valid {
		item.ParentID = &parent.String
	}
	if sha.Valid {
		item.SHA256 = &sha.String
	}
	if trashedAt.Valid {
		at := trashedAt.Time
		item.TrashedAt, item.Trashed = &at, true
	}
	if trashedBy.Valid {
		item.TrashedBy = &trashedBy.String
	}
	if replacedOf.Valid {
		item.ReplacedOf = &replacedOf.String
	}
	return item, nil
}

func scanDriveItems(rows *sql.Rows) ([]DriveItem, error) {
	defer rows.Close()
	out := []DriveItem{}
	for rows.Next() {
		item, err := scanDriveItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func driveItemTx(ctx context.Context, tx *sql.Tx, spaceID, itemID string, lock bool) (DriveItem, error) {
	if !driveStoredIDPattern.MatchString(itemID) {
		return DriveItem{}, ErrDriveNotFound
	}
	query := driveItemSelect + ` WHERE i.id=$1 AND i.space_id=$2`
	if lock {
		query += ` FOR UPDATE OF i`
	}
	item, err := scanDriveItem(tx.QueryRowContext(ctx, query, itemID, spaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return DriveItem{}, ErrDriveNotFound
	}
	return item, err
}

func liveDriveItemTx(ctx context.Context, tx *sql.Tx, spaceID, itemID string, lock bool) (DriveItem, error) {
	item, err := driveItemTx(ctx, tx, spaceID, itemID, lock)
	if err == nil && item.Deleted {
		return DriveItem{}, ErrDriveNotFound
	}
	return item, err
}

func normalizeDriveNameTx(ctx context.Context, tx *sql.Tx, raw string) (string, error) {
	if raw == "" || len(raw) > 1024 || !utf8.ValidString(raw) || strings.ContainsAny(raw, "/\\") {
		return "", ErrLibraryInvalid
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", ErrLibraryInvalid
		}
	}
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT normalize($1::text, NFC)`, raw).Scan(&name); err != nil {
		return "", err
	}
	if len(name) < 1 || len(name) > 255 || name == "." || name == ".." {
		return "", ErrLibraryInvalid
	}
	return name, nil
}

func lockDriveTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_drives(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID); err != nil {
		return err
	}
	var seq int64
	return tx.QueryRowContext(ctx, `SELECT drive_seq FROM space_drives WHERE space_id=$1 FOR UPDATE`, spaceID).Scan(&seq)
}

func nextDriveSeqsTx(ctx context.Context, tx *sql.Tx, spaceID string, n int) (int64, error) {
	var last int64
	if err := tx.QueryRowContext(ctx, `UPDATE space_drives SET drive_seq=drive_seq+$2 WHERE space_id=$1 RETURNING drive_seq`, spaceID, n).Scan(&last); err != nil {
		return 0, err
	}
	return last - int64(n) + 1, nil
}

func recordDriveChangedTx(ctx context.Context, tx *sql.Tx, spaceID, userID, itemID string, seq int64) error {
	_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "drive.changed", itemID, map[string]any{"seq": seq})
	return err
}

func driveAncestorsTx(ctx context.Context, tx *sql.Tx, itemID string) ([]DriveItem, error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE up(id,parent_id,depth) AS (
			SELECT i.id,i.parent_id,0 FROM space_drive_items i WHERE i.id=$1
			UNION ALL
			SELECT p.id,p.parent_id,up.depth+1 FROM space_drive_items p JOIN up ON p.id=up.parent_id WHERE up.depth<$2)
		`+strings.Replace(driveItemSelect, "FROM space_drive_items i", "FROM up JOIN space_drive_items i ON i.id=up.id", 1)+`
		WHERE up.depth>0 ORDER BY up.depth`, itemID, maxDriveDepth)
	if err != nil {
		return nil, err
	}
	return scanDriveItems(rows)
}

func driveHiddenTx(ctx context.Context, tx *sql.Tx, item DriveItem) (bool, error) {
	if item.Trashed {
		return true, nil
	}
	ancestors, err := driveAncestorsTx(ctx, tx, item.ID)
	if err != nil {
		return false, err
	}
	for _, ancestor := range ancestors {
		if ancestor.Trashed {
			return true, nil
		}
	}
	return false, nil
}

func driveSubtreeTx(ctx context.Context, tx *sql.Tx, itemID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE sub(id,depth) AS (
			SELECT $1::text,0
			UNION ALL
			SELECT c.id,sub.depth+1 FROM space_drive_items c JOIN sub ON c.parent_id=sub.id WHERE sub.depth<$2)
		SELECT id FROM sub ORDER BY depth,id`, itemID, maxDriveDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func driveNameHolderTx(ctx context.Context, tx *sql.Tx, spaceID string, parentID *string, name, exceptID string) (*DriveItem, error) {
	parent := ""
	if parentID != nil {
		parent = *parentID
	}
	item, err := scanDriveItem(tx.QueryRowContext(ctx, driveItemSelect+`
		WHERE i.space_id=$1 AND COALESCE(i.parent_id,'')=$2 AND lower(i.name)=lower($3::text) AND i.id<>$4
		AND i.trashed_at IS NULL AND i.deleted_at IS NULL LIMIT 1`, spaceID, parent, name, exceptID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func requireDriveWriteTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	return requireSpacePermissionTx(ctx, tx, userID, spaceID, spaces.PermissionDriveWrite)
}

func (db *Database) DriveItem(ctx context.Context, userID, spaceID, itemID string) (*DriveItem, error) {
	var item DriveItem
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var err error
		item, err = driveItemTx(ctx, tx, spaceID, itemID, false)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &item, nil
}

type DriveChangesPage struct {
	Items     []DriveItem `json:"items"`
	NextSince int64       `json:"next_since"`
	More      bool        `json:"more"`
	Head      int64       `json:"head"`
}

func (db *Database) DriveChanges(ctx context.Context, userID, spaceID string, since int64, limit int) (*DriveChangesPage, error) {
	if since < 0 {
		return nil, ErrLibraryInvalid
	}
	if limit < 1 || limit > MaxDriveChangesPage {
		limit = MaxDriveChangesPage
	}
	page := &DriveChangesPage{Items: []DriveItem{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var horizon int64
		err := tx.QueryRowContext(ctx, `SELECT drive_seq,drive_tombstone_horizon FROM space_drives WHERE space_id=$1`, spaceID).Scan(&page.Head, &horizon)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if since > 0 && since < horizon {
			return ErrDriveResyncRequired
		}
		rows, err := tx.QueryContext(ctx, driveItemSelect+` WHERE i.space_id=$1 AND i.seq>$2 AND i.seq<=$3 ORDER BY i.seq LIMIT $4`,
			spaceID, since, page.Head, limit+1)
		if err != nil {
			return err
		}
		if page.Items, err = scanDriveItems(rows); err != nil {
			return err
		}
		page.NextSince = max(since, page.Head)
		if len(page.Items) > limit {
			page.Items, page.More = page.Items[:limit], true
			page.NextSince = page.Items[limit-1].Seq
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

type DriveTrashPage struct {
	Items     []DriveItem `json:"items"`
	NextAfter string      `json:"next_after"`
}

func (db *Database) DriveTrash(ctx context.Context, userID, spaceID, after string, limit int) (*DriveTrashPage, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	afterAt, afterID := int64(1<<62), "~"
	if after != "" {
		at, id, ok := strings.Cut(after, ":")
		micros, err := strconv.ParseInt(at, 10, 64)
		if !ok || err != nil || !driveStoredIDPattern.MatchString(id) {
			return nil, ErrLibraryInvalid
		}
		afterAt, afterID = micros, id
	}
	page := &DriveTrashPage{Items: []DriveItem{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, driveItemSelect+`
			WHERE i.space_id=$1 AND i.trashed_at IS NOT NULL AND i.deleted_at IS NULL
			AND (floor(extract(epoch FROM i.trashed_at)*1000000)::bigint,i.id) < ($2,$3)
			AND NOT EXISTS (
				WITH RECURSIVE up(parent_id,depth) AS (
					SELECT i.parent_id,0
					UNION ALL
					SELECT p.parent_id,up.depth+1 FROM space_drive_items p JOIN up ON p.id=up.parent_id WHERE up.depth<$5)
				SELECT 1 FROM up JOIN space_drive_items a ON a.id=up.parent_id WHERE a.trashed_at IS NOT NULL)
			ORDER BY i.trashed_at DESC,i.id DESC LIMIT $4`, spaceID, afterAt, afterID, limit+1, maxDriveDepth)
		if err != nil {
			return err
		}
		if page.Items, err = scanDriveItems(rows); err != nil {
			return err
		}
		if len(page.Items) > limit {
			page.Items = page.Items[:limit]
			last := page.Items[limit-1]
			page.NextAfter = fmt.Sprintf("%d:%s", last.TrashedAt.UnixMicro(), last.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}
