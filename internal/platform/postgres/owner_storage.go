package db

import (
	"context"
	"database/sql"
	"time"
)

// StorageQuotaDimension is the additive, explicit representation used by new
// clients. The older flat fields remain populated on the containing response.
type StorageQuotaDimension struct {
	UsedBytes      int64 `json:"used_bytes"`
	ReservedBytes  int64 `json:"reserved_bytes"`
	LimitBytes     int64 `json:"limit_bytes"`
	RemainingBytes int64 `json:"remaining_bytes"`
	OverQuota      bool  `json:"over_quota"`
}

// OwnerStorageUsage is retained as the public compatibility type and method
// name. Its flat values now describe the authenticated user's global personal
// contribution across owned and joined Spaces, rather than storage in Spaces
// that user owns.
type OwnerStorageUsage struct {
	OwnerUserID        string                   `json:"owner_user_id,omitempty"`
	UserID             string                   `json:"user_id,omitempty"`
	UsedBytes          int64                    `json:"used_bytes"`
	ReservedBytes      int64                    `json:"reserved_bytes"`
	LimitBytes         int64                    `json:"limit_bytes"`
	RemainingBytes     int64                    `json:"remaining_bytes"`
	OverQuota          bool                     `json:"over_quota"`
	OverQuotaSince     *time.Time               `json:"over_quota_since,omitempty"`
	CleanupNoticeUntil *time.Time               `json:"cleanup_notice_until,omitempty"`
	Version            int64                    `json:"version"`
	Spaces             []OwnerSpaceStorageUsage `json:"spaces"`
	Personal           StorageQuotaDimension    `json:"personal"`
}

// PersonalStorageUsage is the preferred domain name for new callers.
type PersonalStorageUsage = OwnerStorageUsage

type OwnerSpaceStorageUsage struct {
	SpaceID       string `json:"space_id"`
	Name          string `json:"name"`
	UsedBytes     int64  `json:"used_bytes"`
	ReservedBytes int64  `json:"reserved_bytes"`
}

func personalStorageUsageTx(ctx context.Context, tx *sql.Tx, userID string) (OwnerStorageUsage, error) {
	entitlements, err := entitlementsForUserTx(ctx, tx, userID, time.Now())
	if err != nil {
		return OwnerStorageUsage{}, err
	}
	out := OwnerStorageUsage{OwnerUserID: userID, UserID: userID, Version: 1, Spaces: []OwnerSpaceStorageUsage{}}
	if err := tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT sum(c.logical_bytes)
			FROM space_storage_contributions c JOIN spaces s ON s.id=c.space_id
			WHERE c.user_id=$1 AND c.state IN ('active','recovery') AND s.lifecycle_state='active'),0),
		COALESCE((SELECT sum(r.reserved_bytes)
			FROM space_upload_reservations r JOIN spaces s ON s.id=r.space_id
			WHERE r.user_id=$1 AND r.state='active' AND s.lifecycle_state='active'),0)
		+ COALESCE((SELECT sum(r.reserved_bytes)
			FROM space_rendition_reservations r JOIN spaces s ON s.id=r.space_id
			WHERE r.user_id=$1 AND r.state='active' AND s.lifecycle_state='active'),0)`, userID).
		Scan(&out.UsedBytes, &out.ReservedBytes); err != nil {
		return OwnerStorageUsage{}, err
	}
	out.LimitBytes = entitlements.PersonalStorageLimitBytes
	out.RemainingBytes = remainingStorageBytes(out.UsedBytes, out.ReservedBytes, out.LimitBytes)
	out.OverQuota = out.UsedBytes+out.ReservedBytes > out.LimitBytes
	out.Personal = StorageQuotaDimension{
		UsedBytes: out.UsedBytes, ReservedBytes: out.ReservedBytes,
		LimitBytes: out.LimitBytes, RemainingBytes: out.RemainingBytes,
		OverQuota: out.OverQuota,
	}
	return out, nil
}

func (db *Database) OwnerStorageUsage(ctx context.Context, userID string) (*OwnerStorageUsage, error) {
	var out OwnerStorageUsage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = personalStorageUsageTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.name,
			COALESCE((SELECT sum(c.logical_bytes) FROM space_storage_contributions c
				WHERE c.space_id=s.id AND c.user_id=$1 AND c.state IN ('active','recovery')),0),
			COALESCE((SELECT sum(r.reserved_bytes) FROM space_upload_reservations r
				WHERE r.space_id=s.id AND r.user_id=$1 AND r.state='active'),0)
			+ COALESCE((SELECT sum(r.reserved_bytes) FROM space_rendition_reservations r
				WHERE r.space_id=s.id AND r.user_id=$1 AND r.state='active'),0)
			FROM spaces s WHERE s.lifecycle_state='active' AND (
				EXISTS(SELECT 1 FROM space_storage_contributions c WHERE c.space_id=s.id AND c.user_id=$1 AND c.state IN ('active','recovery')) OR
				EXISTS(SELECT 1 FROM space_upload_reservations r WHERE r.space_id=s.id AND r.user_id=$1 AND r.state='active') OR
				EXISTS(SELECT 1 FROM space_rendition_reservations r WHERE r.space_id=s.id AND r.user_id=$1 AND r.state='active'))
			ORDER BY s.created_at`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item OwnerSpaceStorageUsage
			if err := rows.Scan(&item.SpaceID, &item.Name, &item.UsedBytes, &item.ReservedBytes); err != nil {
				return err
			}
			out.Spaces = append(out.Spaces, item)
		}
		return rows.Err()
	})
	return &out, err
}

func (db *Database) PersonalStorageUsage(ctx context.Context, userID string) (*PersonalStorageUsage, error) {
	return db.OwnerStorageUsage(ctx, userID)
}
