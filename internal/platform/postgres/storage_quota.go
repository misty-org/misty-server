package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

var (
	ErrPersonalStorageQuota = fmt.Errorf("personal storage quota exceeded: %w", ErrLibraryQuota)
	ErrSpaceStorageQuota    = fmt.Errorf("space storage quota exceeded: %w", ErrLibraryQuota)
)

type storageQuotaState struct {
	Personal StorageQuotaDimension
	Space    StorageQuotaDimension
}

func remainingStorageBytes(used, reserved, limit int64) int64 {
	remaining := limit - used - reserved
	if remaining < 0 {
		return 0
	}
	return remaining
}

// storageQuotaStateTx is the single source of storage quota policy. With lock
// enabled it serializes both the contributor's global allowance and the
// destination Space, so concurrent writes across different Spaces cannot race
// the personal limit and concurrent members cannot race the Space limit.
func storageQuotaStateTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, lock bool) (storageQuotaState, error) {
	if lock {
		keys := []string{"storage-personal:" + userID, "storage-space:" + spaceID}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, key); err != nil {
				return storageQuotaState{}, err
			}
		}
	}
	personal, err := personalStorageUsageTx(ctx, tx, userID)
	if err != nil {
		return storageQuotaState{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID); err != nil {
		return storageQuotaState{}, err
	}
	query := `SELECT s.owner_user_id,u.used_bytes,u.reserved_bytes
		FROM spaces s JOIN space_storage_usage u ON u.space_id=s.id
		WHERE s.id=$1 AND s.lifecycle_state='active'`
	if lock {
		query += ` FOR UPDATE OF u`
	}
	var ownerID string
	var spaceUsed, spaceReserved int64
	if err := tx.QueryRowContext(ctx, query, spaceID).Scan(&ownerID, &spaceUsed, &spaceReserved); err != nil {
		return storageQuotaState{}, err
	}
	ownerEntitlements, err := entitlementsForUserTx(ctx, tx, ownerID, time.Now())
	if err != nil {
		return storageQuotaState{}, err
	}
	spaceLimit := ownerEntitlements.SpaceStorageLimitBytes
	return storageQuotaState{
		Personal: personal.Personal,
		Space: StorageQuotaDimension{
			UsedBytes: spaceUsed, ReservedBytes: spaceReserved,
			LimitBytes:     spaceLimit,
			RemainingBytes: remainingStorageBytes(spaceUsed, spaceReserved, spaceLimit),
			OverQuota:      spaceUsed+spaceReserved > spaceLimit,
		},
	}, nil
}

func reserveStorageQuotaTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, bytes int64) (storageQuotaState, error) {
	state, err := storageQuotaStateTx(ctx, tx, userID, spaceID, true)
	if err != nil {
		return storageQuotaState{}, err
	}
	if bytes > state.Personal.RemainingBytes {
		return state, ErrPersonalStorageQuota
	}
	if bytes > state.Space.RemainingBytes {
		return state, ErrSpaceStorageQuota
	}
	return state, nil
}

func storageQuotaError(state storageQuotaState, bytes int64) error {
	if bytes > state.Personal.RemainingBytes {
		return ErrPersonalStorageQuota
	}
	if bytes > state.Space.RemainingBytes {
		return ErrSpaceStorageQuota
	}
	return nil
}
