-- +goose Up
-- A person's own profile banner, uploaded by them. The 3:1 PNG lives in the object store under
-- banner_object_key (banners/<user id>-<suffix>); the version names the current one (0: none)
-- and gives the picture's ETag, so a new upload shows at once.
ALTER TABLE users ADD COLUMN IF NOT EXISTS banner_version bigint NOT NULL DEFAULT 0
    CONSTRAINT users_banner_version_check CHECK (banner_version >= 0);
ALTER TABLE users ADD COLUMN IF NOT EXISTS banner_updated_at timestamptz;
ALTER TABLE users ADD COLUMN IF NOT EXISTS banner_object_key text;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS banner_object_key;
ALTER TABLE users DROP COLUMN IF EXISTS banner_updated_at;
ALTER TABLE users DROP COLUMN IF EXISTS banner_version;
