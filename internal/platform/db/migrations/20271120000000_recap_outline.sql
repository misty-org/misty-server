-- +goose Up
-- What a day's recap names beside its text: the threads of the day and what was left to do.
ALTER TABLE ai_recaps ADD COLUMN outline jsonb;

-- +goose Down
ALTER TABLE ai_recaps DROP COLUMN outline;
