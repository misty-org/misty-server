-- +goose Up
-- A personal vault's password wrapper can be replaced, for a password change or
-- when a vault sealed with a sync secret moves to the account wrapper. Each
-- replacement carries a transition signed by the vault root for the next
-- generation, which clients check against the root key they pinned. The server
-- stores the signature and generation; it cannot forge either.
ALTER TABLE public.browser_sync_vaults
    ADD COLUMN key_generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN key_transition_signature bytea,
    ADD CONSTRAINT browser_sync_vaults_key_transition_check CHECK (
        key_generation >= 0 AND key_generation <= 9007199254740991
        AND ((key_generation = 0) = (key_transition_signature IS NULL))
    );

-- +goose Down
ALTER TABLE public.browser_sync_vaults
    DROP CONSTRAINT browser_sync_vaults_key_transition_check,
    DROP COLUMN key_transition_signature,
    DROP COLUMN key_generation;
