package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Shared Space folders: a folder a member uploads to a Space, which the other
// members download. A folder is one manifest of relative paths whose bytes are
// the Library's content-addressed blobs. A re-upload sends only the blobs the
// Space's security domain lacks (space_shared_folder_sync.go) and replaces the
// manifest whole; the last commit wins. The folder's bytes count against the
// storage limits through one storage contribution, attributed to whoever
// committed it last. Any member reads a folder; library.upload writes one.

const (
	MaxSharedFolderFiles     = 50000
	MaxSharedFolderPathRunes = 1024
	MaxSharedFolderNameRunes = 120
	// SharedFolderMIMEType is what folder blobs are stored and served as.
	SharedFolderMIMEType = "application/octet-stream"
	// EmptyFileSHA256 is the digest of no bytes. An empty file has no blob.
	EmptyFileSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	// sharedFolderRetireBatch bounds the blobs one transaction retires: each
	// takes the blob's deduplication lock, as every other Library path does.
	sharedFolderRetireBatch = 32
)

// SharedFolderBlobsMissingError refuses a commit whose manifest names content
// the server does not hold yet. SHA256 lists the missing digests.
type SharedFolderBlobsMissingError struct{ SHA256 []string }

func (e SharedFolderBlobsMissingError) Error() string {
	return fmt.Sprintf("shared folder commit is missing %d blobs", len(e.SHA256))
}

type SharedFolderUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SharedFolder struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	UploadedBy SharedFolderUser `json:"uploadedBy"`
	UploadedAt time.Time        `json:"uploadedAt"`
	ByteSize   int64            `json:"byteSize"`
	FileCount  int              `json:"fileCount"`
}

// SharedFolderFile is one manifest entry as the client sends it.
type SharedFolderFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// SharedFolderStoredFile is a committed entry with the object holding its
// bytes; BlobKey is empty for an empty file.
type SharedFolderStoredFile struct {
	SharedFolderFile
	BlobKey string
}

// SharedFolderPendingUpload is one blob upload a diff started and a commit
// has not taken in yet.
type SharedFolderPendingUpload struct {
	ID        string
	ObjectKey string
	SHA256    string
	ByteSize  int64
	State     string
}

type sharedFolderBlob struct {
	sha  string
	size int64
}

func (b sharedFolderBlob) key() string { return b.sha + ":" + fmt.Sprint(b.size) }

// NormalizeSharedFolderName trims a folder name and checks its length.
func NormalizeSharedFolderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if count := utf8.RuneCountInString(name); count < 1 || count > MaxSharedFolderNameRunes || !utf8.ValidString(name) {
		return "", ErrLibraryInvalid
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", ErrLibraryInvalid
		}
	}
	return name, nil
}

