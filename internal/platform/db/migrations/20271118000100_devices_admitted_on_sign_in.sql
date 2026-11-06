-- +goose Up
-- A signed-in session is trusted as soon as it registers; there's no second step any more.
ALTER TABLE trusted_devices DROP CONSTRAINT IF EXISTS trusted_devices_admitted_grant_check;
UPDATE trusted_devices SET admission_state = 'admitted', updated_at = NOW()
WHERE admission_state IN ('pending', 'legacy');
DROP TABLE IF EXISTS device_admission_requests;
DROP TABLE IF EXISTS device_lists;

-- +goose Down
SELECT 1;
