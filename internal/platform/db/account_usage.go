package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// usagePeriod is the first day of the account's current billing period: the
// last time its anchor's day came round (the 29th to 31st fall on a shorter
// month's last day), or the first of the calendar month without an anchor.
// The account is the query's $1. Times are UTC.
const usagePeriod = `misty_usage_period((SELECT period_anchor FROM account_controls WHERE user_id=$1), now())`

// ConsumeAccountUsage counts amount uses of a metered action this period, or
// refuses with UsageLimitError when that would pass the account's limit.
// Unlimited accounts are still counted, so the operator sees their use.
func (db *Database) ConsumeAccountUsage(ctx context.Context, userID, metric string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	if metric != UsageAppActions {
		return errors.New("unknown usage metric")
	}
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		limits, _, err := accountControlsTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		limit := limits.AppActionsPerMonth
		if limit != nil && amount > *limit {
			return usageLimitErrorTx(ctx, tx, userID, metric, *limit)
		}
		// The conditional upsert keeps concurrent callers from both passing
		// the last unit: the row lock orders them and the WHERE re-checks.
		var used int64
		err = tx.QueryRowContext(ctx, `INSERT INTO account_usage(user_id,metric,period,used) VALUES($1,$2,`+usagePeriod+`,$3)
			ON CONFLICT(user_id,metric,period) DO UPDATE SET used=account_usage.used+EXCLUDED.used,updated_at=now()
			WHERE $4::bigint IS NULL OR account_usage.used+EXCLUDED.used <= $4::bigint
			RETURNING used`, userID, metric, amount, limit).Scan(&used)
		if errors.Is(err, sql.ErrNoRows) {
			return usageLimitErrorTx(ctx, tx, userID, metric, *limit)
		}
		return err
	})
}

func usageLimitErrorTx(ctx context.Context, tx *sql.Tx, userID, metric string, limit int64) error {
	var anchor sql.NullTime
	var start time.Time
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT period_anchor FROM account_controls WHERE user_id=$1),`+usagePeriod, userID).Scan(&anchor, &start); err != nil {
		return err
	}
	var at *time.Time
	if anchor.Valid {
		at = &anchor.Time
	}
	return UsageLimitError{Metric: metric, Limit: limit, Renews: NextUsagePeriod(at, start)}
}

// NextUsagePeriod is when the period that began on start ends: the anchor's
// day in the following month, clamped to that month's last day, or the first
// of the next month without an anchor.
func NextUsagePeriod(anchor *time.Time, start time.Time) time.Time {
	start = start.UTC()
	first := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	if anchor == nil {
		return first
	}
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(anchor.UTC().Day(), last), 0, 0, 0, 0, time.UTC)
}

// ReleaseAccountUsage gives back uses that were counted for an action that
// then did not happen, such as an app action the app refused before running it.
func (db *Database) ReleaseAccountUsage(ctx context.Context, userID, metric string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	return db.serviceTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE account_usage SET used=GREATEST(used-$3,0),updated_at=now()
			WHERE user_id=$1 AND metric=$2 AND period=`+usagePeriod, userID, metric, amount)
		return err
	})
}

// MonthUsage is an account's metered use in its current billing period,
// which began on PeriodStart.
type MonthUsage struct {
	AppActions  int64     `json:"app_actions"`
	PeriodStart time.Time `json:"period_start"`
}

// AccountMonthUsage returns this period's metered use.
func (db *Database) AccountMonthUsage(ctx context.Context, userID string) (MonthUsage, error) {
	var usage MonthUsage
	err := db.serviceTx(ctx, func(tx *sql.Tx) error {
		var err error
		usage, err = accountMonthUsageTx(ctx, tx, userID)
		return err
	})
	return usage, err
}

func accountMonthUsageTx(ctx context.Context, tx *sql.Tx, userID string) (MonthUsage, error) {
	var usage MonthUsage
	err := tx.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT used FROM account_usage WHERE user_id=$1 AND metric='app_actions' AND period=`+usagePeriod+`),0),
		`+usagePeriod, userID).Scan(&usage.AppActions, &usage.PeriodStart)
	return usage, err
}
