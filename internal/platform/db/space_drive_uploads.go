package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/misty-org/misty-server/internal/spaces"
)

type DriveUpload struct {
	ID                string
	SpaceID           string
	ObjectKey         string
	SHA256            string
	Size              int64
	State             string
	MultipartUploadID string
	PartSize          int64
	ExpiresAt         time.Time
}

func (u DriveUpload) Pending() bool {
	return u.State == "initiated" || u.State == "uploading" || u.State == "uploaded_unverified"
}

type DriveUploadStart struct {
	Upload   DriveUpload
	Present  bool
	Released []DriveUpload
}

const driveUploadSelect = `SELECT id,space_id,object_key,client_sha256,requested_byte_size,state,
	COALESCE(multipart_upload_id,''),COALESCE(multipart_part_size,0),expires_at FROM space_library_uploads`

func scanDriveUpload(row interface{ Scan(...any) error }) (DriveUpload, error) {
	var upload DriveUpload
	err := row.Scan(&upload.ID, &upload.SpaceID, &upload.ObjectKey, &upload.SHA256, &upload.Size, &upload.State,
		&upload.MultipartUploadID, &upload.PartSize, &upload.ExpiresAt)
	return upload, err
}

func spaceSecurityDomainTx(ctx context.Context, tx *sql.Tx, spaceID string) (string, error) {
	var domainID string
	err := tx.QueryRowContext(ctx, `SELECT security_domain_id FROM spaces WHERE id=$1 FOR SHARE`, spaceID).Scan(&domainID)
	return domainID, err
}

func driveBlobKeyTx(ctx context.Context, tx *sql.Tx, domainID, sha string, size int64) (string, error) {
	var key string
	err := tx.QueryRowContext(ctx, `SELECT r2_object_key FROM library_blobs
		WHERE security_domain_id=$1 AND sha256=$2 AND byte_size=$3 AND lifecycle_state='ready' FOR SHARE`, domainID, sha, size).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return key, err
}

func newDriveObjectKey() string { return "library/" + strings.ReplaceAll(uuid.NewString(), "-", "") }

func driveUploadTokenHash() string {
	sum := sha256.Sum256([]byte("drive:" + uuid.NewString()))
	return hex.EncodeToString(sum[:])
}

