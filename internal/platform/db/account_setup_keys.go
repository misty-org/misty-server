package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SetupKeyTTL is how long a setup key (a sign-in link) stays valid.
const SetupKeyTTL = 7 * 24 * time.Hour

// ErrSetupKeyInvalid covers a key that is unknown, used, replaced or expired.
// The caller cannot tell which, so a guess learns nothing.
var ErrSetupKeyInvalid = errors.New("setup_key_invalid")

// IssueSetupKey stores a new key (by hash) for the account and replaces every
// earlier key that was not used, so only the newest link works.
func (db *Database) IssueSetupKey(ctx context.Context, userID, keyHash string, expiresAt time.Time) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveAccountTx(ctx, tx, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE account_setup_keys SET revoked_at=now()
			WHERE user_id=$1 AND used_at IS NULL AND revoked_at IS NULL`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO account_setup_keys(key_hash,user_id,expires_at) VALUES($1,$2,$3)`, keyHash, userID, expiresAt)
		return err
	})
}

// RedeemSetupKey marks the key used and returns its account. A suspended
// account's key is refused with ErrAccountSuspended and stays unused.
func (db *Database) RedeemSetupKey(ctx context.Context, keyHash string) (*User, error) {
	var userID string
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT k.user_id FROM account_setup_keys k JOIN users u ON u.id=k.user_id
			WHERE k.key_hash=$1 AND k.used_at IS NULL AND k.revoked_at IS NULL AND k.expires_at>now() AND u.lifecycle_state='active'
			FOR UPDATE OF k`, keyHash).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSetupKeyInvalid
		}
		if err != nil {
			return err
		}
		var suspended bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_controls WHERE user_id=$1 AND suspended_at IS NOT NULL)`, userID).Scan(&suspended); err != nil {
			return err
		}
		if suspended {
			return ErrAccountSuspended
		}
		_, err = tx.ExecContext(ctx, `UPDATE account_setup_keys SET used_at=now() WHERE key_hash=$1`, keyHash)
		return err
	})
	if err != nil {
		return nil, err
	}
	user, err := db.GetUserByID(userID)
	if err == nil && user == nil {
		err = ErrSetupKeyInvalid
	}
	return user, err
}

// SetupState says whether an account still waits for its first sign-in.
type SetupState struct {
	// Done: a key was used or a password set, so the person has signed in.
	Done bool `json:"done"`
	// LiveKey: an unused key that has not expired is out.
	LiveKey bool `json:"live_key"`
	// LastSentAt is when the newest key was issued.
	LastSentAt *time.Time `json:"last_sent_at,omitempty"`
}

func accountSetupStateTx(ctx context.Context, tx *sql.Tx, userID string) (SetupState, error) {
	var state SetupState
	var last sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM account_setup_keys WHERE user_id=$1 AND used_at IS NOT NULL)
		  OR EXISTS(SELECT 1 FROM users WHERE id=$1 AND (password_hash<>'' OR provider<>'misty')),
		EXISTS(SELECT 1 FROM account_setup_keys WHERE user_id=$1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at>now()),
		(SELECT max(created_at) FROM account_setup_keys WHERE user_id=$1)`, userID).Scan(&state.Done, &state.LiveKey, &last)
	if last.Valid {
		state.LastSentAt = &last.Time
	}
	return state, err
}

// AccountSetupState reports where the account is in its first sign-in.
func (db *Database) AccountSetupState(ctx context.Context, userID string) (SetupState, error) {
	var state SetupState
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		var err error
		state, err = accountSetupStateTx(ctx, tx, userID)
		return err
	})
	return state, err
}
