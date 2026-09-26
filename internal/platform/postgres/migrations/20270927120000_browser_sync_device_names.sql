-- Device names are now chosen by the user. Clear the hostname-based names
-- older clients generated ("<host> · <8-hex id>"); clients show a friendly
-- default ("Matthew's Mac 2") until the user names the device.
-- +goose Up
UPDATE public.browser_sync_devices
SET display_name = ''
WHERE display_name ~ ' · [0-9a-f]{8}$';

-- +goose Down
SELECT 1;
