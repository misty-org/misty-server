package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (db *Database) FailLibraryRenditionJob(ctx context.Context, job *LibraryRenditionJob, code string) error {
	if job == nil || job.ID == "" || job.LeaseToken == "" || code == "" {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var attempts, maxAttempts int
		if err := tx.QueryRowContext(ctx, `SELECT attempt_count,max_attempts FROM library_processing_jobs WHERE id=$1 AND lease_token=$2 AND state IN ('leased','running') FOR UPDATE`, job.ID, job.LeaseToken).Scan(&attempts, &maxAttempts); errors.Is(err, sql.ErrNoRows) {
			return ErrLibraryConflict
		} else if err != nil {
			return err
		}
		terminal := attempts >= maxAttempts
		state := "queued"
		if terminal {
			state = "dead"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state=$1,error_code=$2,available_at=NOW()+make_interval(secs=>LEAST(300,attempt_count*attempt_count*5)),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,updated_at=NOW() WHERE id=$3`, state, code, job.ID); err != nil {
			return err
		}
		if terminal {
			var released int64
			if err := tx.QueryRowContext(ctx, `UPDATE space_rendition_reservations SET state='released',updated_at=NOW() WHERE source_kind='edit' AND source_id=$1 AND state='active' RETURNING reserved_bytes`, job.EditID).Scan(&released); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if released > 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, job.SpaceID); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET rendition_state='failed',rendition_error_code=$1,rendition_updated_at=NOW() WHERE id=$2`, code, job.EditID); err != nil {
				return err
			}
			return insertLibraryAuditTx(ctx, tx, job.SpaceID, job.SecurityDomainID, job.RequestedBy, "library.edit.render_failed", "edit", job.EditID, "failed", map[string]any{"code": code})
		}
		_, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET rendition_state='queued',rendition_error_code=$1,rendition_updated_at=NOW() WHERE id=$2`, code, job.EditID)
		return err
	})
}

func (db *Database) ReleaseExpiredLibraryRenditionReservations(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	releasedCount := 0
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,space_id,source_id,reserved_bytes FROM space_rendition_reservations WHERE state='active' AND expires_at<=NOW() ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
		if err != nil {
			return err
		}
		type expired struct {
			id, spaceID, sourceID string
			bytes                 int64
		}
		var values []expired
		for rows.Next() {
			var value expired
			if err := rows.Scan(&value.id, &value.spaceID, &value.sourceID, &value.bytes); err != nil {
				rows.Close()
				return err
			}
			values = append(values, value)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, value := range values {
			if _, err := tx.ExecContext(ctx, `UPDATE space_rendition_reservations SET state='released',updated_at=NOW() WHERE id=$1`, value.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, value.bytes, value.spaceID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET rendition_state='failed',rendition_error_code='reservation_expired',rendition_updated_at=NOW() WHERE id=$1 AND rendition_state IN ('queued','processing')`, value.sourceID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE library_processing_jobs SET state='canceled',lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL,error_code='reservation_expired',updated_at=NOW() WHERE job_kind='edit' AND target_id=$1 AND state IN ('queued','leased','running')`, value.sourceID); err != nil {
				return err
			}
			releasedCount++
		}
		return nil
	})
	return releasedCount, err
}

// ClaimExpiredLibraryRenditionPurge leases one due edit tombstone. ObjectKey
// is empty when the physical blob is still authoritatively referenced and
// only the Space-scoped version/contribution should be removed.
func (db *Database) ClaimExpiredLibraryRenditionPurge(ctx context.Context, lease time.Duration) (*LibraryRenditionPurge, error) {
	if lease < time.Second || lease > 15*time.Minute {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryRenditionPurge{LeaseToken: "delete_lease_" + uuid.NewString()}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var blobID sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT t.id,t.target_id,COALESCE(t.space_id,''),v.rendition_blob_id,COALESCE(c.logical_bytes,0)
			FROM library_recovery_tombstones t JOIN library_item_versions v ON v.id=t.target_id
			LEFT JOIN space_storage_contributions c ON c.source_kind='edit' AND c.source_id=v.id AND c.state='recovery'
			WHERE t.target_kind='edit' AND t.lifecycle_state='recovery' AND t.recover_until<=NOW()
			AND NOT EXISTS(SELECT 1 FROM library_legal_holds h WHERE h.active AND (h.target_kind='edit' AND h.target_id=t.target_id OR h.target_kind='blob' AND h.target_id=v.rendition_blob_id))
			ORDER BY t.recover_until FOR UPDATE OF t,v SKIP LOCKED LIMIT 1`).Scan(&out.TombstoneID, &out.EditID, &out.SpaceID, &blobID, &out.LogicalBytes); err != nil {
			return err
		}
		if blobID.Valid {
			out.BlobID = blobID.String
			var shared bool
			if err := tx.QueryRowContext(ctx, `SELECT
				EXISTS(SELECT 1 FROM library_files WHERE blob_id=$1 AND lifecycle_state<>'deleted')
				OR EXISTS(SELECT 1 FROM library_item_versions WHERE rendition_blob_id=$1 AND id<>$2 AND lifecycle_state<>'deleted')
				OR EXISTS(SELECT 1 FROM library_derivatives WHERE derivative_blob_id=$1 AND lifecycle_state<>'deleted')
				OR EXISTS(SELECT 1 FROM library_exports WHERE export_blob_id=$1 AND state<>'deleted')
				OR EXISTS(SELECT 1 FROM library_legal_holds WHERE target_kind='blob' AND target_id=$1 AND active)`, out.BlobID, out.EditID).Scan(&shared); err != nil {
				return err
			}
			if !shared {
				if err := tx.QueryRowContext(ctx, `UPDATE library_blobs SET lifecycle_state='purging',version=version+1,updated_at=NOW() WHERE id=$1 AND lifecycle_state='ready' RETURNING r2_object_key`, out.BlobID).Scan(&out.ObjectKey); errors.Is(err, sql.ErrNoRows) {
					return ErrLibraryConflict
				} else if err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET lifecycle_state='purging' WHERE id=$1 AND lifecycle_state='recovery'`, out.EditID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE library_recovery_tombstones SET lifecycle_state='purging',delete_lease_token=$1,delete_lease_expires_at=NOW()+$2::interval,updated_at=NOW() WHERE id=$3 AND lifecycle_state='recovery'`, out.LeaseToken, lease.String(), out.TombstoneID)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return out, err
}

