-- +goose Up
-- Each device in a pair decides whether the other may change its files. Off by
-- default; the existing device_pairs trigger publishes the change as "devices".
ALTER TABLE device_pairs ADD COLUMN IF NOT EXISTS first_accepts_writes boolean NOT NULL DEFAULT false;
ALTER TABLE device_pairs ADD COLUMN IF NOT EXISTS second_accepts_writes boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE device_pairs DROP COLUMN IF EXISTS second_accepts_writes;
ALTER TABLE device_pairs DROP COLUMN IF EXISTS first_accepts_writes;
