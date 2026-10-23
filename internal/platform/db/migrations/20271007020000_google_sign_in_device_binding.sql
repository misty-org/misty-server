-- +goose Up
-- A finished Google sign-in is redeemed only with a one-time code that reaches
-- the browser that completed it (and, from there, Misty on the same computer).
-- Holding the polling secret alone no longer yields a session, so a sign-in
-- link sent to someone else cannot sign the sender into their account.
ALTER TABLE google_sign_in_flows ADD COLUMN completion_hash text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE google_sign_in_flows DROP COLUMN completion_hash;
