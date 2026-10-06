package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/kannachi323/misty/server/internal/billingadapter"
	"github.com/kannachi323/misty/server/internal/cloudusage"
	envconfig "github.com/kannachi323/misty/server/internal/platform/config"
)

var (
	ErrPersonalStorageQuota = fmt.Errorf("account cloud storage quota exceeded: %w", ErrLibraryQuota)
	// Compatibility error for old callers; new admissions never return it.
	ErrSpaceStorageQuota = fmt.Errorf("account cloud storage quota exceeded: %w", ErrLibraryQuota)
)

type storageQuotaState struct {
	BillingAvailable bool
	Personal         StorageQuotaDimension
	Space            StorageQuotaDimension
}

func storageQuotaStateTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, lock bool) (storageQuotaState, error) {
	if lock {
		if err := cloudusage.Lock(ctx, tx, userID); err != nil {
			return storageQuotaState{}, err
		}
	}
	personal, err := personalStorageUsageTx(ctx, tx, userID)
	if err != nil {
		return storageQuotaState{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID); err != nil {
		return storageQuotaState{}, err
	}
	var space StorageQuotaDimension
	if err = tx.QueryRowContext(ctx, `SELECT used_bytes,reserved_bytes FROM space_storage_usage WHERE space_id=$1`, spaceID).Scan(&space.UsedBytes, &space.ReservedBytes); err != nil {
		return storageQuotaState{}, err
	}
	// Space counters are attribution only; no Space allowance or owner-plan lookup.
	return storageQuotaState{BillingAvailable: true, Personal: personal.Personal, Space: space}, nil
}
func reserveStorageQuotaTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, bytes int64) (storageQuotaState, error) {
	_, err := approveCloudStorageTx(ctx, tx, userID, "storage.check", map[string]int64{"requested_bytes": bytes})
	if err != nil {
		return storageQuotaState{}, err
	}
	// Maintain the native attribution row used by existing upload transactions.
	_, err = tx.ExecContext(ctx, `INSERT INTO space_storage_usage(space_id) VALUES($1) ON CONFLICT DO NOTHING`, spaceID)
	return storageQuotaState{BillingAvailable: true}, err
}
func approveCloudStorageTx(ctx context.Context, tx *sql.Tx, userID, operation string, units map[string]int64) (int64, error) {
	if err := cloudusage.Lock(ctx, tx, userID); err != nil {
		return 0, err
	}
	adapter, err := envconfig.BillingAdapter()
	if err != nil {
		return 0, err
	}
	if !adapter.Enabled() {
		if units["requested_bytes"] > 0 {
			return units["requested_bytes"], nil
		}
		return max(int64(1_000_000), units["source_bytes"]), nil
	}
	decision, err := cloudusage.Check(ctx, tx, adapter, userID, operation, units)
	if errors.Is(err, billingadapter.ErrDenied) {
		return 0, ErrPersonalStorageQuota
	}
	return decision.ApprovedBytes, err
}
