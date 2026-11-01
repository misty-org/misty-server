package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A closing account (docs/cloud/README.md#closing-an-account) is read-only
// until its deletion date: it signs in, reads and exports, and changes
// nothing. The HTTP guard refuses its writes before any handler runs; the
// checks here cover writes that arrive another way (the sync socket, agent
// runs another member or a schedule starts) and back the guard up.

// ErrAccountClosing refuses a change from a closing account.
var ErrAccountClosing = errors.New("account_closing")

// AccountClosingError is ErrAccountClosing with the day the account is
// deleted, which the refusal names.
type AccountClosingError struct{ DeleteAt time.Time }

func (e AccountClosingError) Error() string        { return "account_closing" }
func (e AccountClosingError) Is(target error) bool { return target == ErrAccountClosing }

// accountDeleteAtTx is when the closing account is deleted, or nil when it
// isn't closing. It reads in service scope whatever scope tx has.
func accountDeleteAtTx(ctx context.Context, tx *sql.Tx, userID string) (*time.Time, error) {
	var deleteAt sql.NullTime
	err := asServiceTx(ctx, tx, func() error {
		err := tx.QueryRowContext(ctx, `SELECT delete_at FROM account_controls WHERE user_id=$1`, userID).Scan(&deleteAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil || !deleteAt.Valid {
		return nil, err
	}
	at := deleteAt.Time.UTC()
	return &at, nil
}

// RequireAccountWritableTx refuses with AccountClosingError when the account
// is closing.
func RequireAccountWritableTx(ctx context.Context, tx *sql.Tx, userID string) error {
	deleteAt, err := accountDeleteAtTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	if deleteAt != nil {
		return AccountClosingError{DeleteAt: *deleteAt}
	}
	return nil
}

// RequireAccountWritable is RequireAccountWritableTx in a transaction of its own.
func (db *Database) RequireAccountWritable(ctx context.Context, userID string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		return RequireAccountWritableTx(ctx, tx, userID)
	})
}

// ClosingAccounts maps every closing account to its deletion date, for the
// request guard.
func (db *Database) ClosingAccounts(ctx context.Context) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT c.user_id,c.delete_at FROM `+closingAccounts)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var at time.Time
			if err := rows.Scan(&id, &at); err != nil {
				return err
			}
			out[id] = at.UTC()
		}
		return rows.Err()
	})
	return out, err
}

// stopClosingAccountWorkTx cancels the agent work a closing account has under
// way: its personal and inbound invocations, and the runs it asked for or its
// agents were doing. Schedules stay as they are and wait (ClaimDueWorkflowSchedules
// skips closing accounts), so resuming the account brings them back.
func stopClosingAccountWorkTx(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='canceled',error_code='account_closing',
		canceled_at=NOW(),updated_at=NOW() WHERE user_id=$1
		AND state IN ('queued','running','awaiting_device','awaiting_intervention','awaiting_timer')`, userID); err != nil {
		return err
	}
	return cancelAccountRunsTx(ctx, tx, userID, "account_closing")
}
