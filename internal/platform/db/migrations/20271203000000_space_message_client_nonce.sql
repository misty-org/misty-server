-- +goose Up
-- The app sends a nonce with every message so a retry after a lost response can be recognised.
-- It was echoed back but never kept, so a retry posted the message twice. Keeping it with a
-- unique (sender, nonce) lets the server hand back the message it already made instead.
ALTER TABLE space_messages ADD COLUMN client_nonce text;
ALTER TABLE space_messages ADD CONSTRAINT space_messages_client_nonce_check
    CHECK (client_nonce IS NULL OR char_length(client_nonce) BETWEEN 1 AND 128);
CREATE UNIQUE INDEX space_messages_sender_client_nonce_idx
    ON space_messages(sender_user_id, client_nonce)
    WHERE client_nonce IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS space_messages_sender_client_nonce_idx;
ALTER TABLE space_messages DROP CONSTRAINT IF EXISTS space_messages_client_nonce_check;
ALTER TABLE space_messages DROP COLUMN IF EXISTS client_nonce;
