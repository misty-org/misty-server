-- +goose Up
-- Where an agent opens a screen when its task needs one: a separate agent
-- window, a tab in the user's Misty window, or a choice in the chat each time.
ALTER TABLE ai_user_settings ADD COLUMN IF NOT EXISTS screen_location text NOT NULL DEFAULT 'separate'
    CHECK (screen_location IN ('separate', 'window', 'ask'));

-- +goose Down
ALTER TABLE ai_user_settings DROP COLUMN IF EXISTS screen_location;