// ValidSharedFolderPath accepts a relative posix path: no leading slash, no
// backslashes, no empty, "." or ".." segments, no control characters, and at
// most 1024 characters.
func ValidSharedFolderPath(p string) bool {
	if p == "" || !utf8.ValidString(p) || utf8.RuneCountInString(p) > MaxSharedFolderPathRunes ||
		strings.HasPrefix(p, "/") || strings.ContainsRune(p, '\\') {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// NormalizeSharedFolderManifest checks a manifest and returns it with
// lowercase digests, sorted by path. Every path is unique and none is also a
// directory of another; each file keeps the per-object upload bound.
func NormalizeSharedFolderManifest(files []SharedFolderFile) ([]SharedFolderFile, error) {
	if len(files) > MaxSharedFolderFiles {
		return nil, ErrLibraryInvalid
	}
	out := make([]SharedFolderFile, len(files))
	seen := make(map[string]bool, len(files))
	for i, file := range files {
		file.SHA256 = strings.ToLower(strings.TrimSpace(file.SHA256))
		if !ValidSharedFolderPath(file.Path) || seen[file.Path] || !sha256Hex(file.SHA256) || file.Size < 0 {
			return nil, ErrLibraryInvalid
		}
		if file.Size == 0 && file.SHA256 != EmptyFileSHA256 {
			return nil, ErrLibraryInvalid
		}
		if file.Size > 0 && !uploadObjectSizeAllowed(UploadPurposeSharedFolder, file.Size) {
			return nil, ErrLibraryInvalid
		}
		seen[file.Path] = true
		out[i] = file
	}
	for _, file := range out {
		for dir := path.Dir(file.Path); dir != "."; dir = path.Dir(dir) {
			if seen[dir] {
				return nil, ErrLibraryInvalid
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func sha256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// manifestBlobs lists the distinct non-empty blobs a manifest needs, in a
// stable order, and the manifest's total size.
func manifestBlobs(files []SharedFolderFile) ([]sharedFolderBlob, int64) {
	seen := map[string]bool{}
	blobs := []sharedFolderBlob{}
	var total int64
	for _, file := range files {
		total += file.Size
		blob := sharedFolderBlob{sha: file.SHA256, size: file.Size}
		if file.Size == 0 || seen[blob.key()] {
			continue
		}
		seen[blob.key()] = true
		blobs = append(blobs, blob)
	}
	return blobs, total
}

func blobArrays(blobs []sharedFolderBlob) (any, any) {
	shas, sizes := make([]string, len(blobs)), make([]int64, len(blobs))
	for i, blob := range blobs {
		shas[i], sizes[i] = blob.sha, blob.size
	}
	return pq.Array(shas), pq.Array(sizes)
}

const sharedFolderSelect = `SELECT f.id::text,f.name,f.uploaded_by,COALESCE(u.username,''),f.uploaded_at,f.byte_size,f.file_count
	FROM space_shared_folders f LEFT JOIN users u ON u.id=f.uploaded_by`

func scanSharedFolder(row interface{ Scan(...any) error }) (SharedFolder, error) {
	var folder SharedFolder
	err := row.Scan(&folder.ID, &folder.Name, &folder.UploadedBy.ID, &folder.UploadedBy.Name, &folder.UploadedAt, &folder.ByteSize, &folder.FileCount)
	return folder, err
}

func validSharedFolderID(folderID string) bool {
	_, err := uuid.Parse(folderID)
	return err == nil && len(folderID) == 36
}

// sharedFolderTx reads one live folder of the Space; lock takes it for update.
func sharedFolderTx(ctx context.Context, tx *sql.Tx, spaceID, folderID string, lock bool) (SharedFolder, error) {
	if !validSharedFolderID(folderID) {
		return SharedFolder{}, ErrLibraryNotFound
	}
	query := sharedFolderSelect + ` WHERE f.id=$1 AND f.space_id=$2 AND f.deleted_at IS NULL`
	if lock {
		query += ` FOR UPDATE OF f`
	}
	folder, err := scanSharedFolder(tx.QueryRowContext(ctx, query, folderID, spaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return SharedFolder{}, ErrLibraryNotFound
	}
	return folder, err
}

// SharedFolders lists a Space's folders by name, to any member.
func (db *Database) SharedFolders(ctx context.Context, userID, spaceID string) ([]SharedFolder, error) {
	folders := []SharedFolder{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, sharedFolderSelect+` WHERE f.space_id=$1 AND f.deleted_at IS NULL ORDER BY f.name,f.id`, spaceID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			folder, err := scanSharedFolder(rows)
			if err != nil {
				return err
			}
			folders = append(folders, folder)
		}
		return rows.Err()
	})
	return folders, err
}

// CreateSharedFolder starts an empty folder, uploaded by its creator.
func (db *Database) CreateSharedFolder(ctx context.Context, userID, spaceID, name string) (*SharedFolder, error) {
	name, err := NormalizeSharedFolderName(name)
	if err != nil {
		return nil, err
	}
	var folder SharedFolder
	err = db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryUpload); err != nil {
			return err
		}
		id := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_shared_folders(id,space_id,name,uploaded_by) VALUES($1,$2,$3,$4)`, id, spaceID, name, userID); err != nil {
			return err
		}
		if folder, err = sharedFolderTx(ctx, tx, spaceID, id, false); err != nil {
			return err
		}
		_, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "folder.created", id, map[string]any{"folder_id": id})
		return err
	})
	if err != nil {
		return nil, err
	}
	return &folder, nil
}

// SharedFolderManifest returns a folder and its files by path, to any member.
func (db *Database) SharedFolderManifest(ctx context.Context, userID, spaceID, folderID string) (*SharedFolder, []SharedFolderStoredFile, error) {
	var folder SharedFolder
	files := []SharedFolderStoredFile{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		var err error
		if folder, err = sharedFolderTx(ctx, tx, spaceID, folderID, false); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT path,sha256,byte_size,COALESCE(blob_key,'') FROM space_shared_folder_files WHERE folder_id=$1 ORDER BY path`, folderID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var file SharedFolderStoredFile
			if err := rows.Scan(&file.Path, &file.SHA256, &file.Size, &file.BlobKey); err != nil {
				return err
			}
			files = append(files, file)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	return &folder, files, nil
}

// PendingSharedFolderUploads lists the caller's blob uploads for a folder that
// a commit has not taken in yet, so the caller can check which have landed.
func (db *Database) PendingSharedFolderUploads(ctx context.Context, userID, spaceID, folderID string) ([]SharedFolderPendingUpload, error) {
	out := []SharedFolderPendingUpload{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryUpload); err != nil {
			return err
		}
		if _, err := sharedFolderTx(ctx, tx, spaceID, folderID, false); err != nil {
			return err
		}
		var err error
		out, err = pendingSharedFolderUploadsTx(ctx, tx, userID, spaceID, folderID, false)
		return err
	})
	return out, err
}

func pendingSharedFolderUploadsTx(ctx context.Context, tx *sql.Tx, userID, spaceID, folderID string, lock bool) ([]SharedFolderPendingUpload, error) {
	query := `SELECT id,object_key,client_sha256,requested_byte_size,state FROM space_library_uploads
		WHERE shared_folder_id=$1 AND user_id=$2 AND space_id=$3 AND state IN ('initiated','uploading','uploaded_unverified')
		ORDER BY client_sha256,id`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, query, folderID, userID, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SharedFolderPendingUpload{}
	for rows.Next() {
		var upload SharedFolderPendingUpload
		if err := rows.Scan(&upload.ID, &upload.ObjectKey, &upload.SHA256, &upload.ByteSize, &upload.State); err != nil {
			return nil, err
		}
		out = append(out, upload)
	}
	return out, rows.Err()
}
