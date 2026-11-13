-- +goose Up
-- A Space's own picture, uploaded by its owner. The PNG lives in the object store under
-- avatars/spaces/<space id>; the version names the current one (0: none, the app draws initials)
-- and gives the picture's ETag, so a new upload shows everywhere at once.
ALTER TABLE spaces ADD COLUMN IF NOT EXISTS avatar_version bigint NOT NULL DEFAULT 0
    CONSTRAINT spaces_avatar_version_check CHECK (avatar_version >= 0);

-- +goose Down
ALTER TABLE spaces DROP COLUMN IF EXISTS avatar_version;
