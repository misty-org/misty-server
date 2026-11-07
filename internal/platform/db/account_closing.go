package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Closing an account: the operator account API, or the
// billing service through it, sets the day the account's data is deleted.
// Until then the account is read-only (account_read_only.go): it signs in,
// reads and exports, and changes nothing. On the date the account-deletion
// worker deletes it with the same full purge as a person deleting their own
// account. The server sends no email: telling the person is the billing
// service's job (or the operator's). Resuming before then cancels it all. A
// suspension is separate and, when both apply, wins.

// ErrAccountPendingDeletion refuses an operator create for an email whose
// account is being deleted: the email is free again once the purge finishes.
var ErrAccountPendingDeletion = errors.New("account_pending_deletion")

// CloseAccount makes the account read-only, stops its agent runs and
// schedules, and schedules its deletion for deleteAt. Its sessions stay, so it
// can still export. Closing an account that is already closing keeps the date
// set first, so a retried request never moves it; resume, then close again, to
// change it. It returns the date that holds.
func (db *Database) CloseAccount(ctx context.Context, userID string, deleteAt time.Time) (time.Time, error) {
	var held time.Time
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveAccountTx(ctx, tx, userID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO account_controls(user_id,delete_at) VALUES($1,$2)
			ON CONFLICT(user_id) DO UPDATE SET delete_at=COALESCE(account_controls.delete_at,EXCLUDED.delete_at),updated_at=now()
			RETURNING delete_at`, userID, deleteAt.UTC()).Scan(&held); err != nil {
			return err
		}
		return nil
	})
	return held.UTC(), err
}

const closingAccounts = `account_controls c JOIN users u ON u.id=c.user_id AND u.lifecycle_state='active'
	WHERE c.delete_at IS NOT NULL`

// DueClosedAccounts lists closed accounts whose deletion date has come.
func (db *Database) DueClosedAccounts(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	ids := []string{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.user_id FROM `+closingAccounts+` AND c.delete_at<=now()
			ORDER BY c.delete_at LIMIT $1`, limit)
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

// BeginClosedAccountDeletion starts the purge of a closed account whose date
// has come: Spaces it owns that other people use pass to their longest-standing
// member, and a deletion request due at once takes the account through the
// same steps as self-deletion. It does nothing when the account was resumed.
func (db *Database) BeginClosedAccountDeletion(ctx context.Context, userID, requestID, statusTokenHash string) (bool, error) {
	started := false
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		var due bool
		err := tx.QueryRowContext(ctx, `SELECT c.delete_at<=now() FROM `+closingAccounts+` AND c.user_id=$1
			FOR UPDATE OF u, c`, userID).Scan(&due)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !due) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := handOverSharedSpacesTx(ctx, tx, userID); err != nil {
			return err
		}
		if err := beginAccountDeletionTx(ctx, tx, userID, requestID, statusTokenHash, time.Now().UTC(), false); err != nil {
			return err
		}
		started = true
		return nil
	})
	return started, err
}

// handOverSharedSpacesTx gives each active Space the account owns that has
// other members to the member who joined first, as an ownership transfer
// would. The personal Space has no other members.
func handOverSharedSpacesTx(ctx context.Context, tx *sql.Tx, userID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.id,(SELECT m.user_id FROM space_members m
		WHERE m.space_id=s.id AND m.user_id<>$1 ORDER BY m.joined_at,m.user_id LIMIT 1)
		FROM spaces s WHERE s.owner_user_id=$1 AND s.lifecycle_state='active'
		AND EXISTS(SELECT 1 FROM space_members m WHERE m.space_id=s.id AND m.user_id<>$1)
		ORDER BY s.created_at FOR UPDATE OF s`, userID)
	if err != nil {
		return err
	}
	type handover struct{ space, member string }
	handovers := []handover{}
	for rows.Next() {
		var item handover
		if err := rows.Scan(&item.space, &item.member); err != nil {
			rows.Close()
			return err
		}
		handovers = append(handovers, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range handovers {
		for _, step := range []struct {
			query string
			args  []any
		}{
			{`UPDATE space_members SET role='member' WHERE space_id=$1 AND user_id=$2`, []any{item.space, userID}},
			{`UPDATE space_members SET role='owner' WHERE space_id=$1 AND user_id=$2`, []any{item.space, item.member}},
			{`UPDATE spaces SET owner_user_id=$2,updated_at=NOW() WHERE id=$1`, []any{item.space, item.member}},
			{`UPDATE security_domains SET owner_user_id=$2,version=version+1,updated_at=NOW() WHERE space_id=$1 AND kind='space'`, []any{item.space, item.member}},
		} {
			if _, err := tx.ExecContext(ctx, step.query, step.args...); err != nil {
				return err
			}
		}
		if _, err := recordSpaceEventTx(ctx, tx, item.space, userID, "owner.transferred", item.member, map[string]any{"reason": "account_closed"}); err != nil {
			return err
		}
	}
	return nil
}

// SetAccountPeriodAnchor sets when the account's billing periods start, or
// clears it (nil) for calendar months.
func (db *Database) SetAccountPeriodAnchor(ctx context.Context, userID string, anchor *time.Time) error {
	var value any
	if anchor != nil {
		value = anchor.UTC()
	}
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		if err := requireActiveAccountTx(ctx, tx, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO account_controls(user_id,period_anchor) VALUES($1,$2)
			ON CONFLICT(user_id) DO UPDATE SET period_anchor=EXCLUDED.period_anchor,updated_at=now()`, userID, value)
		return err
	})
}

// accountPendingDeletionTx reports whether the email belongs to an account
// that is being deleted.
func accountPendingDeletionTx(ctx context.Context, tx *sql.Tx, email string) (bool, error) {
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(email)=$1 AND lifecycle_state='pending_deletion')`, email).Scan(&pending)
	return pending, err
}
