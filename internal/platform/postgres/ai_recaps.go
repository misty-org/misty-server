package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AIRecap struct {
	UserID           string          `json:"-"`
	SurfaceID        string          `json:"surface_id"`
	Enabled          bool            `json:"enabled"`
	Cadence          string          `json:"cadence"`
	LocalTime        string          `json:"local_time"`
	Weekday          int             `json:"weekday"`
	Timezone         string          `json:"timezone"`
	Prompt           string          `json:"prompt"`
	State            string          `json:"state"`
	NextRunAt        *time.Time      `json:"next_run_at,omitempty"`
	LastInvocationID string          `json:"last_invocation_id,omitempty"`
	LastResult       string          `json:"last_result,omitempty"`
	LastCitations    json.RawMessage `json:"last_citations"`
	LastError        string          `json:"last_error,omitempty"`
	LastRunAt        *time.Time      `json:"last_run_at,omitempty"`
	LastSeenAt       *time.Time      `json:"last_seen_at,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

const aiRecapColumns = `user_id,surface_id,enabled,cadence,local_time,weekday,timezone,prompt,state,next_run_at,
	COALESCE(last_invocation_id,''),last_result,last_citations,last_error,last_run_at,last_seen_at,updated_at`

func scanAIRecap(scanner interface{ Scan(...any) error }, item *AIRecap) error {
	return scanner.Scan(
		&item.UserID, &item.SurfaceID, &item.Enabled, &item.Cadence, &item.LocalTime,
		&item.Weekday, &item.Timezone, &item.Prompt, &item.State, &item.NextRunAt,
		&item.LastInvocationID, &item.LastResult, &item.LastCitations, &item.LastError,
		&item.LastRunAt, &item.LastSeenAt, &item.UpdatedAt,
	)
}

func (db *Database) AIRecaps(ctx context.Context, userID string) ([]AIRecap, error) {
	items := []AIRecap{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+aiRecapColumns+` FROM ai_recaps WHERE user_id=$1 ORDER BY surface_id`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIRecap
			if err := scanAIRecap(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) UpsertAIRecap(ctx context.Context, userID string, item AIRecap, now time.Time) (*AIRecap, error) {
	if !validAIRecap(item) {
		return nil, ErrSpaceInvalid
	}
	next, err := NextAIRecapAt(item.Cadence, item.LocalTime, item.Weekday, item.Timezone, now)
	if err != nil {
		return nil, ErrSpaceInvalid
	}
	if !item.Enabled {
		next = time.Time{}
	}
	var nextValue any
	if !next.IsZero() {
		nextValue = next
	}
	out := &AIRecap{}
	err = db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return scanAIRecap(tx.QueryRowContext(ctx, `
			INSERT INTO ai_recaps(user_id,surface_id,enabled,cadence,local_time,weekday,timezone,prompt,next_run_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT(user_id,surface_id) DO UPDATE SET enabled=EXCLUDED.enabled,cadence=EXCLUDED.cadence,
				local_time=EXCLUDED.local_time,weekday=EXCLUDED.weekday,timezone=EXCLUDED.timezone,
				prompt=EXCLUDED.prompt,next_run_at=EXCLUDED.next_run_at,state='idle',lease_until=NULL,updated_at=NOW()
			RETURNING `+aiRecapColumns,
			userID, item.SurfaceID, item.Enabled, item.Cadence, item.LocalTime, item.Weekday,
			item.Timezone, strings.TrimSpace(item.Prompt), nextValue), out)
	})
	return out, err
}

func validAIRecap(item AIRecap) bool {
	if strings.TrimSpace(item.SurfaceID) == "" || len(item.Prompt) > 8000 {
		return false
	}
	if item.Cadence != "daily" && item.Cadence != "weekly" || item.Weekday < 0 || item.Weekday > 6 {
		return false
	}
	if len(item.LocalTime) != 5 || item.LocalTime[2] != ':' {
		return false
	}
	_, err := time.Parse("15:04", item.LocalTime)
	if err != nil || strings.TrimSpace(item.Timezone) == "" || item.Timezone == "local" {
		return false
	}
	_, err = time.LoadLocation(item.Timezone)
	return err == nil
}

func NextAIRecapAt(cadence, localTime string, weekday int, timezone string, after time.Time) (time.Time, error) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil || location.String() == "Local" {
		return time.Time{}, errors.New("invalid recap timezone")
	}
	clock, err := time.Parse("15:04", localTime)
	if err != nil {
		return time.Time{}, err
	}
	local := after.In(location)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, location)
	if cadence == "weekly" {
		days := (weekday - int(candidate.Weekday()) + 7) % 7
		candidate = candidate.AddDate(0, 0, days)
	}
	if !candidate.After(local) {
		if cadence == "weekly" {
			candidate = candidate.AddDate(0, 0, 7)
		} else {
			candidate = candidate.AddDate(0, 0, 1)
		}
	}
	return candidate.UTC(), nil
}

func (db *Database) ClaimDueAIRecaps(ctx context.Context, now time.Time, limit int) ([]AIRecap, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	items := []AIRecap{}
	err := db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			UPDATE ai_recaps r SET state='running',lease_until=$1+INTERVAL '10 minutes',updated_at=NOW()
			WHERE (r.user_id,r.surface_id) IN (
				SELECT r2.user_id,r2.surface_id FROM ai_recaps r2
				WHERE r2.enabled
				  AND COALESCE((SELECT s.enabled FROM ai_user_settings s WHERE s.user_id=r2.user_id),TRUE)
				  AND r2.next_run_at<=$1
				  AND (r2.state<>'running' OR r2.lease_until<=$1)
				ORDER BY r2.next_run_at FOR UPDATE SKIP LOCKED LIMIT $2
			) RETURNING `+aiRecapColumns, now, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AIRecap
			if err := scanAIRecap(rows, &item); err != nil {
				return err
			}
			items = append(items, item)
		}
		return rows.Err()
	})
	return items, err
}

