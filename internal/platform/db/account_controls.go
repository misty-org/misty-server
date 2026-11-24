package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type AccountLimits struct {
	StorageBytes         *int64 `json:"storage_bytes"`
	Devices              *int64 `json:"devices"`
	SharedSpaces         *int64 `json:"shared_spaces"`
	SpaceMembers         *int64 `json:"space_members"`
	Spaces               *int64 `json:"spaces"`
	UploadBytes          *int64 `json:"upload_bytes"`
	FriendRequestsPerDay *int64 `json:"friend_requests_per_day"`
	IntegrationsPerSpace *int64 `json:"integrations_per_space"`
}

var (
	ErrAccountSuspended = errors.New("account_suspended")
	ErrAccountNotFound  = errors.New("account not found")
)

func (db *Database) serviceTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), fn)
}

func (db *Database) SetAccountLimits(ctx context.Context, userID string, limits AccountLimits) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveAccountTx(ctx, tx, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO account_controls(user_id,storage_bytes,devices,shared_spaces,space_members,spaces,upload_bytes,friend_requests_per_day,integrations_per_space)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT(user_id) DO UPDATE SET storage_bytes=EXCLUDED.storage_bytes,devices=EXCLUDED.devices,
			shared_spaces=EXCLUDED.shared_spaces,space_members=EXCLUDED.space_members,spaces=EXCLUDED.spaces,
			upload_bytes=EXCLUDED.upload_bytes,friend_requests_per_day=EXCLUDED.friend_requests_per_day,
			integrations_per_space=EXCLUDED.integrations_per_space,updated_at=now()`,
			userID, limits.StorageBytes, limits.Devices, limits.SharedSpaces, limits.SpaceMembers, limits.Spaces, limits.UploadBytes, limits.FriendRequestsPerDay,
			limits.IntegrationsPerSpace)
		return err
	})
}

func (db *Database) AccountLimitsFor(ctx context.Context, userID string) (AccountLimits, error) {
	var limits AccountLimits
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		var err error
		limits, _, err = accountControlsTx(ctx, tx, userID)
		return err
	})
	return limits, err
}

func accountControlsTx(ctx context.Context, tx *sql.Tx, userID string) (AccountLimits, *time.Time, error) {
	var limits AccountLimits
	var storage, devices, sharedSpaces, spaceMembers, spaces, uploads, friendRequests, integrations sql.NullInt64
	var suspended sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT storage_bytes,devices,shared_spaces,space_members,spaces,upload_bytes,friend_requests_per_day,integrations_per_space,suspended_at
		FROM account_controls WHERE user_id=$1`, userID).Scan(&storage, &devices, &sharedSpaces, &spaceMembers, &spaces, &uploads, &friendRequests, &integrations, &suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return limits, nil, nil
	}
	if err != nil {
		return limits, nil, err
	}
	limits = AccountLimits{
		StorageBytes: nullableInt(storage), Devices: nullableInt(devices),
		SharedSpaces: nullableInt(sharedSpaces), SpaceMembers: nullableInt(spaceMembers),
		Spaces: nullableInt(spaces), UploadBytes: nullableInt(uploads),
		FriendRequestsPerDay: nullableInt(friendRequests), IntegrationsPerSpace: nullableInt(integrations),
	}
	if suspended.Valid {
		return limits, &suspended.Time, nil
	}
	return limits, nil, nil
}

func nullableInt(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func requireActiveAccountTx(ctx context.Context, tx *sql.Tx, userID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND lifecycle_state='active')`, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrAccountNotFound
	}
	return nil
}

func (db *Database) SuspendAccount(ctx context.Context, userID string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveAccountTx(ctx, tx, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_controls(user_id,suspended_at) VALUES($1,now())
			ON CONFLICT(user_id) DO UPDATE SET suspended_at=COALESCE(account_controls.suspended_at,now()),updated_at=now()`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		return err
	})
}

func (db *Database) ResumeAccount(ctx context.Context, userID string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveAccountTx(ctx, tx, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE account_controls SET suspended_at=NULL,delete_at=NULL,
			updated_at=now() WHERE user_id=$1`, userID)
		return err
	})
}

func (db *Database) AccountSuspended(ctx context.Context, userID string) (bool, error) {
	var suspended bool
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_controls WHERE user_id=$1 AND suspended_at IS NOT NULL)`, userID).Scan(&suspended)
	})
	return suspended, err
}

func (db *Database) SuspendedAccounts(ctx context.Context) ([]string, error) {
	ids := []string{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT user_id FROM account_controls WHERE suspended_at IS NOT NULL`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

func (db *Database) PendingAccountInvitation(ctx context.Context, email string) (bool, error) {
	var invited bool
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_invitations
			WHERE lower(invited_email)=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>now())`,
			normalizeEmail(email)).Scan(&invited)
	})
	return invited, err
}
