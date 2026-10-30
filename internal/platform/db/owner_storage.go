package db

import (
	"context"
	"database/sql"
)

// StorageUsageDimension is what an account or a Space holds in cloud storage.
type StorageUsageDimension struct {
	UsedBytes     int64 `json:"used_bytes"`
	ReservedBytes int64 `json:"reserved_bytes"`
}

// OwnerStorageUsage describes the authenticated user's personal contribution
// across owned and joined Spaces.
type OwnerStorageUsage struct {
	OwnerUserID   string                   `json:"owner_user_id,omitempty"`
	UserID        string                   `json:"user_id,omitempty"`
	UsedBytes     int64                    `json:"used_bytes"`
	ReservedBytes int64                    `json:"reserved_bytes"`
	Version       int64                    `json:"version"`
	Spaces        []OwnerSpaceStorageUsage `json:"spaces"`
	Personal      StorageUsageDimension    `json:"personal"`
}

type OwnerSpaceStorageUsage struct {
	SpaceID       string `json:"space_id"`
	Name          string `json:"name"`
	UsedBytes     int64  `json:"used_bytes"`
	ReservedBytes int64  `json:"reserved_bytes"`
}

func personalStorageUsageTx(ctx context.Context, tx *sql.Tx, userID string) (OwnerStorageUsage, error) {
	measured, err := accountStorageTx(ctx, tx, userID)
	if err != nil {
		return OwnerStorageUsage{}, err
	}
	return OwnerStorageUsage{OwnerUserID: userID, UserID: userID, Version: 1, Spaces: []OwnerSpaceStorageUsage{},
		UsedBytes: measured.UsedBytes, ReservedBytes: measured.ReservedBytes, Personal: measured}, nil
}

func (db *Database) OwnerStorageUsage(ctx context.Context, userID string) (*OwnerStorageUsage, error) {
	var out OwnerStorageUsage
	err := db.SpaceTx(ctx, func(tx *sql.Tx) error {
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
