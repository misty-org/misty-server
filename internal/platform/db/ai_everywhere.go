package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (db *Database) AIActionAvailable(ctx context.Context, userID, surfaceID, actionID, modelID string) (bool, error) {
	enabled := true
	rollout := 100
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var userEnabled bool
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled FROM ai_user_settings WHERE user_id=$1),TRUE)`, userID).Scan(&userEnabled); err != nil {
			return err
		}
		if !userEnabled {
			enabled = false
			rollout = 0
			return nil
		}
		err := tx.QueryRowContext(ctx, `
			SELECT enabled,rollout_percent FROM ai_feature_flags
			WHERE surface_id IN ('*',$1) AND action_id IN ('*',$2) AND model_id IN ('*',$3)
			ORDER BY (surface_id=$1)::int+(action_id=$2)::int+(model_id=$3)::int DESC,updated_at DESC
			LIMIT 1
		`, surfaceID, actionID, modelID).Scan(&enabled, &rollout)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil || !enabled || rollout <= 0 {
		return false, err
	}
	if rollout >= 100 {
		return true, nil
	}
	digest := sha256.Sum256([]byte(userID + "\x00" + surfaceID + "\x00" + actionID + "\x00" + modelID))
	cohort := int(digest[0])<<8 | int(digest[1])
	return cohort%100 < rollout, nil
}

type AIUserSettings struct {
	Enabled                bool `json:"enabled"`
	CursorCompanionEnabled bool `json:"cursor_companion_enabled"`
	MemoryEnabled          bool `json:"memory_enabled"`
	// AppActionsAsk pauses sends, shares, deletes and payments until the user
	// approves them: in chat for connected apps, in the island on pages. Off by
	// default, so agents act on their own.
	AppActionsAsk bool `json:"app_actions_ask"`
	// PaymentsAsk pauses paying and buying even while AppActionsAsk is off.
	PaymentsAsk bool `json:"payments_ask"`
	// ScreenLocation is where agents open a new screen: window (a new tab in the
	// user's Misty window, the default) or ask. Rows saved as separate, the
	// retired agent window, read as window.
	ScreenLocation string `json:"screen_location"`
	// AgentBlockSensitive keeps agents from acting on banking, crypto, payment
	// and adult sites; AgentBlockedSites adds the account's own.
	AgentBlockSensitive bool     `json:"agent_block_sensitive"`
	AgentBlockedSites   []string `json:"agent_blocked_sites"`
	// KeepAwakeForAgents keeps a computer from idling to sleep while an agent
	// works on it.
	KeepAwakeForAgents bool      `json:"keep_awake_for_agents"`
	RetentionDays      int       `json:"retention_days"`
	PurgeState         string    `json:"purge_state"`
	DisabledAt         time.Time `json:"disabled_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type AISurfacePreference struct {
	SurfaceID                string          `json:"surface_id"`
	Proactive                bool            `json:"proactive_enabled"`
	ProactiveCooldownMinutes int             `json:"proactive_cooldown_minutes"`
	ProactiveSnoozedUntil    *time.Time      `json:"proactive_snoozed_until,omitempty"`
	ProactiveLastShownAt     *time.Time      `json:"proactive_last_shown_at,omitempty"`
	ProactiveDismissedAt     *time.Time      `json:"proactive_dismissed_at,omitempty"`
	SavedActions             json.RawMessage `json:"saved_actions"`
	UpdatedAt                time.Time       `json:"updated_at"`
}

