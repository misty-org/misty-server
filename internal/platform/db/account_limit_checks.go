package db

import (
	"context"
	"database/sql"
)

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

func accountStorageUsedTx(ctx context.Context, tx *sql.Tx, userID string) (int64, error) {
	var used int64
	err := asServiceTx(ctx, tx, func() error {
		library, err := accountStorageTx(ctx, tx, userID)
		used = library.UsedBytes + library.ReservedBytes
		return err
	})
	return used, err
}

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

func requireSpaceRoomTx(ctx context.Context, tx *sql.Tx, userID string) error {
	return asServiceTx(ctx, tx, func() error {
		limits, _, err := accountControlsTx(ctx, tx, userID)
		if err != nil || limits.Spaces == nil {
			return err
		}
		var belongs int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM space_members m
			JOIN spaces s ON s.id=m.space_id AND s.lifecycle_state='active' AND s.kind='space' WHERE m.user_id=$1`, userID).Scan(&belongs); err != nil {
			return err
		}
		if belongs >= *limits.Spaces {
			return ErrSpaceCountLimit
		}
		return nil
	})
}

func lockAccountSpacesTx(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "spaces:member:"+userID)
	return err
}

func requireSpaceSharingRoomTx(ctx context.Context, tx *sql.Tx, ownerID, spaceID string) error {
	return asServiceTx(ctx, tx, func() error {
		limits, _, err := accountControlsTx(ctx, tx, ownerID)
		if err != nil {
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
		if err := requireSpaceMembersRoomTx(ctx, tx, spaceID, here); err != nil {
			return err
		}
		if limits.SharedSpaces == nil || here > 1 {
			return nil
		}
		var shared int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM spaces s
			WHERE s.owner_user_id=$1 AND s.lifecycle_state='active' AND s.kind='space' AND s.id<>$2 AND `+people+` > 1`,
			ownerID, spaceID).Scan(&shared); err != nil {
			return err
		}
		if shared >= *limits.SharedSpaces {
			return ErrSpaceLimit
		}
		return nil
	})
}