func validDriveContent(sha string, size int64) bool {
	if !sha256Hex(sha) || size < 0 || size > MaxDriveFileBytes {
		return false
	}
	return size > 0 || sha == EmptyFileSHA256
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

func (db *Database) StartDriveUpload(ctx context.Context, userID, spaceID, sha string, size int64, expiresAt time.Time) (*DriveUploadStart, error) {
	if size < 1 || !validDriveContent(sha, size) || !expiresAt.After(time.Now()) {
		return nil, ErrLibraryInvalid
	}
	out := &DriveUploadStart{Released: []DriveUpload{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if err := requireSpaceUploadSizeTx(ctx, tx, userID, spaceID, size); err != nil {
			return err
		}
		domainID, err := spaceSecurityDomainTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if err := prepareStorageReservationTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, driveUploadSelect+` WHERE purpose='drive' AND space_id=$1 AND user_id=$2
			AND client_sha256=$3 AND requested_byte_size=$4 AND state IN ('initiated','uploading','uploaded_unverified','ready')
			ORDER BY created_at DESC,id FOR UPDATE`, spaceID, userID, sha, size)
		if err != nil {
			return err
		}
		var ready *DriveUpload
		pending := []DriveUpload{}
		for rows.Next() {
			upload, err := scanDriveUpload(rows)
			if err != nil {
				rows.Close()
				return err
			}
			if upload.State == "ready" {
				if ready == nil {
					ready = &upload
				}
				continue
			}
			pending = append(pending, upload)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		key, err := driveBlobKeyTx(ctx, tx, domainID, sha, size)
		if err != nil {
			return err
		}
		if key != "" {
			out.Present, out.Released = true, pending
			if err := releaseDriveUploadsTx(ctx, tx, spaceID, pending, "superseded"); err != nil {
				return err
			}
			if ready != nil {
				out.Upload = *ready
				return nil
			}
			out.Upload = DriveUpload{ID: "upload_" + uuid.NewString(), SpaceID: spaceID, ObjectKey: newDriveObjectKey(),
				SHA256: sha, Size: size, State: "ready", ExpiresAt: time.Now().UTC()}
			_, err := tx.ExecContext(ctx, `INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,
				client_declared_mime_type,requested_byte_size,client_sha256,verified_byte_size,verified_sha256,detected_mime_type,state,upload_token_hash,expires_at,finalized_at)
				VALUES($1,$2,$3,$4,$5,$6,'drive',$7,$8,$6,$8,$6,$7,'ready',$9,$10,NOW())`,
				out.Upload.ID, spaceID, domainID, userID, out.Upload.ObjectKey, sha, DriveMIMEType, size, driveUploadTokenHash(), out.Upload.ExpiresAt)
			return err
		}
		if len(pending) > 0 {
			out.Upload, out.Released = pending[0], pending[1:]
			if err := releaseDriveUploadsTx(ctx, tx, spaceID, out.Released, "superseded"); err != nil {
				return err
			}
			out.Upload.ExpiresAt = expiresAt
			if _, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET expires_at=$2,version=version+1,updated_at=NOW() WHERE id=$1`, out.Upload.ID, expiresAt); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE space_upload_reservations SET expires_at=$2,updated_at=NOW() WHERE upload_id=$1 AND state='active'`, out.Upload.ID, expiresAt)
			return err
		}
		if err := RequireStorageRoomTx(ctx, tx, userID, spaceID, size); err != nil {
			return err
		}
		out.Upload = DriveUpload{ID: "upload_" + uuid.NewString(), SpaceID: spaceID, ObjectKey: newDriveObjectKey(),
			SHA256: sha, Size: size, State: "initiated", ExpiresAt: expiresAt}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,
			client_declared_mime_type,requested_byte_size,client_sha256,state,upload_token_hash,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,'drive',$7,$8,$6,'initiated',$9,$10)`,
			out.Upload.ID, spaceID, domainID, userID, out.Upload.ObjectKey, sha, DriveMIMEType, size, driveUploadTokenHash(), expiresAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_upload_reservations(upload_id,space_id,user_id,reserved_bytes,state,expires_at)
			VALUES($1,$2,$3,$4,'active',$5)`, out.Upload.ID, spaceID, userID, size, expiresAt); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=reserved_bytes+$1,version=version+1,updated_at=NOW() WHERE space_id=$2`, size, spaceID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func releaseDriveUploadsTx(ctx context.Context, tx *sql.Tx, spaceID string, uploads []DriveUpload, code string) error {
	if len(uploads) == 0 {
		return nil
	}
	ids := make([]string, len(uploads))
	for i, upload := range uploads {
		ids[i] = upload.ID
	}
	var released int64
	if err := tx.QueryRowContext(ctx, `WITH released AS (
		UPDATE space_upload_reservations SET state='released',updated_at=NOW() WHERE upload_id=ANY($1) AND state='active' RETURNING reserved_bytes)
		SELECT COALESCE(sum(reserved_bytes),0) FROM released`, pq.Array(ids)).Scan(&released); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, spaceID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET state='rejected',error_code=$2,version=version+1,updated_at=NOW() WHERE id=ANY($1)`, pq.Array(ids), code)
	return err
}

