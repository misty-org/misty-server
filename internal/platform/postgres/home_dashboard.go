package db

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"
)

const homeActivityRetentionDays = 370

var homeDateKeyPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

type HomeDashboardSnapshot struct {
	Activity   map[string]int `json:"activity"`
	RecentApps []string       `json:"recent_apps"`
}

func (db *Database) HomeDashboard(ctx context.Context, userID, spaceID string) (HomeDashboardSnapshot, error) {
	out := emptyHomeDashboardSnapshot()
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		return readHomeDashboardTx(ctx, tx, userID, spaceID, &out)
	})
	return out, err
}

func (db *Database) RecordHomeVisit(ctx context.Context, userID, spaceID, dateKey string) (HomeDashboardSnapshot, error) {
	out := emptyHomeDashboardSnapshot()
	dateKey = strings.TrimSpace(dateKey)
	if !homeDateKeyPattern.MatchString(dateKey) {
		return out, ErrSpaceInvalid
	}
	if _, err := time.Parse("2006-01-02", dateKey); err != nil {
		return out, ErrSpaceInvalid
	}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := requireSpaceMemberTx(ctx, tx, spaceID, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_home_activity(user_id,space_id,activity_date)
			VALUES($1,$2,$3::date) ON CONFLICT(user_id,space_id,activity_date) DO UPDATE SET
			visit_count=LEAST(user_home_activity.visit_count+1,1000000),updated_at=NOW()`, userID, spaceID, dateKey); err != nil {
			return err
		}
		return readHomeDashboardTx(ctx, tx, userID, spaceID, &out)
	})
	return out, err
}

func (db *Database) RecordAppActivity(ctx context.Context, userID, appID string) error {
	appID = strings.TrimSpace(appID)
	if len(appID) < 1 || len(appID) > 80 {
		return ErrSpaceInvalid
	}
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_app_activity(user_id,app_id)
			VALUES($1,$2) ON CONFLICT(user_id,app_id) DO UPDATE SET
			open_count=user_app_activity.open_count+1,last_opened_at=NOW()`, userID, appID)
		return err
	})
}

func emptyHomeDashboardSnapshot() HomeDashboardSnapshot {
	return HomeDashboardSnapshot{Activity: map[string]int{}, RecentApps: []string{}}
}

func readHomeDashboardTx(ctx context.Context, tx *sql.Tx, userID, spaceID string, out *HomeDashboardSnapshot) error {
	rows, err := tx.QueryContext(ctx, `SELECT activity_date::text,visit_count FROM user_home_activity
		WHERE user_id=$1 AND space_id=$2 AND activity_date>=CURRENT_DATE-($3::int-1)
		ORDER BY activity_date`, userID, spaceID, homeActivityRetentionDays)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var dateKey string
		var count int
		if err := rows.Scan(&dateKey, &count); err != nil {
			return err
		}
		out.Activity[dateKey] = count
	}
	if err := rows.Err(); err != nil {
		return err
	}

	appRows, err := tx.QueryContext(ctx, `SELECT app_id FROM user_app_activity
		WHERE user_id=$1 ORDER BY last_opened_at DESC,app_id LIMIT 10`, userID)
	if err != nil {
		return err
	}
	defer appRows.Close()
	for appRows.Next() {
		var appID string
		if err := appRows.Scan(&appID); err != nil {
			return err
		}
		out.RecentApps = append(out.RecentApps, appID)
	}
	return appRows.Err()
}
