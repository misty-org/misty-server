package db

import (
	"context"
	"database/sql"
	"time"
)

type OwnerStorageUsage struct {
	OwnerUserID        string                   `json:"owner_user_id,omitempty"`
	UsedBytes          int64                    `json:"used_bytes"`
	ReservedBytes      int64                    `json:"reserved_bytes"`
	LimitBytes         int64                    `json:"limit_bytes"`
	RemainingBytes     int64                    `json:"remaining_bytes"`
	OverQuota          bool                     `json:"over_quota"`
	OverQuotaSince     *time.Time               `json:"over_quota_since,omitempty"`
	CleanupNoticeUntil *time.Time               `json:"cleanup_notice_until,omitempty"`
	Version            int64                    `json:"version"`
	Spaces             []OwnerSpaceStorageUsage `json:"spaces"`
}

type OwnerSpaceStorageUsage struct {
	SpaceID       string `json:"space_id"`
	Name          string `json:"name"`
	UsedBytes     int64  `json:"used_bytes"`
	ReservedBytes int64  `json:"reserved_bytes"`
}

func ownerStorageUsageTx(ctx context.Context, tx *sql.Tx, ownerID string, lock bool) (OwnerStorageUsage, error) {
	entitlements, err := entitlementsForUserTx(ctx, tx, ownerID, time.Now())
	if err != nil {
		return OwnerStorageUsage{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO owner_storage_usage(owner_user_id) VALUES($1) ON CONFLICT DO NOTHING`, ownerID); err != nil {
		return OwnerStorageUsage{}, err
	}
	query := `SELECT owner_user_id,used_bytes,reserved_bytes,over_quota_since,version FROM owner_storage_usage WHERE owner_user_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var out OwnerStorageUsage
	var over sql.NullTime
	if err := tx.QueryRowContext(ctx, query, ownerID).Scan(&out.OwnerUserID, &out.UsedBytes, &out.ReservedBytes, &over, &out.Version); err != nil {
		return OwnerStorageUsage{}, err
	}
	out.LimitBytes = entitlements.StorageLimitBytes
	out.RemainingBytes = out.LimitBytes - out.UsedBytes - out.ReservedBytes
	if out.RemainingBytes < 0 {
		out.RemainingBytes = 0
	}
	if over.Valid {
		value := over.Time
		out.OverQuotaSince = &value
		cleanup := value.Add(30 * 24 * time.Hour)
		out.CleanupNoticeUntil = &cleanup
	}
	isOver := out.UsedBytes+out.ReservedBytes > out.LimitBytes
	out.OverQuota = isOver
	if isOver && out.OverQuotaSince == nil {
		now := time.Now().UTC()
		out.OverQuotaSince = &now
		_, err = tx.ExecContext(ctx, `UPDATE owner_storage_usage SET over_quota_since=$2,updated_at=NOW() WHERE owner_user_id=$1`, ownerID, now)
	} else if !isOver && out.OverQuotaSince != nil {
		out.OverQuotaSince = nil
		out.CleanupNoticeUntil = nil
		_, err = tx.ExecContext(ctx, `UPDATE owner_storage_usage SET over_quota_since=NULL,updated_at=NOW() WHERE owner_user_id=$1`, ownerID)
	}
	return out, err
}

func (db *Database) OwnerStorageUsage(ctx context.Context, userID string) (*OwnerStorageUsage, error) {
	var out OwnerStorageUsage
	err := db.TestingSpaceTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = ownerStorageUsageTx(ctx, tx, userID, false)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.name,COALESCE(u.used_bytes,0),COALESCE(u.reserved_bytes,0)
			FROM spaces s LEFT JOIN space_storage_usage u ON u.space_id=s.id
			WHERE s.owner_user_id=$1 AND s.lifecycle_state='active' AND s.kind='standard'
			ORDER BY s.created_at`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		out.Spaces = []OwnerSpaceStorageUsage{}
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
