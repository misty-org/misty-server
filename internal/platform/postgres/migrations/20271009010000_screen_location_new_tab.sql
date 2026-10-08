-- +goose Up
-- New screens open in a new tab beside the user's work by default ('window').
-- 'separate' was only ever the implicit default, so it becomes the new one.
ALTER TABLE ai_user_settings ALTER COLUMN screen_location SET DEFAULT 'window';
UPDATE ai_user_settings SET screen_location = 'window' WHERE screen_location = 'separate';

-- +goose Down
ALTER TABLE ai_user_settings ALTER COLUMN screen_location SET DEFAULT 'separate';