func (db *Database) AISettings(ctx context.Context, userID string) (AIUserSettings, []AISurfacePreference, error) {
	settings := AIUserSettings{Enabled: true, CursorCompanionEnabled: true, MemoryEnabled: true, PaymentsAsk: true, ScreenLocation: "window", AgentBlockSensitive: true, AgentBlockedSites: []string{}, KeepAwakeForAgents: true, RetentionDays: 30, PurgeState: "none"}
	preferences := []AISurfacePreference{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		var disabledAt sql.NullTime
		err := tx.QueryRowContext(ctx, `
			SELECT enabled,cursor_companion_enabled,memory_enabled,app_actions_ask,payments_ask,screen_location,agent_block_sensitive,agent_blocked_sites,keep_awake_for_agents,retention_days,purge_state,disabled_at,updated_at FROM ai_user_settings WHERE user_id=$1
		`, userID).Scan(&settings.Enabled, &settings.CursorCompanionEnabled, &settings.MemoryEnabled, &settings.AppActionsAsk, &settings.PaymentsAsk, &settings.ScreenLocation, &settings.AgentBlockSensitive, pq.Array(&settings.AgentBlockedSites), &settings.KeepAwakeForAgents, &settings.RetentionDays, &settings.PurgeState, &disabledAt, &settings.UpdatedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if settings.ScreenLocation == "separate" {
			settings.ScreenLocation = "window"
		}
		if disabledAt.Valid {
			settings.DisabledAt = disabledAt.Time
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT surface_id,proactive_enabled,proactive_cooldown_minutes,
				proactive_snoozed_until,proactive_last_shown_at,proactive_dismissed_at,saved_actions,updated_at
			FROM ai_surface_preferences WHERE user_id=$1 ORDER BY surface_id
		`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item AISurfacePreference
			if err := rows.Scan(&item.SurfaceID, &item.Proactive, &item.ProactiveCooldownMinutes,
				&item.ProactiveSnoozedUntil, &item.ProactiveLastShownAt, &item.ProactiveDismissedAt,
				&item.SavedActions, &item.UpdatedAt); err != nil {
				return err
			}
			preferences = append(preferences, item)
		}
		return rows.Err()
	})
	return settings, preferences, err
}

// SetAIAppActionsAsk changes only the approval settings; nil leaves a value as it is.
func (db *Database) SetAIAppActionsAsk(ctx context.Context, userID string, ask, payments *bool) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_user_settings(user_id,app_actions_ask,payments_ask) VALUES($1,COALESCE($2,false),COALESCE($3,true))
			ON CONFLICT(user_id) DO UPDATE SET app_actions_ask=COALESCE($2,ai_user_settings.app_actions_ask),
				payments_ask=COALESCE($3,ai_user_settings.payments_ask),updated_at=NOW()`, userID, ask, payments)
		return err
	})
}

// SetAIAgentSiteBlocks changes which sites agents may not act on. Sites are
// already normalized hosts; nil leaves a value as it is.
func (db *Database) SetAIAgentSiteBlocks(ctx context.Context, userID string, sensitive *bool, sites []string) error {
	if len(sites) > 200 {
		return ErrSpaceInvalid
	}
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ai_user_settings(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, userID); err != nil {
			return err
		}
		if sensitive != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE ai_user_settings SET agent_block_sensitive=$2,updated_at=NOW() WHERE user_id=$1`, userID, *sensitive); err != nil {
				return err
			}
		}
		if sites != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE ai_user_settings SET agent_blocked_sites=$2,updated_at=NOW() WHERE user_id=$1`, userID, pq.Array(sites)); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetAIKeepAwake changes whether computers stay awake while agents work on them.
func (db *Database) SetAIKeepAwake(ctx context.Context, userID string, keepAwake bool) error {
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_user_settings(user_id,keep_awake_for_agents) VALUES($1,$2)
			ON CONFLICT(user_id) DO UPDATE SET keep_awake_for_agents=EXCLUDED.keep_awake_for_agents,updated_at=NOW()`, userID, keepAwake)
		return err
	})
}

// SetAIScreenLocation changes only where agents open a screen.
func (db *Database) SetAIScreenLocation(ctx context.Context, userID, location string) error {
	if location != "window" && location != "ask" {
		return ErrSpaceInvalid
	}
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_user_settings(user_id,screen_location) VALUES($1,$2)
			ON CONFLICT(user_id) DO UPDATE SET screen_location=EXCLUDED.screen_location,updated_at=NOW()`, userID, location)
		return err
	})
}

