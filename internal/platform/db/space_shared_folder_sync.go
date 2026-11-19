package db

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// A folder upload is two calls. The diff (PlanSharedFolderUpload) compares the
// client's manifest with what the Space's security domain already holds and
// starts one Library upload of purpose "folder" for each blob it lacks; those
// reserve their bytes as any upload does. The commit (CommitSharedFolder)
// turns the uploads that landed into blobs, checks that every blob the
// manifest names is present, and replaces the manifest in one transaction.

// SharedFolderPlanInput is one diff. Files must come from
// NormalizeSharedFolderManifest. Landed names the pending uploads whose
// objects the caller found in the object store; every other pending upload
// the manifest still needs is sent again.
type SharedFolderPlanInput struct {
	UserID, SpaceID, FolderID string
	Files                     []SharedFolderFile
	Landed                    []string
	TokenHash                 string
	ExpiresAt                 time.Time
}

// SharedFolderUploadPlan is a diff's answer: the blob uploads the client must
// send, the committed paths the new manifest drops, how many of its files need
// no upload, and the objects of given-up uploads, which the caller deletes.
type SharedFolderUploadPlan struct {
	Uploads            []SharedFolderPendingUpload
	Remove             []string
	Reused             int
	ReleasedObjectKeys []string
}

// SharedFolderCommitResult is a committed folder. DeleteObjectKeys are objects
// nothing names any more (duplicates of existing blobs, given-up uploads);
// RetireKeys are blobs the folder stopped naming, for RetireSharedFolderBlobs.
type SharedFolderCommitResult struct {
	Folder           SharedFolder
	DeleteObjectKeys []string
	RetireKeys       []string
}

// readyBlobsTx returns the object key of each needed blob the domain holds.
// lock takes the rows for share, so no Library path can retire them before
// this transaction commits.
func readyBlobsTx(ctx context.Context, tx *sql.Tx, domainID string, blobs []sharedFolderBlob, lock bool) (map[string]string, error) {
	out := map[string]string{}
	if len(blobs) == 0 {
		return out, nil
	}
	shas, sizes := blobArrays(blobs)
	query := `SELECT b.sha256,b.byte_size,b.r2_object_key FROM library_blobs b
		JOIN unnest($2::text[],$3::bigint[]) AS n(sha256,byte_size) ON b.sha256=n.sha256 AND b.byte_size=n.byte_size
		WHERE b.security_domain_id=$1 AND b.lifecycle_state='ready'`
	if lock {
		query += ` FOR SHARE OF b`
	}
	rows, err := tx.QueryContext(ctx, query, domainID, shas, sizes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var blob sharedFolderBlob
		var key string
		if err := rows.Scan(&blob.sha, &blob.size, &key); err != nil {
			return nil, err
		}
		out[blob.key()] = key
	}
	return out, rows.Err()
}

func spaceSecurityDomainTx(ctx context.Context, tx *sql.Tx, spaceID string) (string, error) {
	var domainID string
	err := tx.QueryRowContext(ctx, `SELECT security_domain_id FROM spaces WHERE id=$1 FOR SHARE`, spaceID).Scan(&domainID)
	return domainID, err
}

