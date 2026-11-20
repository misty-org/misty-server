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

// The Space drive: each Space's files as a small file system whose one
// authority is this server. Every change to an item is one transaction that
// takes the Space's drive lock (its space_drives row), locks the item, checks
// the version the sender last saw when it names one, applies the change, bumps
// the version and stamps every row it touched with the next numbers of the
// Space's change sequence. Because the drive lock is held until commit, changes
// commit in sequence order, so a reader of the changes since some number never
// skips one. The last write wins and nothing is lost: a save over content its
// sender never saw keeps that content as a trashed copy, and a write to a
// trashed item brings it back. The one refusal is a trash move while someone
// has the item open. Trashed items keep their rows; purged ones leave
// tombstones the change feed carries until they are pruned after 90 days.
// Members read everything; drive.write changes and trashes, drive.purge
// deletes for good.

const (
	// DriveInUseLease is how long an in-use mark lasts unless renewed.
	DriveInUseLease = 120 * time.Second
	// DriveTrashRetention is how long the trash keeps an item that isn't held.
	DriveTrashRetention = 30 * 24 * time.Hour
	// DriveTombstoneRetention is how long a purged item's tombstone stays in the change feed.
	DriveTombstoneRetention = 90 * 24 * time.Hour
	// MaxDriveFileBytes is the drive's bound on one file, below the object
	// store's own; the storage limits usually decide first.
	MaxDriveFileBytes = int64(5_000_000_000_000)
	// MaxDriveChangesPage bounds one page of changes.
	MaxDriveChangesPage = 1000
	// DriveMIMEType is what drive blobs are stored and served as.
	DriveMIMEType = "application/octet-stream"
	// EmptyFileSHA256 is the digest of no bytes. An empty file has no blob.
	EmptyFileSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	// maxDriveDepth guards every walk up or down the tree.
	maxDriveDepth = 4096
	driveKindFile = "file"
	// DriveKindFolder is a folder item; every other item is a file.
	DriveKindFolder = "folder"
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

// DriveConflictError refuses a change against the item as it is now. Code is
// version_conflict, trashed (a trash move of what is already in the trash) or
// name_taken; Item is the current item (for name_taken, the sibling that
// holds the name).
type DriveConflictError struct {
	Code string
	Item DriveItem
}

func (e DriveConflictError) Error() string { return "drive " + e.Code + ": " + e.Item.ID }

// DriveInUseHolder is one computer that has an item (or something in a
// folder) open.
type DriveInUseHolder struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	DeviceName string `json:"device_name"`
	Editing    bool   `json:"editing"`
}

// DriveInUseError refuses a trash move (or a rename that would trash the
// item holding the name) while someone has the item open.
type DriveInUseError struct{ Who []DriveInUseHolder }

func (e DriveInUseError) Error() string { return fmt.Sprintf("drive item in use by %d", len(e.Who)) }

// DriveDevice is the computer a request comes from: X-Misty-Device and
// X-Misty-Device-Name.
type DriveDevice struct {
	ID, Name string
}

// ValidDriveDevice checks a device id (dev_ and a lowercase uuid) and a
// decoded device name (1-64 characters, no control characters).
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

// ValidDriveItemID accepts a client-made item id: drv_ and a lowercase uuid v4.
func ValidDriveItemID(id string) bool { return driveItemIDPattern.MatchString(id) }

// DriveItem is one file or folder as the API shows it. Folders carry no
// digest, size or mtime. A tombstone (Deleted) is a purged item. ReplacedOf
// marks a trashed copy of content a save replaced: the item it was replaced in.
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
	// BlobKey is the object holding a file's bytes; empty for an empty file,
	// a folder or a tombstone. It never reaches the client.
	BlobKey string `json:"-"`
	// ContentVersion is the version at which the content last changed, so a
	// rename, move or trash never counts as content a save's sender missed.
	ContentVersion int64 `json:"-"`
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

// driveItemTx reads one item of the Space, tombstones included; lock takes it
// for update.
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

// liveDriveItemTx is driveItemTx for an item that isn't a tombstone.
func liveDriveItemTx(ctx context.Context, tx *sql.Tx, spaceID, itemID string, lock bool) (DriveItem, error) {
	item, err := driveItemTx(ctx, tx, spaceID, itemID, lock)
	if err == nil && item.Deleted {
		return DriveItem{}, ErrDriveNotFound
	}
	return item, err
}

// normalizeDriveNameTx checks a name and returns its NFC form (Postgres does
// the normalizing): 1-255 bytes, no slash, backslash, NUL or control
// character, and not "." or "..".
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

// lockDriveTx takes the Space's drive lock, making its drive row first. Every
// drive write takes it before any item lock.
func lockDriveTx(ctx context.Context, tx *sql.Tx, spaceID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_drives(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID); err != nil {
		return err
	}
	var seq int64
	return tx.QueryRowContext(ctx, `SELECT drive_seq FROM space_drives WHERE space_id=$1 FOR UPDATE`, spaceID).Scan(&seq)
}

// nextDriveSeqsTx takes n numbers of the Space's change sequence and returns
// the first. The caller holds the drive lock.
func nextDriveSeqsTx(ctx context.Context, tx *sql.Tx, spaceID string, n int) (int64, error) {
	var last int64
	if err := tx.QueryRowContext(ctx, `UPDATE space_drives SET drive_seq=drive_seq+$2 WHERE space_id=$1 RETURNING drive_seq`, spaceID, n).Scan(&last); err != nil {
		return 0, err
	}
	return last - int64(n) + 1, nil
}

// recordDriveChangedTx tells the Space's members to read the changes up to seq.
func recordDriveChangedTx(ctx context.Context, tx *sql.Tx, spaceID, userID, itemID string, seq int64) error {
	_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "drive.changed", itemID, map[string]any{"seq": seq})
	return err
}

// driveAncestorsTx lists an item's ancestors, nearest first, with whether
// each is trashed.
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

// driveHiddenTx says whether an item is trashed or inside a trashed folder.
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

// driveSubtreeTx lists an item and everything under it, parents before children.
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

// driveNameHolderTx finds the live sibling (other than exceptID) that holds a
// name in a folder, ignoring case.
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

// requireDriveWriteTx checks drive.write.
func requireDriveWriteTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	return requireSpacePermissionTx(ctx, tx, userID, spaceID, spaces.PermissionDriveWrite)
}

// DriveItem returns one item of the Space, a tombstone included, to any member.
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

// DriveChangesPage is one page of the change feed. NextSince is where the
// next page starts; Head is the Space's newest sequence number.
type DriveChangesPage struct {
	Items     []DriveItem `json:"items"`
	NextSince int64       `json:"next_since"`
	More      bool        `json:"more"`
	Head      int64       `json:"head"`
}

// DriveChanges returns the items changed after since, in sequence order,
// tombstones included; since 0 is everything the drive keeps. A cursor older
// than the Space's tombstone horizon may have missed a purge, so it gets
// ErrDriveResyncRequired.
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
		// The head is read first: every change up to it has committed, so the
		// items read next hold all of them.
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

// DriveTrashPage is one page of the Space's trash.
type DriveTrashPage struct {
	Items     []DriveItem `json:"items"`
	NextAfter string      `json:"next_after"`
}

// DriveTrash lists the Space's top-level trashed items, newest first: those
// not inside another trashed folder. after is the previous page's NextAfter.
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
