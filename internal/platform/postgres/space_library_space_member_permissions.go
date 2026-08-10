package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (db *Database) SpaceMemberPermissions(ctx context.Context, actorUserID, spaceID, memberUserID string) (map[string]bool, error) {
	out := map[string]bool{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, actorUserID); err != nil {
			return err
		}
		if actorUserID != memberUserID {
			if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, actorUserID); err != nil {
				return ErrLibraryForbidden
			}
		}
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, memberUserID); err != nil {
			return ErrLibraryNotFound
		}
		for _, permission := range configurableSpacePermissions {
			allowed, err := hasSpacePermissionTx(ctx, tx, memberUserID, spaceID, permission)
			if err != nil {
				return err
			}
			out[permission] = allowed
		}
		applySpacePermissionDependencies(out)
		return nil
	})
	return out, err
}

func applySpacePermissionDependencies(permissions map[string]bool) {
	if !permissions[PermissionMessagesRead] {
		permissions[PermissionMessagesWrite] = false
	}
	if !permissions[PermissionMessagesRead] || !permissions[PermissionMessagesWrite] {
		permissions[PermissionAttachmentUpload] = false
	}
	if !permissions[PermissionTasksView] {
		permissions[PermissionTasksManage] = false
	}
}

func (db *Database) SetSpaceMemberPermission(
	ctx context.Context,
	ownerUserID,
	spaceID,
	memberUserID,
	permission,
	effect string,
) error {
	validPermission := false
	for _, candidate := range configurableSpacePermissions {
		if permission == candidate {
			validPermission = true
			break
		}
	}
	if !validPermission || effect != "allow" && effect != "deny" && effect != "inherit" {
		return ErrLibraryInvalid
	}

	return db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpaceLifecycleManagerTx(ctx, tx, spaceID, ownerUserID); err != nil {
			return ErrLibraryForbidden
		}
		role, err := requireSpaceMemberTx(ctx, tx, spaceID, memberUserID)
		if err != nil {
			return ErrLibraryNotFound
		}
		if role == "owner" {
			return ErrLibraryInvalid
		}

		if effect == "inherit" {
			_, err = tx.ExecContext(
				ctx,
				`DELETE FROM space_member_permission_overrides
				 WHERE space_id=$1 AND user_id=$2 AND permission=$3`,
				spaceID,
				memberUserID,
				permission,
			)
		} else {
			_, err = tx.ExecContext(
				ctx,
				`INSERT INTO space_member_permission_overrides(
					space_id,user_id,permission,effect,updated_by_user_id
				) VALUES($1,$2,$3,$4,$5)
				ON CONFLICT(space_id,user_id,permission) DO UPDATE
				SET effect=excluded.effect,
					updated_by_user_id=excluded.updated_by_user_id,
					version=space_member_permission_overrides.version+1,
					updated_at=NOW()`,
				spaceID,
				memberUserID,
				permission,
				effect,
				ownerUserID,
			)
		}
		if err != nil {
			return err
		}

		var securityDomainID string
		if err := tx.QueryRowContext(
			ctx,
			`SELECT security_domain_id FROM spaces WHERE id=$1`,
			spaceID,
		).Scan(&securityDomainID); err != nil {
			return err
		}
		return insertLibraryAuditTx(
			ctx,
			tx,
			spaceID,
			securityDomainID,
			ownerUserID,
			"space.permission.updated",
			"member",
			memberUserID,
			"success",
			map[string]any{"permission": permission, "effect": effect},
		)
	})
}

func hasSpacePermissionTx(ctx context.Context, tx *sql.Tx, userID, spaceID, permission string) (bool, error) {
	role, err := requireSpaceMemberTx(ctx, tx, spaceID, userID)
	if err != nil {
		return false, err
	}
	if role == "owner" {
		return true, nil
	}
	misty, err := isMistySpaceTx(ctx, tx, spaceID)
	if err != nil {
		return false, err
	}
	if misty {
		operator, err := isMistyOperatorTx(ctx, tx, userID)
		if err != nil {
			return false, err
		}
		if operator {
			return true, nil
		}
		switch permission {
		case PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload:
			return true, nil
		default:
			return false, nil
		}
	}
	var effect string
	err = tx.QueryRowContext(
		ctx,
		`SELECT effect FROM space_member_permission_overrides
		 WHERE space_id=$1 AND user_id=$2 AND permission=$3`,
		spaceID,
		userID,
		permission,
	).Scan(&effect)
	if err == nil {
		return effect == "allow", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return fixedMemberPermission(permission), nil
}

func fixedMemberPermission(permission string) bool {
	switch permission {
	case PermissionMessagesRead, PermissionMessagesWrite, PermissionAttachmentUpload,
		PermissionLibraryView, PermissionLibraryUpload, PermissionLibraryAdd,
		PermissionLibraryEdit, PermissionLibraryDownload, PermissionLibraryImport,
		PermissionStorageViewOwn, PermissionStudioView, PermissionAgentsRun,
		PermissionTasksView, PermissionTasksManage:
		return true
	default:
		return false
	}
}

func requireSpacePermissionTx(ctx context.Context, tx *sql.Tx, userID, spaceID, permission string) error {
	allowed, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, permission)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrLibraryForbidden
	}
	return nil
}

