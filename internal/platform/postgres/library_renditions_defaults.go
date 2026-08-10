package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	defaultLibraryRenditionReserve = int64(250_000_000)
	minimumLibraryRenditionReserve = int64(64_000)
)

type LibraryRenditionRequest struct {
	EditID        string `json:"edit_id"`
	State         string `json:"state"`
	ReservedBytes int64  `json:"reserved_bytes"`
}

type LibraryRenditionJob struct {
	ID               string
	SecurityDomainID string
	SpaceID          string
	ItemID           string
	EditID           string
	RequestedBy      string
	SourceObjectKey  string
	SourceMIME       string
	SourceBytes      int64
	SourceSHA256     string
	Definition       LibraryEditDefinition
	ReservedBytes    int64
	LeaseToken       string
	AttemptCount     int
}

type CompleteLibraryRenditionResult struct {
	DiscardObjectKey string
}

type LibraryRenditionPurge struct {
	TombstoneID  string
	EditID       string
	BlobID       string
	ObjectKey    string
	SpaceID      string
	LogicalBytes int64
	LeaseToken   string
}

// QueueLibraryEditRendition atomically reserves Space storage before any
// expensive media processing begins. A zero maximum chooses a conservative
// server estimate and may use the remaining Space allowance as the hard cap.
func (db *Database) QueueLibraryEditRendition(ctx context.Context, userID, spaceID, itemID, editID string, maximumBytes int64) (*LibraryRenditionRequest, error) {
	if editID == "" || maximumBytes < 0 || maximumBytes > MaxSpaceStorageBytes {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryRenditionRequest{EditID: editID}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryEdit); err != nil {
			return err
		}
		var domainID, lifecycle, renditionState string
		var sourceBytes int64
		var renditionBlob sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT f.security_domain_id,b.byte_size,v.lifecycle_state,v.rendition_state,v.rendition_blob_id
			FROM library_item_versions v JOIN space_library_items i ON i.id=v.space_library_item_id
			JOIN library_files f ON f.id=i.file_id JOIN library_blobs b ON b.id=f.blob_id
			WHERE v.id=$1 AND i.id=$2 AND i.space_id=$3 AND i.lifecycle_state='ready' AND b.lifecycle_state='ready'
			FOR UPDATE OF v,i`, editID, itemID, spaceID).Scan(&domainID, &sourceBytes, &lifecycle, &renditionState, &renditionBlob); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryNotFound
		} else if err != nil {
			return err
		}
		if lifecycle != "ready" {
			return ErrLibraryConflict
		}
		if renditionState == "ready" && renditionBlob.Valid {
			out.State = "ready"
			return nil
		}
		var activeReserved int64
		err := tx.QueryRowContext(ctx, `SELECT reserved_bytes FROM space_rendition_reservations WHERE source_kind='edit' AND source_id=$1 AND state='active'`, editID).Scan(&activeReserved)
		if err == nil {
			out.State, out.ReservedBytes = renditionState, activeReserved
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var ownerID string
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM spaces WHERE id=$1 FOR SHARE`, spaceID).Scan(&ownerID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "owner-storage:"+ownerID); err != nil {
			return err
		}
		ownerUsage, err := ownerStorageUsageTx(ctx, tx, ownerID, true)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID); err != nil {
			return err
		}
		var used, reserved int64
		if err := tx.QueryRowContext(ctx, `SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1 FOR UPDATE`, spaceID).Scan(&used, &reserved); err != nil {
			return err
		}
		_ = used
		_ = reserved
		remaining := ownerUsage.RemainingBytes
		if remaining < minimumLibraryRenditionReserve {
			return ErrLibraryQuota
		}
		requested := maximumBytes
		if requested == 0 {
			requested = sourceBytes
			if requested < 1_000_000 {
				requested = 1_000_000
			}
			if requested > defaultLibraryRenditionReserve {
				requested = defaultLibraryRenditionReserve
			}
			if requested > remaining {
				requested = remaining
			}
		} else if requested > remaining {
			return ErrLibraryQuota
		}
		if requested < minimumLibraryRenditionReserve {
			return ErrLibraryQuota
		}
		reservationID := "rendition_reservation_" + uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_rendition_reservations(id,space_id,user_id,source_kind,source_id,reserved_bytes,state,expires_at)
			VALUES($1,$2,$3,'edit',$4,$5,'active',NOW()+INTERVAL '2 hours')
			ON CONFLICT(source_kind,source_id) DO UPDATE SET id=EXCLUDED.id,space_id=EXCLUDED.space_id,user_id=EXCLUDED.user_id,reserved_bytes=EXCLUDED.reserved_bytes,state='active',expires_at=EXCLUDED.expires_at,updated_at=NOW()`, reservationID, spaceID, userID, editID, requested); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=reserved_bytes+$1,version=version+1,updated_at=NOW() WHERE space_id=$2`, requested, spaceID); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"maximum_output_bytes": requested})
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_processing_jobs(id,security_domain_id,space_id,job_kind,target_kind,target_id,payload,priority)
			VALUES($1,$2,$3,'edit','library_item_version',$4,$5,10)
			ON CONFLICT(job_kind,target_kind,target_id) DO UPDATE SET payload=EXCLUDED.payload,state='queued',attempt_count=0,lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,available_at=NOW(),error_code=NULL,updated_at=NOW()`, "job_"+uuid.NewString(), domainID, spaceID, editID, payload); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET rendition_state='queued',rendition_error_code='',rendition_updated_at=NOW() WHERE id=$1`, editID); err != nil {
			return err
		}
		out.State, out.ReservedBytes = "queued", requested
		return insertLibraryAuditTx(ctx, tx, spaceID, domainID, userID, "library.edit.render_queued", "edit", editID, "success", map[string]any{"reserved_bytes": requested})
	})
	return out, err
}