func (db *Database) DriveUploadByID(ctx context.Context, userID, spaceID, uploadID string) (*DriveUpload, error) {
	var upload DriveUpload
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireDriveWriteTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		var err error
		upload, err = scanDriveUpload(tx.QueryRowContext(ctx, driveUploadSelect+` WHERE id=$1 AND space_id=$2 AND user_id=$3 AND purpose='drive'`, uploadID, spaceID, userID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &upload, nil
}

func (db *Database) SetDriveUploadMultipart(ctx context.Context, userID, spaceID, uploadID, multipartID string, partSize int64) error {
	if multipartID == "" || partSize < 1 {
		return ErrLibraryInvalid
	}
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET multipart_upload_id=$4,multipart_part_size=$5,version=version+1,updated_at=NOW()
			WHERE id=$1 AND space_id=$2 AND user_id=$3 AND purpose='drive' AND multipart_upload_id IS NULL AND state='initiated'`,
			uploadID, spaceID, userID, multipartID, partSize)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return ErrLibraryConflict
		}
		return nil
	})
}

func (db *Database) MarkDriveUploadLanded(ctx context.Context, userID, spaceID, uploadID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET state='uploaded_unverified',verified_byte_size=requested_byte_size,
			verified_sha256=client_sha256,detected_mime_type=client_declared_mime_type,version=version+1,updated_at=NOW()
			WHERE id=$1 AND space_id=$2 AND user_id=$3 AND purpose='drive' AND state IN ('initiated','uploading')`, uploadID, spaceID, userID)
		return err
	})
}

func (db *Database) RejectDriveUpload(ctx context.Context, userID, spaceID, uploadID string) error {
	return db.SpaceTx(ctx, func(tx *sql.Tx) error {
		upload, err := scanDriveUpload(tx.QueryRowContext(ctx, driveUploadSelect+` WHERE id=$1 AND space_id=$2 AND user_id=$3 AND purpose='drive' FOR UPDATE`, uploadID, spaceID, userID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		}
		if err != nil || !upload.Pending() {
			return err
		}
		if err := releaseDriveUploadsTx(ctx, tx, spaceID, []DriveUpload{upload}, "object_missing_or_mismatched"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE space_library_uploads SET state='invalid' WHERE id=$1`, uploadID)
		return err
	})
}

func finishDriveUploadTx(ctx context.Context, tx *sql.Tx, userID, spaceID, domainID, uploadID, sha string, size int64) (int64, string, error) {
	upload, err := scanDriveUpload(tx.QueryRowContext(ctx, driveUploadSelect+` WHERE id=$1 AND space_id=$2 AND user_id=$3 AND purpose='drive' FOR UPDATE`, uploadID, spaceID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrLibraryInvalid
	}
	if err != nil {
		return 0, "", err
	}
	if upload.SHA256 != sha || upload.Size != size {
		return 0, "", ErrLibraryInvalid
	}
	switch upload.State {
	case "ready", "deleted":
		return 0, "", nil
	case "uploaded_unverified":
	default:
		return 0, "", ErrDriveUploadIncomplete
	}
	var created string
	err = tx.QueryRowContext(ctx, `INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,client_declared_mime_type,server_detected_mime_type,scan_status,processing_status,lifecycle_state)
		VALUES($1,$2,$3,$4,$5,$6,$6,'skipped','ready','ready')
		ON CONFLICT (security_domain_id,sha256,byte_size) WHERE lifecycle_state<>'deleted' DO NOTHING
		RETURNING r2_object_key`, "blob_"+uuid.NewString(), domainID, upload.ObjectKey, sha, size, DriveMIMEType).Scan(&created)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	discard, state := "", "ready"
	if created == "" {
		discard, state = upload.ObjectKey, "deleted"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET state=$2,finalized_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1`, uploadID, state); err != nil {
		return 0, "", err
	}
	var held int64
	err = tx.QueryRowContext(ctx, `UPDATE space_upload_reservations SET state='consumed',updated_at=NOW() WHERE upload_id=$1 AND state='active' RETURNING reserved_bytes`, uploadID).Scan(&held)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	if held > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, held, spaceID); err != nil {
			return 0, "", err
		}
	}
	return held, discard, nil
}

type driveContent struct {
	sha      string
	size     int64
	uploadID string
}

func resolveDriveContentTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, content driveContent) (string, int64, []string, error) {
	if !validDriveContent(content.sha, content.size) {
		return "", 0, nil, ErrLibraryInvalid
	}
	if content.size == 0 {
		return "", 0, nil, nil
	}
	if err := requireSpaceUploadSizeTx(ctx, tx, userID, spaceID, content.size); err != nil {
		return "", 0, nil, err
	}
	if err := prepareStorageReservationTx(ctx, tx, userID, spaceID); err != nil {
		return "", 0, nil, err
	}
	domainID, err := spaceSecurityDomainTx(ctx, tx, spaceID)
	if err != nil {
		return "", 0, nil, err
	}
	var held int64
	discard := []string{}
	if content.uploadID != "" {
		var key string
		if held, key, err = finishDriveUploadTx(ctx, tx, userID, spaceID, domainID, content.uploadID, content.sha, content.size); err != nil {
			return "", 0, nil, err
		}
		if key != "" {
			discard = append(discard, key)
		}
	}
	key, err := driveBlobKeyTx(ctx, tx, domainID, content.sha, content.size)
	if err != nil {
		return "", 0, nil, err
	}
	if key == "" {
		return "", 0, nil, ErrDriveBlobMissing
	}
	return key, held, discard, nil
}

func driveContributionTx(ctx context.Context, tx *sql.Tx, spaceID, itemID string) (string, int64, error) {
	var userID string
	var bytes int64
	err := tx.QueryRowContext(ctx, `SELECT user_id,logical_bytes FROM space_storage_contributions
		WHERE space_id=$1 AND source_kind='drive' AND source_id=$2 AND state IN ('active','recovery') LIMIT 1`, spaceID, itemID).Scan(&userID, &bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return userID, bytes, err
}

func requireDriveRoomTx(ctx context.Context, tx *sql.Tx, userID, spaceID, itemID string, newBytes, held int64) error {
	if err := prepareStorageReservationTx(ctx, tx, userID, spaceID); err != nil {
		return err
	}
	adding := newBytes - held
	if itemID != "" {
		owner, bytes, err := driveContributionTx(ctx, tx, spaceID, itemID)
		if err != nil {
			return err
		}
		if owner == userID {
			adding -= bytes
		}
	}
	return RequireStorageRoomTx(ctx, tx, userID, spaceID, adding)
}

func setDriveContributionTx(ctx context.Context, tx *sql.Tx, spaceID, itemID, userID string, bytes int64) error {
	released, err := releaseDriveContributionsTx(ctx, tx, spaceID, []string{itemID})
	if err != nil {
		return err
	}
	if bytes > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_contributions(id,space_id,user_id,file_id,source_kind,source_id,logical_bytes,state)
			VALUES($1,$2,$3,NULL,'drive',$4,$5,'active')
			ON CONFLICT (space_id,user_id,source_kind,source_id) DO UPDATE SET logical_bytes=EXCLUDED.logical_bytes,state='active',released_at=NULL,updated_at=NOW()`,
			"contribution_"+uuid.NewString(), spaceID, userID, itemID, bytes); err != nil {
			return err
		}
	}
	if released == 0 && bytes == 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1+$2),version=version+1,updated_at=NOW() WHERE space_id=$3`, released, bytes, spaceID)
	return err
}

func releaseDriveContributionsTx(ctx context.Context, tx *sql.Tx, spaceID string, itemIDs []string) (int64, error) {
	var released int64
	err := tx.QueryRowContext(ctx, `WITH released AS (
		UPDATE space_storage_contributions SET state='released',released_at=NOW(),updated_at=NOW()
		WHERE space_id=$1 AND source_kind='drive' AND source_id=ANY($2) AND state IN ('active','recovery') RETURNING logical_bytes)
		SELECT COALESCE(sum(logical_bytes),0) FROM released`, spaceID, pq.Array(itemIDs)).Scan(&released)
	return released, err
}

func requireDrivePurgeTx(ctx context.Context, tx *sql.Tx, userID, spaceID string) error {
	return requireSpacePermissionTx(ctx, tx, userID, spaceID, spaces.PermissionDrivePurge)
}