func committedPathsTx(ctx context.Context, tx *sql.Tx, folderID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT path FROM space_shared_folder_files WHERE folder_id=$1 ORDER BY path`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// requireSharedFolderRoomTx checks the storage limits for a commit or diff:
// the manifest's bytes count against the caller once committed, replacing the
// folder's bytes when the caller uploaded it last. held is what the caller
// already reserves for this folder. Callers hold the account's storage lock.
func requireSharedFolderRoomTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, folder SharedFolder, total, held int64) error {
	adding := total - held
	if folder.UploadedBy.ID == userID {
		adding -= folder.ByteSize
	}
	return RequireStorageRoomTx(ctx, tx, userID, spaceID, adding)
}

// PlanSharedFolderUpload is a folder's diff.
func (db *Database) PlanSharedFolderUpload(ctx context.Context, in SharedFolderPlanInput) (*SharedFolderUploadPlan, error) {
	if in.TokenHash == "" || !in.ExpiresAt.After(time.Now()) {
		return nil, ErrLibraryInvalid
	}
	plan := &SharedFolderUploadPlan{Uploads: []SharedFolderPendingUpload{}, Remove: []string{}}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, in.UserID, in.SpaceID, PermissionLibraryUpload); err != nil {
			return err
		}
		folder, err := sharedFolderTx(ctx, tx, in.SpaceID, in.FolderID, true)
		if err != nil {
			return err
		}
		domainID, err := spaceSecurityDomainTx(ctx, tx, in.SpaceID)
		if err != nil {
			return err
		}
		if err := prepareStorageReservationTx(ctx, tx, in.UserID, in.SpaceID); err != nil {
			return err
		}
		committed, err := committedPathsTx(ctx, tx, in.FolderID)
		if err != nil {
			return err
		}
		wanted := make(map[string]bool, len(in.Files))
		for _, file := range in.Files {
			wanted[file.Path] = true
		}
		for _, p := range committed {
			if !wanted[p] {
				plan.Remove = append(plan.Remove, p)
			}
		}
		blobs, total := manifestBlobs(in.Files)
		have, err := readyBlobsTx(ctx, tx, domainID, blobs, false)
		if err != nil {
			return err
		}
		pending, err := pendingSharedFolderUploadsTx(ctx, tx, in.UserID, in.SpaceID, in.FolderID, true)
		if err != nil {
			return err
		}
		landed := map[string]bool{}
		for _, id := range in.Landed {
			landed[id] = true
		}
		// One pending upload per missing blob carries on; the rest are given up.
		byBlob := map[string]SharedFolderPendingUpload{}
		release := []SharedFolderPendingUpload{}
		for _, upload := range pending {
			key := sharedFolderBlob{sha: upload.SHA256, size: upload.ByteSize}.key()
			// The object store, not the row, says whether its bytes arrived.
			upload.State = "initiated"
			if landed[upload.ID] {
				upload.State = "uploaded_unverified"
			}
			current, taken := byBlob[key]
			switch {
			case !taken:
				byBlob[key] = upload
			case current.State != "uploaded_unverified" && upload.State == "uploaded_unverified":
				byBlob[key] = upload
				release = append(release, current)
			default:
				release = append(release, upload)
			}
		}
		missing := map[string]bool{}
		var keep, resend, arrived []string
		var held int64
		newUploads := []SharedFolderPendingUpload{}
		for _, blob := range blobs {
			if _, ok := have[blob.key()]; ok {
				continue
			}
			missing[blob.key()] = true
			upload, ok := byBlob[blob.key()]
			if !ok {
				upload = SharedFolderPendingUpload{
					ID:        "upload_" + uuid.NewString(),
					ObjectKey: "library/" + strings.ReplaceAll(uuid.NewString(), "-", ""),
					SHA256:    blob.sha, ByteSize: blob.size, State: "initiated",
				}
				newUploads = append(newUploads, upload)
				plan.Uploads = append(plan.Uploads, upload)
				continue
			}
			keep, held = append(keep, upload.ID), held+upload.ByteSize
			if upload.State == "uploaded_unverified" {
				arrived = append(arrived, upload.ID)
			} else {
				resend = append(resend, upload.ID)
				plan.Uploads = append(plan.Uploads, upload)
			}
		}
		for key, upload := range byBlob {
			if !missing[key] {
				release = append(release, upload)
			}
		}
		if plan.ReleasedObjectKeys, err = releaseSharedFolderUploadsTx(ctx, tx, in.SpaceID, release); err != nil {
			return err
		}
		if err := requireSharedFolderRoomTx(ctx, tx, in.UserID, in.SpaceID, folder, total, held); err != nil {
			return err
		}
		if len(keep) > 0 {
			// Uploads carried on keep their objects; the ones still to send get
			// the new token and every one the new lifetime.
			if _, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET
				state=CASE WHEN id=ANY($2) THEN 'uploaded_unverified' WHEN id=ANY($3) THEN 'initiated' ELSE state END,
				upload_token_hash=CASE WHEN id=ANY($3) THEN $4 ELSE upload_token_hash END,
				expires_at=$5,version=version+1,updated_at=NOW() WHERE id=ANY($1)`,
				pq.Array(keep), pq.Array(arrived), pq.Array(resend), in.TokenHash, in.ExpiresAt); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE space_upload_reservations SET expires_at=$2,updated_at=NOW() WHERE upload_id=ANY($1) AND state='active'`, pq.Array(keep), in.ExpiresAt); err != nil {
				return err
			}
		}
		if err := insertSharedFolderUploadsTx(ctx, tx, in, domainID, newUploads); err != nil {
			return err
		}
		sending := map[string]bool{}
		for _, upload := range plan.Uploads {
			sending[sharedFolderBlob{sha: upload.SHA256, size: upload.ByteSize}.key()] = true
		}
		for _, file := range in.Files {
			if !sending[sharedFolderBlob{sha: file.SHA256, size: file.Size}.key()] {
				plan.Reused++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func insertSharedFolderUploadsTx(ctx context.Context, tx *sql.Tx, in SharedFolderPlanInput, domainID string, uploads []SharedFolderPendingUpload) error {
	if len(uploads) == 0 {
		return nil
	}
	ids, keys, shas, sizes := make([]string, len(uploads)), make([]string, len(uploads)), make([]string, len(uploads)), make([]int64, len(uploads))
	var reserved int64
	for i, upload := range uploads {
		// The per-object bound every upload keeps.
		if !uploadObjectSizeAllowed(UploadPurposeSharedFolder, upload.ByteSize) {
			return ErrLibraryInvalid
		}
		ids[i], keys[i], shas[i], sizes[i] = upload.ID, upload.ObjectKey, upload.SHA256, upload.ByteSize
		reserved += upload.ByteSize
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,state,upload_token_hash,expires_at,shared_folder_id)
		SELECT n.id,$1,$2,$3,n.object_key,n.sha256,'folder',$4,n.byte_size,n.sha256,'initiated',$5,$6,$7::uuid
		FROM unnest($8::text[],$9::text[],$10::text[],$11::bigint[]) AS n(id,object_key,sha256,byte_size)`,
		in.SpaceID, domainID, in.UserID, SharedFolderMIMEType, in.TokenHash, in.ExpiresAt, in.FolderID,
		pq.Array(ids), pq.Array(keys), pq.Array(shas), pq.Array(sizes)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_upload_reservations(upload_id,space_id,user_id,reserved_bytes,state,expires_at)
		SELECT n.id,$1,$2,n.byte_size,'active',$3 FROM unnest($4::text[],$5::bigint[]) AS n(id,byte_size)`,
		in.SpaceID, in.UserID, in.ExpiresAt, pq.Array(ids), pq.Array(sizes)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=reserved_bytes+$1,version=version+1,updated_at=NOW() WHERE space_id=$2`, reserved, in.SpaceID)
	return err
}

