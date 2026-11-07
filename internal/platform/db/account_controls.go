package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AccountLimits are what an account may use on this server. A nil field is
// unlimited, which is every account's default (a self-hosted server sets none).
type AccountLimits struct {
	StorageBytes *int64 `json:"storage_bytes"`
	Devices      *int64 `json:"devices"`
}

var (
	// ErrAccountSuspended refuses sign-in and API use for a suspended account.
	ErrAccountSuspended = errors.New("account_suspended")
	// ErrAccountNotFound names an account the operator asked for that does not exist.
	ErrAccountNotFound = errors.New("account not found")
	// ErrDeviceLimit refuses one more device than the account's limit.
	ErrDeviceLimit = errors.New("device_limit_reached")
	// ErrNoDevicesAllowed refuses a device on an account allowed none.
	ErrNoDevicesAllowed = errors.New("no_devices_allowed")
)

func (db *Database) serviceTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return db.WithRLSContext(ctx, ServiceRLSSettings(), fn)
}

// SetAccountLimits replaces the account's limits.
func (db *Database) SetAccountLimits(ctx context.Context, userID string, limits AccountLimits) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveAccountTx(ctx, tx, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO account_controls(user_id,storage_bytes,devices)
			VALUES($1,$2,$3)
			ON CONFLICT(user_id) DO UPDATE SET storage_bytes=EXCLUDED.storage_bytes,devices=EXCLUDED.devices,updated_at=now()`,
			userID, limits.StorageBytes, limits.Devices)
		return err
	})
}

// AccountLimitsFor returns the account's limits; an account with no row is unlimited.
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
	var storage, devices sql.NullInt64
	var suspended sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT storage_bytes,devices,suspended_at
		FROM account_controls WHERE user_id=$1`, userID).Scan(&storage, &devices, &suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return limits, nil, nil
	}
	if err != nil {
		return limits, nil, err
	}
	limits = AccountLimits{nullableInt(storage), nullableInt(devices)}
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

// SuspendAccount stops the account signing in or using the API and ends its
// sessions. Nothing it stored is removed.
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

// ResumeAccount lets a suspended account sign in again, makes a closing one
// writable again and cancels its scheduled deletion. Once the deletion has
// begun the account is gone.
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

// AccountSuspended reports whether the account is suspended.
func (db *Database) AccountSuspended(ctx context.Context, userID string) (bool, error) {
	var suspended bool
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_controls WHERE user_id=$1 AND suspended_at IS NOT NULL)`, userID).Scan(&suspended)
	})
	return suspended, err
}

// SuspendedAccounts lists every suspended account, for the request guard.
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

// PendingAccountInvitation reports whether an existing account invited this
// email to a Space and the invitation is still open. After the first account,
// that is how a person may register on this server by themselves.
func (db *Database) PendingAccountInvitation(ctx context.Context, email string) (bool, error) {
	var invited bool
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM space_invitations
			WHERE lower(invited_email)=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>now())`,
			normalizeEmail(email)).Scan(&invited)
	})
	return invited, err
}