func (db *Database) SpaceStorageUsage(ctx context.Context, userID, spaceID string) (*SpaceStorageUsage, error) {
	out := &SpaceStorageUsage{SpaceID: spaceID}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, PermissionStorageViewOwn); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT owner_user_id FROM spaces WHERE id=$1`, spaceID).Scan(&out.OwnerUserID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID); err != nil {
			return err
		}
		if out.OwnerUserID == userID {
			// Keep the selected Space counter authoritative at read time. Older
			// deployments can leave the derived cache at zero even though its
			// contribution and reservation ledgers contain storage.
			if _, err := tx.ExecContext(ctx, `WITH actual AS (
				SELECT
					COALESCE((SELECT sum(logical_bytes) FROM space_storage_contributions WHERE space_id=$1 AND state IN ('active','recovery')),0) used,
					COALESCE((SELECT sum(reserved_bytes) FROM space_upload_reservations WHERE space_id=$1 AND state='active'),0)
					+ COALESCE((SELECT sum(reserved_bytes) FROM space_rendition_reservations WHERE space_id=$1 AND state='active'),0) reserved
			)
			UPDATE space_storage_usage u
			SET used_bytes=actual.used,reserved_bytes=actual.reserved,version=u.version+1,updated_at=NOW()
			FROM actual
			WHERE u.space_id=$1 AND (u.used_bytes<>actual.used OR u.reserved_bytes<>actual.reserved)`, spaceID); err != nil {
				return err
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT used_bytes,reserved_bytes,version FROM space_storage_usage WHERE space_id=$1`, spaceID).Scan(&out.SpaceUsedBytes, &out.SpaceReservedBytes, &out.Version); err != nil {
			return err
		}
		owner, err := ownerStorageUsageTx(ctx, tx, out.OwnerUserID, false)
		if err != nil {
			return err
		}
		out.UsedBytes, out.ReservedBytes, out.LimitBytes, out.RemainingBytes = owner.UsedBytes, owner.ReservedBytes, owner.LimitBytes, owner.RemainingBytes
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (db *Database) CreateLibraryUpload(ctx context.Context, userID, spaceID, purpose, filename, declaredMIME string, byteSize int64, clientSHA, objectKey, tokenHash string, expiresAt time.Time) (*LibraryUpload, error) {
	return db.CreateLibraryUploadForConversation(ctx, userID, spaceID, "", purpose, filename, declaredMIME, byteSize, clientSHA, objectKey, tokenHash, expiresAt)
}

