-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

ALTER TABLE ai_surface_preferences
    ADD COLUMN proactive_cooldown_minutes SMALLINT NOT NULL DEFAULT 360
        CHECK(proactive_cooldown_minutes BETWEEN 30 AND 10080),
    ADD COLUMN proactive_snoozed_until TIMESTAMPTZ,
    ADD COLUMN proactive_last_shown_at TIMESTAMPTZ,
    ADD COLUMN proactive_dismissed_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE ai_surface_preferences
    DROP COLUMN IF EXISTS proactive_dismissed_at,
    DROP COLUMN IF EXISTS proactive_last_shown_at,
    DROP COLUMN IF EXISTS proactive_snoozed_until,
    DROP COLUMN IF EXISTS proactive_cooldown_minutes;
-- +goose StatementEnd
