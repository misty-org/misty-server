package db

import (
	"context"
	"database/sql"
)

// asServiceTx runs fn with the transaction in service RLS scope and restores
// the scope it had. Limit checks run inside callers' transactions, some of
// which have no RLS scope at all; account_controls must still be readable.
func asServiceTx(ctx context.Context, tx *sql.Tx, fn func() error) error {
	var previous sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT current_setting('app.rls_mode',true)`).Scan(&previous); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode','service',true)`); err != nil {
		return err
	}
	err := fn()
	if _, restoreErr := tx.ExecContext(ctx, `SELECT set_config('app.rls_mode',$1,true)`, previous.String); err == nil {
		err = restoreErr
	}
	return err
}

// requireDeviceRoomTx refuses a new device once the account holds as many
// devices as its limit allows. Pending and added devices count; removed ones
// do not, so removing a device frees its place.
func requireDeviceRoomTx(ctx context.Context, tx *sql.Tx, userID string) error {
	return asServiceTx(ctx, tx, func() error {
		limits, _, err := accountControlsTx(ctx, tx, userID)
		if err != nil || limits.Devices == nil {
			return err
		}
		// Two new devices registering at once must not both take the last place.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "devices:"+userID); err != nil {
			return err
		}
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM trusted_devices
			WHERE user_id=$1 AND revoked_at IS NULL AND admission_state<>'revoked'`, userID).Scan(&count); err != nil {
			return err
		}
		if *limits.Devices <= 0 {
			return ErrNoDevicesAllowed
		}
		if count >= *limits.Devices {
			return ErrDeviceLimit
		}
		return nil
	})
}

// syncStorageBytesTx is the ciphertext the account's sync vaults hold.
func syncStorageBytesTx(ctx context.Context, tx *sql.Tx, userID string) (int64, error) {
	var bytes int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(r.sealed)),0) FROM sync_records r JOIN sync_vaults v USING(vault_id) WHERE v.user_id=$1`,
		userID).Scan(&bytes)
	return bytes, err
}

// accountStorageUsedTx is everything the account stores on this server: its
// Library, notes, messages and pending uploads, plus its encrypted sync vault.
func accountStorageUsedTx(ctx context.Context, tx *sql.Tx, userID string) (int64, error) {
	var used int64
	err := asServiceTx(ctx, tx, func() error {
		library, err := accountStorageTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		vault, err := syncStorageBytesTx(ctx, tx, userID)
		used = library.UsedBytes + library.ReservedBytes + vault
		return err
	})
	return used, err
}

// RequireAccountStorageRoomTx refuses adding bytes that would pass the
// account's storage limit with ErrLibraryQuota. Accounts without a limit are
// not measured at all. Callers hold the account's storage lock where they
// reserve (prepareStorageReservationTx), so concurrent uploads cannot both pass.
func RequireAccountStorageRoomTx(ctx context.Context, tx *sql.Tx, userID string, adding int64) error {
	if adding <= 0 {
		return nil
	}
	var limit *int64
	if err := asServiceTx(ctx, tx, func() error {
		limits, _, err := accountControlsTx(ctx, tx, userID)
		limit = limits.StorageBytes
		return err
	}); err != nil || limit == nil {
		return err
	}
	used, err := accountStorageUsedTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	if used+adding > *limit {
		return ErrLibraryQuota
	}
	return nil
}

// requireSpaceSharingRoomTx refuses one more invitation once the owner's account is at a sharing
// limit: the people one Space may hold (members and open invitations both count), or the number
// of its Spaces that may be shared at all. A Space already shared never counts against itself.
func requireSpaceSharingRoomTx(ctx context.Context, tx *sql.Tx, ownerID, spaceID string) error {
	return asServiceTx(ctx, tx, func() error {
		limits, _, err := accountControlsTx(ctx, tx, ownerID)
		if err != nil || (limits.SpaceMembers == nil && limits.SharedSpaces == nil) {
			return err
		}
		const people = `(SELECT count(*) FROM space_members m WHERE m.space_id=s.id) +
			(SELECT count(*) FROM space_invitations i WHERE i.space_id=s.id AND i.expires_at>NOW()
			  AND i.revoked_at IS NULL AND i.consumed_at IS NULL)`
		var here int64
		if err := tx.QueryRowContext(ctx, `SELECT `+people+` FROM spaces s WHERE s.id=$1`, spaceID).Scan(&here); err != nil {
			return err
		}
		if limits.SpaceMembers != nil && here >= *limits.SpaceMembers {
			return ErrSpacePeopleLimit
		}
		if limits.SharedSpaces == nil || here > 1 {
			return nil
		}
		var shared int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM spaces s
			WHERE s.owner_user_id=$1 AND s.lifecycle_state='active' AND s.id<>$2 AND `+people+` > 1`,
			ownerID, spaceID).Scan(&shared); err != nil {
			return err
		}
		if shared >= *limits.SharedSpaces {
			return ErrSpaceLimit
		}
		return nil
	})
}