func (db *Database) ClaimLibraryRenditionJob(ctx context.Context, workerID string, lease time.Duration) (*LibraryRenditionJob, error) {
	if workerID == "" || lease < time.Second || lease > 15*time.Minute {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryRenditionJob{LeaseToken: "lease_" + uuid.NewString()}
	var raw []byte
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `WITH candidate AS (
			SELECT id FROM library_processing_jobs WHERE job_kind='edit' AND (state='queued' AND available_at<=NOW() OR state IN ('leased','running') AND lease_expires_at<=NOW())
			ORDER BY priority DESC,created_at FOR UPDATE SKIP LOCKED LIMIT 1
		), claimed AS (
			UPDATE library_processing_jobs j SET state='leased',lease_token=$1,lease_owner=$2,lease_expires_at=NOW()+$3::interval,attempt_count=attempt_count+1,updated_at=NOW()
			FROM candidate WHERE j.id=candidate.id RETURNING j.*
		)
		SELECT c.id,c.security_domain_id,c.space_id,i.id,v.id,r.user_id,b.r2_object_key,b.server_detected_mime_type,b.byte_size,b.sha256,v.edit_definition,r.reserved_bytes,c.attempt_count
		FROM claimed c JOIN library_item_versions v ON v.id=c.target_id
		JOIN space_library_items i ON i.id=v.space_library_item_id JOIN library_files f ON f.id=i.file_id
		JOIN library_blobs b ON b.id=f.blob_id JOIN space_rendition_reservations r ON r.source_kind='edit' AND r.source_id=v.id AND r.state='active'
		WHERE v.lifecycle_state='ready' AND i.lifecycle_state='ready' AND b.lifecycle_state='ready'`, out.LeaseToken, workerID, lease.String()).
			Scan(&out.ID, &out.SecurityDomainID, &out.SpaceID, &out.ItemID, &out.EditID, &out.RequestedBy, &out.SourceObjectKey, &out.SourceMIME, &out.SourceBytes, &out.SourceSHA256, &raw, &out.ReservedBytes, &out.AttemptCount); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET rendition_state='processing',rendition_updated_at=NOW() WHERE id=$1`, out.EditID); err != nil {
			return err
		}
		return json.Unmarshal(raw, &out.Definition)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return out, err
}

func (db *Database) CompleteLibraryRenditionJob(ctx context.Context, job *LibraryRenditionJob, objectKey, mimeType string, byteSize int64, sha string) (*CompleteLibraryRenditionResult, error) {
	if job == nil || job.ID == "" || job.LeaseToken == "" || objectKey == "" || mimeType == "" || byteSize < 1 || byteSize > job.ReservedBytes || len(sha) != 64 {
		return nil, ErrLibraryInvalid
	}
	out := &CompleteLibraryRenditionResult{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='running',updated_at=NOW() WHERE id=$1 AND state='leased' AND lease_token=$2 AND lease_expires_at>NOW()`, job.ID, job.LeaseToken)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "library:blob:"+job.SecurityDomainID+":"+sha+fmt.Sprint(byteSize)); err != nil {
			return err
		}
		blobID, existingKey := "", ""
		err = tx.QueryRowContext(ctx, `SELECT id,r2_object_key FROM library_blobs WHERE security_domain_id=$1 AND sha256=$2 AND byte_size=$3 AND lifecycle_state='ready' LIMIT 1`, job.SecurityDomainID, sha, byteSize).Scan(&blobID, &existingKey)
		if errors.Is(err, sql.ErrNoRows) {
			blobID = "blob_" + uuid.NewString()
			if _, err := tx.ExecContext(ctx, `INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,client_declared_mime_type,server_detected_mime_type,scan_status,processing_status,lifecycle_state)
				VALUES($1,$2,$3,$4,$5,$6,$6,'clean','ready','ready')`, blobID, job.SecurityDomainID, objectKey, sha, byteSize, mimeType); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if existingKey != objectKey {
			out.DiscardObjectKey = objectKey
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_contributions(id,space_id,user_id,file_id,source_kind,source_id,logical_bytes,state)
			VALUES($1,$2,$3,NULL,'edit',$4,$5,'active')`, "contribution_"+uuid.NewString(), job.SpaceID, job.RequestedBy, job.EditID, byteSize); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_rendition_reservations SET state='consumed',updated_at=NOW() WHERE source_kind='edit' AND source_id=$1 AND state='active'`, job.EditID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),used_bytes=used_bytes+$2,version=version+1,updated_at=NOW() WHERE space_id=$3`, job.ReservedBytes, byteSize, job.SpaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET rendition_blob_id=$1,rendition_state='ready',rendition_mime_type=$2,rendition_byte_size=$3,rendition_error_code='',rendition_updated_at=NOW() WHERE id=$4 AND lifecycle_state='ready'`, blobID, mimeType, byteSize, job.EditID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_library_items SET version=version+1,updated_at=NOW() WHERE id=$1 AND space_id=$2`, job.ItemID, job.SpaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='completed',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,error_code=NULL,updated_at=NOW() WHERE id=$1`, job.ID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, job.SpaceID, job.SecurityDomainID, job.RequestedBy, "library.edit.rendered", "edit", job.EditID, "success", map[string]any{"logical_bytes": byteSize, "deduplicated": out.DiscardObjectKey != ""})
	})
	return out, err
}
