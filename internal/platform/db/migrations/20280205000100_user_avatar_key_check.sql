-- +goose Up
-- A person's avatar is stored under avatars/<user id>-<16 hex>, a new key for each upload, but the
-- baseline only allowed the old avatars/avatar_<uuid> keys, so every upload failed this check. It
-- now takes both.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_avatar_object_key_check;
ALTER TABLE users ADD CONSTRAINT users_avatar_object_key_check CHECK (
    avatar_object_key IS NULL
    OR avatar_object_key ~ '^avatars/avatar_[0-9a-f-]{36}$'
    OR avatar_object_key ~ '^avatars/[0-9a-f-]{36}-[0-9a-f]{16}$'
);

-- +goose Down
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_avatar_object_key_check;
ALTER TABLE users ADD CONSTRAINT users_avatar_object_key_check CHECK (
    avatar_object_key IS NULL OR avatar_object_key ~ '^avatars/avatar_[0-9a-f-]{36}$'
);