// finishSharedFolderUploadsTx turns verified uploads into blobs. An upload
// whose content another blob already holds gives up its object instead.
// Returns the objects to delete.
func finishSharedFolderUploadsTx(ctx context.Context, tx *sql.Tx, userID, spaceID, folderID, domainID string, verified []string) ([]string, error) {
	if len(verified) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,object_key,client_sha256,requested_byte_size FROM space_library_uploads
		WHERE id=ANY($1) AND shared_folder_id=$2 AND user_id=$3 AND space_id=$4 AND state IN ('initiated','uploaded_unverified')
		ORDER BY client_sha256,id FOR UPDATE`, pq.Array(verified), folderID, userID, spaceID)
	if err != nil {
		return nil, err
	}
	var ids, blobIDs, keys, shas []string
	var sizes []int64
	var reserved int64
	for rows.Next() {
		var id, key, sha string
		var size int64
		if err := rows.Scan(&id, &key, &sha, &size); err != nil {
			rows.Close()
			return nil, err
		}
		ids, blobIDs, keys, shas, sizes = append(ids, id), append(blobIDs, "blob_"+uuid.NewString()), append(keys, key), append(shas, sha), append(sizes, size)
		reserved += size
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	// The digest index decides between this upload and any blob of the same
	// content, so two uploads of it can never both become blobs.
	created := map[string]bool{}
	inserted, err := tx.QueryContext(ctx, `INSERT INTO library_blobs(id,security_domain_id,r2_object_key,sha256,byte_size,client_declared_mime_type,server_detected_mime_type,scan_status,processing_status,lifecycle_state)
		SELECT n.id,$1,n.object_key,n.sha256,n.byte_size,$2,$2,'skipped','ready','ready'
		FROM unnest($3::text[],$4::text[],$5::text[],$6::bigint[]) AS n(id,object_key,sha256,byte_size)
		ON CONFLICT (security_domain_id,sha256,byte_size) WHERE lifecycle_state<>'deleted' DO NOTHING
		RETURNING r2_object_key`, domainID, SharedFolderMIMEType, pq.Array(blobIDs), pq.Array(keys), pq.Array(shas), pq.Array(sizes))
	if err != nil {
		return nil, err
	}
	for inserted.Next() {
		var key string
		if err := inserted.Scan(&key); err != nil {
			inserted.Close()
			return nil, err
		}
		created[key] = true
	}
	if err := inserted.Close(); err != nil {
		return nil, err
	}
	var finished, discard []string
	for _, key := range keys {
		if created[key] {
			finished = append(finished, key)
		} else {
			discard = append(discard, key)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_library_uploads SET verified_byte_size=requested_byte_size,verified_sha256=client_sha256,
		detected_mime_type=client_declared_mime_type,state=CASE WHEN object_key=ANY($2) THEN 'ready' ELSE 'deleted' END,
		finalized_at=NOW(),version=version+1,updated_at=NOW() WHERE id=ANY($1)`, pq.Array(ids), pq.Array(finished)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_upload_reservations SET state='consumed',updated_at=NOW() WHERE upload_id=ANY($1) AND state='active'`, pq.Array(ids)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=GREATEST(0,reserved_bytes-$1),version=version+1,updated_at=NOW() WHERE space_id=$2`, reserved, spaceID); err != nil {
		return nil, err
	}
	return discard, nil
}

