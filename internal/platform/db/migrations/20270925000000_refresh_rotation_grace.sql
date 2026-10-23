-- A refresh response can be lost after the server rotates the token (a dropped
-- connection, an app quit mid-request). Remember the token each rotation
-- replaced so the client's retry with it is accepted for a short grace window
-- instead of being treated as a replay that revokes the whole session.
-- +goose Up
ALTER TABLE public.sessions
    ADD COLUMN previous_refresh_hash text,
    ADD COLUMN refresh_rotated_at timestamp with time zone;

-- +goose Down
ALTER TABLE public.sessions
    DROP COLUMN IF EXISTS previous_refresh_hash,
    DROP COLUMN IF EXISTS refresh_rotated_at;
