package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrAccountClosing = errors.New("account_closing")

type AccountClosingError struct{ DeleteAt time.Time }

func (e AccountClosingError) Error() string        { return "account_closing" }
func (e AccountClosingError) Is(target error) bool { return target == ErrAccountClosing }

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

func (db *Database) RequireAccountWritable(ctx context.Context, userID string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		return RequireAccountWritableTx(ctx, tx, userID)
	})
}

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