func (db *Database) CreateLibraryUploadForConversation(ctx context.Context, userID, spaceID, conversationID, purpose, filename, declaredMIME string, byteSize int64, clientSHA, objectKey, tokenHash string, expiresAt time.Time) (*LibraryUpload, error) {
	permission, purposeKnown := UploadPurposePermission(purpose)
	maxBytes := MaxUploadBytesForPurpose(purpose)
	if !purposeKnown || byteSize < 1 || byteSize > maxBytes || byteSize > MaxSpaceStorageBytes || len(clientSHA) != 64 || filename == "" || objectKey == "" || tokenHash == "" {
		return nil, ErrLibraryInvalid
	}
	out := &LibraryUpload{ID: "upload_" + uuid.NewString(), SpaceID: spaceID, UserID: userID, ObjectKey: objectKey, OriginalFilename: filename, Purpose: purpose, ClientDeclaredMIMEType: declaredMIME, RequestedByteSize: byteSize, ClientSHA256: clientSHA, State: "initiated", UploadTokenHash: tokenHash, ExpiresAt: expiresAt, Version: 1}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if err := requireSpacePermissionTx(ctx, tx, userID, spaceID, permission); err != nil {
			return err
		}
		var ownerID string
		if err := tx.QueryRowContext(ctx, `SELECT security_domain_id,owner_user_id FROM spaces WHERE id=$1 FOR SHARE`, spaceID).Scan(&out.SecurityDomainID, &ownerID); err != nil {
			return err
		}
		if purpose == "attachment" {
			if err := requireSpaceMessageWriteTx(ctx, tx, userID, spaceID); err != nil {
				return err
			}
			if conversationID != "" {
				if err := requireSpaceConversationMemberTx(ctx, tx, userID, spaceID, conversationID); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "owner-storage:"+ownerID); err != nil {
			return err
		}
		ownerUsage, err := ownerStorageUsageTx(ctx, tx, ownerID, true)
		if err != nil {
			return err
		}
		if ownerUsage.UsedBytes+ownerUsage.ReservedBytes+byteSize > ownerUsage.LimitBytes {
			return ErrLibraryQuota
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
		if err := tx.QueryRowContext(ctx, `INSERT INTO space_library_uploads(id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,state,upload_token_hash,expires_at,conversation_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'initiated',$11,$12,NULLIF($13,'')) RETURNING created_at,updated_at`, out.ID, spaceID, out.SecurityDomainID, userID, objectKey, filename, purpose, declaredMIME, byteSize, clientSHA, tokenHash, expiresAt, conversationID).Scan(&out.CreatedAt, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO space_upload_reservations(upload_id,space_id,user_id,reserved_bytes,state,expires_at) VALUES($1,$2,$3,$4,'active',$5)`, out.ID, spaceID, userID, byteSize, expiresAt); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE space_storage_usage SET reserved_bytes=reserved_bytes+$1,version=version+1,updated_at=NOW() WHERE space_id=$2`, byteSize, spaceID); err != nil {
			return err
		}
		return insertLibraryAuditTx(ctx, tx, spaceID, out.SecurityDomainID, userID, "library.upload.initiated", "upload", out.ID, "success", map[string]any{"purpose": purpose, "reserved_bytes": byteSize})
	})
	return out, err
}

func (db *Database) LibraryUpload(ctx context.Context, userID, spaceID, uploadID string) (*LibraryUpload, error) {
	out := &LibraryUpload{}
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return ErrLibraryForbidden
		}
		return scanLibraryUpload(tx.QueryRowContext(ctx, `SELECT id,space_id,security_domain_id,user_id,object_key,original_filename,purpose,client_declared_mime_type,requested_byte_size,client_sha256,verified_byte_size,COALESCE(verified_sha256,''),COALESCE(detected_mime_type,''),state,COALESCE(file_id,''),upload_token_hash,COALESCE(error_code,''),expires_at,version,created_at,updated_at FROM space_library_uploads WHERE id=$1 AND space_id=$2 AND user_id=$3`, uploadID, spaceID, userID), out)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLibraryNotFound
	}
	return out, err
}

// LibraryUploadDeduplicationObjectKey returns the object currently selected as
// the deduplication target for an upload. The object store must verify this key
// before finalization reuses it; a ready database row alone does not prove the
// immutable object still exists in R2.
func (db *Database) LibraryUploadDeduplicationObjectKey(ctx context.Context, userID, spaceID, uploadID string) (string, error) {
	var objectKey string
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		canUploadLibrary, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionLibraryUpload)
		if err != nil {
			return err
		}
		canUploadAttachment, err := hasSpacePermissionTx(ctx, tx, userID, spaceID, PermissionAttachmentUpload)
		if err != nil {
			return err
		}
		if !canUploadLibrary && !canUploadAttachment {
			return ErrLibraryForbidden
		}
		err = tx.QueryRowContext(ctx, `SELECT b.r2_object_key
			FROM space_library_uploads u
			JOIN library_blobs b ON b.security_domain_id=u.security_domain_id AND b.sha256=u.client_sha256 AND b.byte_size=u.requested_byte_size AND b.lifecycle_state='ready'
			WHERE u.id=$1 AND u.space_id=$2 AND u.user_id=$3 AND u.state='uploaded_unverified' AND b.r2_object_key<>u.object_key
			LIMIT 1`, uploadID, spaceID, userID).Scan(&objectKey)
		if errors.Is(err, sql.ErrNoRows) {
			objectKey = ""
			return nil
		}
		return err
	})
	return objectKey, err
}
