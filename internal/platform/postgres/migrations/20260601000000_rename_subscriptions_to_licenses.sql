-- +goose Up
-- +goose StatementBegin
ALTER TABLE subscriptions RENAME TO licenses;

ALTER TABLE licenses RENAME CONSTRAINT subscriptions_pkey TO licenses_user_id_pkey;
ALTER TABLE licenses DROP CONSTRAINT licenses_user_id_pkey;

ALTER TABLE licenses
    ADD COLUMN id TEXT;

ALTER TABLE licenses
    ADD COLUMN IF NOT EXISTS trial_started_at TIMESTAMPTZ;

UPDATE licenses
SET id = 'lic_' || REPLACE(user_id, '-', '')
WHERE id IS NULL;

ALTER TABLE licenses
    ALTER COLUMN id SET NOT NULL;

ALTER TABLE licenses
    ADD CONSTRAINT licenses_pkey PRIMARY KEY (id);

ALTER TABLE licenses
    ADD CONSTRAINT licenses_user_id_key UNIQUE (user_id);

INSERT INTO licenses (id, user_id, tier, status, expires_at, license_device, trial_started_at)
SELECT
    'lic_' || REPLACE(users.id, '-', ''),
    users.id,
    'basic',
    'active',
    NULL,
    '',
    NULL
FROM users
LEFT JOIN licenses ON licenses.user_id = users.id
WHERE licenses.user_id IS NULL;

UPDATE licenses
SET tier = CASE tier
    WHEN 'free' THEN 'basic'
    WHEN 'pro' THEN 'personal'
    WHEN 'max' THEN 'pro'
    ELSE tier
END;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS license_id TEXT;

UPDATE users
SET license_id = licenses.id
FROM licenses
WHERE licenses.user_id = users.id
  AND users.license_id IS NULL;

ALTER TABLE users
    ALTER COLUMN license_id SET NOT NULL;

ALTER TABLE users
    ADD CONSTRAINT users_license_id_fkey FOREIGN KEY (license_id) REFERENCES licenses(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_license_id_fkey;
ALTER TABLE users DROP COLUMN IF EXISTS license_id;

ALTER TABLE licenses DROP CONSTRAINT IF EXISTS licenses_user_id_key;
ALTER TABLE licenses DROP CONSTRAINT IF EXISTS licenses_pkey;
ALTER TABLE licenses ADD CONSTRAINT licenses_user_id_pkey PRIMARY KEY (user_id);
ALTER TABLE licenses DROP COLUMN IF EXISTS id;
ALTER TABLE licenses DROP COLUMN IF EXISTS trial_started_at;

UPDATE licenses
SET tier = CASE tier
    WHEN 'basic' THEN 'free'
    WHEN 'personal' THEN 'pro'
    WHEN 'pro' THEN 'max'
    ELSE tier
END;

ALTER TABLE licenses RENAME TO subscriptions;
-- +goose StatementEnd