func (db *Database) CompleteAIRecap(ctx context.Context, item AIRecap, invocationID, result string, citations json.RawMessage, runErr error, now time.Time) error {
	next, err := NextAIRecapAt(item.Cadence, item.LocalTime, item.Weekday, item.Timezone, now)
	if err != nil {
		return err
	}
	state, message := "idle", ""
	if runErr != nil {
		state, message = "failed", runErr.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	if len(result) > 100000 {
		result = result[:100000]
	}
	return db.TestingWithRLSContext(ctx, TestingServiceRLSSettings(), func(tx *sql.Tx) error {
		updated, err := tx.ExecContext(ctx, `
			UPDATE ai_recaps SET state=$1,next_run_at=$2,lease_until=NULL,last_invocation_id=NULLIF($3,''),
				last_result=CASE WHEN $1='idle' THEN $4 ELSE last_result END,
				last_citations=CASE WHEN $1='idle' THEN $5 ELSE last_citations END,last_error=$6,
				last_run_at=$7,updated_at=NOW()
			WHERE user_id=$8 AND surface_id=$9 AND state='running'
		`, state, next, invocationID, result, jsonOr(citations, `[]`), message, now, item.UserID, item.SurfaceID)
		if err != nil {
			return err
		}
		rows, _ := updated.RowsAffected()
		if rows != 1 {
			return fmt.Errorf("%w: recap lease", ErrSpaceConflict)
		}
		return nil
	})
}

func (db *Database) MarkAIRecapSeen(ctx context.Context, userID, surfaceID string) error {
	return db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE ai_recaps SET last_seen_at=NOW(),updated_at=NOW() WHERE user_id=$1 AND surface_id=$2`, userID, surfaceID)
		if err != nil {
			return err
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			return ErrSpaceNotFound
		}
		return nil
	})
}
