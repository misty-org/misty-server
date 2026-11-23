package db

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

type LoginFailureCap struct {
	Key   string
	Limit int
}

func (db *Database) LoginFailuresBlocked(ctx context.Context, caps []LoginFailureCap, window time.Duration) (bool, time.Duration, error) {
	blocked := false
	var retryAfter time.Duration
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		for _, limit := range caps {
			if limit.Limit < 1 {
				continue
			}
			var seconds float64
			err := tx.QueryRowContext(ctx, `SELECT EXTRACT(EPOCH FROM (failed_at + make_interval(secs => $2) - clock_timestamp()))
   FROM login_failures WHERE key=$1 AND failed_at > clock_timestamp() - make_interval(secs => $2)
   ORDER BY failed_at DESC OFFSET $3 LIMIT 1`, limit.Key, window.Seconds(), limit.Limit-1).Scan(&seconds)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			blocked = true
			if wait := time.Duration(math.Max(seconds, 0) * float64(time.Second)); wait > retryAfter {
				retryAfter = wait
			}
		}
		return nil
	})
	return blocked, retryAfter, err
}

func (db *Database) RecordLoginFailure(ctx context.Context, keys []string, window time.Duration) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		for _, key := range keys {
			if _, err := tx.ExecContext(ctx, `INSERT INTO login_failures(key) VALUES($1)`, key); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM login_failures WHERE failed_at <= clock_timestamp() - make_interval(secs => $1)`, window.Seconds())
		return err
	})
}

func (db *Database) ClearLoginFailures(ctx context.Context, keys []string) error {
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		for _, key := range keys {
			if _, err := tx.ExecContext(ctx, `DELETE FROM login_failures WHERE key=$1`, key); err != nil {
				return err
			}
		}
		return nil
	})
}
