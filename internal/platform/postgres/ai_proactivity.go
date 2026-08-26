package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (db *Database) RecordAIProactiveEvent(ctx context.Context, userID, surfaceID, event string, snoozeMinutes int, now time.Time) (AISurfacePreference, error) {
	surfaceID = strings.TrimSpace(surfaceID)
	event = strings.TrimSpace(event)
	if surfaceID == "" || event != "shown" && event != "snoozed" && event != "dismissed" {
		return AISurfacePreference{}, ErrSpaceInvalid
	}
	if event == "snoozed" && (snoozeMinutes < 60 || snoozeMinutes > 10080) {
		return AISurfacePreference{}, ErrSpaceInvalid
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := AISurfacePreference{}
	err := db.TestingWithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		query := ""
		arguments := []any{userID, surfaceID, now}
		switch event {
		case "shown":
			query = `UPDATE ai_surface_preferences p SET proactive_last_shown_at=$3,updated_at=NOW()
				WHERE p.user_id=$1 AND p.surface_id=$2 AND p.proactive_enabled
					AND COALESCE(p.proactive_snoozed_until,'epoch'::timestamptz)<=$3
					AND (p.proactive_last_shown_at IS NULL OR p.proactive_last_shown_at<=
						$3-(p.proactive_cooldown_minutes::text||' minutes')::interval)
					AND COALESCE((SELECT enabled FROM ai_user_settings WHERE user_id=$1),TRUE)`
		case "snoozed":
			query = `UPDATE ai_surface_preferences SET
				proactive_snoozed_until=$3::timestamptz+($4::text||' minutes')::interval,updated_at=NOW()
				WHERE user_id=$1 AND surface_id=$2 AND proactive_enabled`
			arguments = append(arguments, snoozeMinutes)
		case "dismissed":
			query = `UPDATE ai_surface_preferences SET proactive_dismissed_at=$3,
				proactive_snoozed_until=$3::timestamptz+INTERVAL '7 days',updated_at=NOW()
				WHERE user_id=$1 AND surface_id=$2 AND proactive_enabled`
		}
		query += ` RETURNING surface_id,COALESCE(pinned_agent_id,''),proactive_enabled,
			proactive_cooldown_minutes,proactive_snoozed_until,proactive_last_shown_at,
			proactive_dismissed_at,saved_actions,updated_at`
		err := tx.QueryRowContext(ctx, query, arguments...).Scan(
			&out.SurfaceID, &out.PinnedAgentID, &out.Proactive, &out.ProactiveCooldownMinutes,
			&out.ProactiveSnoozedUntil, &out.ProactiveLastShownAt, &out.ProactiveDismissedAt,
			&out.SavedActions, &out.UpdatedAt,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSpaceConflict
		}
		return err
	})
	return out, err
}