func (db *Database) UpdateAISettings(ctx context.Context, userID string, enabled bool, retentionDays int, cursorCompanionEnabled, memoryEnabled bool) (AIUserSettings, error) {
	if retentionDays < 1 || retentionDays > 365 {
		return AIUserSettings{}, ErrSpaceInvalid
	}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		purgeState := "none"
		if !enabled {
			purgeState = "queued"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ai_user_settings(user_id,enabled,retention_days,purge_state,disabled_at,cursor_companion_enabled,memory_enabled)
			VALUES($1,$2,$3,$4,CASE WHEN $2 THEN NULL ELSE NOW() END,$5,$6)
			ON CONFLICT(user_id) DO UPDATE SET enabled=EXCLUDED.enabled,retention_days=EXCLUDED.retention_days,
				purge_state=EXCLUDED.purge_state,disabled_at=EXCLUDED.disabled_at,
				cursor_companion_enabled=EXCLUDED.cursor_companion_enabled,
				memory_enabled=EXCLUDED.memory_enabled,updated_at=NOW()
		`, userID, enabled, retentionDays, purgeState, cursorCompanionEnabled, memoryEnabled); err != nil {
			return err
		}
		if enabled {
			_, err := tx.ExecContext(ctx, `DELETE FROM ai_cleanup_jobs WHERE user_id=$1 AND state IN ('queued','failed','verified')`, userID)
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET state='canceled',canceled_at=NOW(),updated_at=NOW() WHERE user_id=$1 AND state IN ('queued','running','awaiting_approval','awaiting_device','awaiting_intervention','awaiting_timer')`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_retrieval_documents WHERE owner_user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_invocations i WHERE i.user_id=$1 AND NOT EXISTS(SELECT 1 FROM ai_artifacts a WHERE a.invocation_id=i.id AND a.state='applied')`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_invocations SET request_payload='{}'::jsonb,updated_at=NOW() WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ai_artifacts SET sources='[]'::jsonb,operations='{}'::jsonb,updated_at=NOW() WHERE user_id=$1 AND state='applied'`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_feedback WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_surface_preferences WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_recaps WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM misty_memories WHERE user_id=$1`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_cleanup_jobs(id,user_id) VALUES($1,$2)`, "aicleanup_"+uuid.NewString(), userID)
		return err
	})
	if err != nil {
		return AIUserSettings{}, err
	}
	settings, _, err := db.AISettings(ctx, userID)
	return settings, err
}

func (db *Database) UpsertAISurfacePreference(ctx context.Context, userID string, preference AISurfacePreference) (AISurfacePreference, error) {
	if strings.TrimSpace(preference.SurfaceID) == "" {
		return AISurfacePreference{}, ErrSpaceInvalid
	}
	if len(preference.SavedActions) == 0 {
		preference.SavedActions = json.RawMessage(`[]`)
	}
	out := AISurfacePreference{}
	err := db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
			INSERT INTO ai_surface_preferences(user_id,surface_id,proactive_enabled,saved_actions)
			VALUES($1,$2,$3,$4)
			ON CONFLICT(user_id,surface_id) DO UPDATE SET proactive_enabled=EXCLUDED.proactive_enabled,saved_actions=EXCLUDED.saved_actions,updated_at=NOW()
			RETURNING surface_id,proactive_enabled,proactive_cooldown_minutes,
				proactive_snoozed_until,proactive_last_shown_at,proactive_dismissed_at,saved_actions,updated_at
		`, userID, preference.SurfaceID, preference.Proactive, preference.SavedActions).Scan(
			&out.SurfaceID, &out.Proactive, &out.ProactiveCooldownMinutes,
			&out.ProactiveSnoozedUntil, &out.ProactiveLastShownAt, &out.ProactiveDismissedAt,
			&out.SavedActions, &out.UpdatedAt,
		)
	})
	return out, err
}

func (db *Database) RecordAIFeedback(ctx context.Context, userID, invocationID string, rating int, reason, comment string) error {
	if rating != -1 && rating != 1 || len([]rune(comment)) > 2000 {
		return ErrSpaceInvalid
	}
	return db.WithRLSContext(ctx, userRLSSettings(userID), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO ai_feedback(id,user_id,invocation_id,rating,reason_code,comment)
			SELECT $1,$2,$3,$4,$5,$6 FROM ai_invocations WHERE id=$3 AND user_id=$2
			ON CONFLICT(user_id,invocation_id) DO UPDATE SET rating=EXCLUDED.rating,reason_code=EXCLUDED.reason_code,comment=EXCLUDED.comment,created_at=NOW()
		`, "aifeedback_"+uuid.NewString(), userID, invocationID, rating, strings.TrimSpace(reason), strings.TrimSpace(comment))
		return err
	})
}