func (db *Database) CompleteLibraryRenditionPurge(ctx context.Context, purge *LibraryRenditionPurge) error {
	if purge == nil || purge.TombstoneID == "" || purge.EditID == "" || purge.LeaseToken == "" {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var valid bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library_recovery_tombstones WHERE id=$1 AND target_id=$2 AND lifecycle_state='purging' AND delete_lease_token=$3 AND delete_lease_expires_at>NOW())`, purge.TombstoneID, purge.EditID, purge.LeaseToken).Scan(&valid); err != nil || !valid {
			return ErrLibraryConflict
		}
		if purge.ObjectKey != "" {
			result, err := tx.ExecContext(ctx, `UPDATE library_blobs SET lifecycle_state='deleted',deleted_at=NOW(),version=version+1,updated_at=NOW() WHERE id=$1 AND lifecycle_state='purging' AND r2_object_key=$2`, purge.BlobID, purge.ObjectKey)
			if err != nil {
				return err
			}
			if count, _ := result.RowsAffected(); count == 0 {
				return ErrLibraryConflict
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET lifecycle_state='deleted' WHERE id=$1 AND lifecycle_state='purging'`, purge.EditID); err != nil {
			return err
		}
		var released int64
		if err := tx.QueryRowContext(ctx, `UPDATE space_storage_contributions SET state='released',released_at=NOW(),updated_at=NOW() WHERE space_id=$1 AND source_kind='edit' AND source_id=$2 AND state='recovery' RETURNING logical_bytes`, purge.SpaceID, purge.EditID).Scan(&released); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if released > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, released, purge.SpaceID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_recovery_tombstones SET lifecycle_state='purged',delete_lease_token=NULL,delete_lease_expires_at=NULL,updated_at=NOW() WHERE id=$1`, purge.TombstoneID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, purge.SpaceID, "", "", "library.edit.purged", "edit", purge.EditID, "success", map[string]any{"released_bytes": released, "physical_blob_deleted": purge.ObjectKey != ""})
	})
}

func (db *Database) FailLibraryRenditionPurge(ctx context.Context, purge *LibraryRenditionPurge) error {
	if purge == nil || purge.TombstoneID == "" || purge.EditID == "" || purge.LeaseToken == "" {
		return ErrLibraryInvalid
	}
	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if purge.ObjectKey != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE library_blobs SET lifecycle_state='ready',version=version+1,updated_at=NOW() WHERE id=$1 AND lifecycle_state='purging'`, purge.BlobID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE library_item_versions SET lifecycle_state='recovery' WHERE id=$1 AND lifecycle_state='purging'`, purge.EditID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE library_recovery_tombstones SET lifecycle_state='recovery',recover_until=NOW()+INTERVAL '1 hour',delete_lease_token=NULL,delete_lease_expires_at=NULL,updated_at=NOW() WHERE id=$1 AND delete_lease_token=$2`, purge.TombstoneID, purge.LeaseToken)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return ErrLibraryConflict
		}
		return nil
	})
}