// CommitSharedFolder replaces a folder's manifest. verified names the
// caller's pending uploads whose objects the caller checked in the object
// store. Every blob the manifest names must then be present, or nothing
// changes and SharedFolderBlobsMissingError lists what is missing.
func (db *Database) CommitSharedFolder(ctx context.Context, userID, spaceID, folderID string, files []SharedFolderFile, verified []string) (*SharedFolderCommitResult, error) {
	result := &SharedFolderCommitResult{}
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryUpload); err != nil {
			return err
		}
		folder, err := sharedFolderTx(ctx, tx, spaceID, folderID, true)
		if err != nil {
			return err
		}
		domainID, err := spaceSecurityDomainTx(ctx, tx, spaceID)
		if err != nil {
			return err
		}
		if err := prepareStorageReservationTx(ctx, tx, userID, spaceID); err != nil {
			return err
		}
		if result.DeleteObjectKeys, err = finishSharedFolderUploadsTx(ctx, tx, userID, spaceID, folderID, domainID, verified); err != nil {
			return err
		}
		blobs, total := manifestBlobs(files)
		have, err := readyBlobsTx(ctx, tx, domainID, blobs, true)
		if err != nil {
			return err
		}
		missing := []string{}
		for _, blob := range blobs {
			if _, ok := have[blob.key()]; !ok {
				missing = append(missing, blob.sha)
			}
		}
		if len(missing) > 0 {
			return SharedFolderBlobsMissingError{SHA256: missing}
		}
		// Uploads this commit did not take in are superseded by it.
		rest, err := pendingSharedFolderUploadsTx(ctx, tx, userID, spaceID, folderID, true)
		if err != nil {
			return err
		}
		releasedKeys, err := releaseSharedFolderUploadsTx(ctx, tx, spaceID, rest)
		if err != nil {
			return err
		}
		result.DeleteObjectKeys = append(result.DeleteObjectKeys, releasedKeys...)
		if err := requireSharedFolderRoomTx(ctx, tx, userID, spaceID, folder, total, 0); err != nil {
			return err
		}
		oldKeys, err := sharedFolderBlobKeysTx(ctx, tx, folderID)
		if err != nil {
			return err
		}
		if err := replaceSharedFolderFilesTx(ctx, tx, folderID, files, have); err != nil {
			return err
		}
		released, err := releaseSharedFolderContributionsTx(ctx, tx, spaceID, folderID)
		if err != nil {
			return err
		}
		if total > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_contributions(id,space_id,user_id,file_id,source_kind,source_id,logical_bytes,state)
				VALUES($1,$2,$3,NULL,'shared_folder',$4,$5,'active')
				ON CONFLICT (space_id,user_id,source_kind,source_id) DO UPDATE SET logical_bytes=EXCLUDED.logical_bytes,state='active',released_at=NULL,updated_at=NOW()`,
				"contribution_"+uuid.NewString(), spaceID, userID, folderID, total); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET used_bytes=GREATEST(0,used_bytes-$1+$2),version=version+1,updated_at=NOW() WHERE space_id=$3`, released, total, spaceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_shared_folders SET byte_size=$2,file_count=$3,uploaded_by=$4,uploaded_at=NOW() WHERE id=$1`, folderID, total, len(files), userID); err != nil {
			return err
		}
		if result.Folder, err = sharedFolderTx(ctx, tx, spaceID, folderID, false); err != nil {
			return err
		}
		kept := map[string]bool{}
		for _, key := range have {
			kept[key] = true
		}
		for _, key := range oldKeys {
			if !kept[key] {
				result.RetireKeys = append(result.RetireKeys, key)
			}
		}
		sort.Strings(result.RetireKeys)
		if _, err := recordSpaceEventTx(ctx, tx, spaceID, userID, "folder.updated", folderID, map[string]any{"folder_id": folderID}); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, domainID, userID, "folder.committed", "shared_folder", folderID, "success",
			map[string]any{"logical_bytes": total, "file_count": len(files), "new_blobs": len(verified)})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func replaceSharedFolderFilesTx(ctx context.Context, tx *sql.Tx, folderID string, files []SharedFolderFile, blobKeys map[string]string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM space_shared_folder_files WHERE folder_id=$1`, folderID); err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	paths, shas, sizes, keys := make([]string, len(files)), make([]string, len(files)), make([]int64, len(files)), make([]string, len(files))
	for i, file := range files {
		paths[i], shas[i], sizes[i] = file.Path, file.SHA256, file.Size
		if file.Size > 0 {
			keys[i] = blobKeys[sharedFolderBlob{sha: file.SHA256, size: file.Size}.key()]
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO space_shared_folder_files(folder_id,path,sha256,byte_size,blob_key)
		SELECT $1::uuid,n.path,n.sha256,n.byte_size,NULLIF(n.blob_key,'')
		FROM unnest($2::text[],$3::text[],$4::bigint[],$5::text[]) AS n(path,sha256,byte_size,blob_key)`,
		folderID, pq.Array(paths), pq.Array(shas), pq.Array(sizes), pq.Array(keys))
	return err
}
