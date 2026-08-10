-- +goose Up
-- +goose StatementBegin
-- Google credentials are account-level and can serve Calendar, Gmail, Drive,
-- and future Google capabilities through incremental OAuth consent.
DELETE FROM provider_oauth_states WHERE provider='google_calendar';

ALTER TABLE space_calendar_sources DROP CONSTRAINT IF EXISTS space_calendar_sources_provider_check;
ALTER TABLE space_calendar_events DROP CONSTRAINT IF EXISTS space_calendar_events_provider_check;

UPDATE space_integrations SET provider='google',updated_at=NOW() WHERE provider='google_calendar';
UPDATE space_provider_credentials SET provider='google',updated_at=NOW() WHERE provider='google_calendar';
UPDATE provider_subscriptions SET provider='google',updated_at=NOW() WHERE provider='google_calendar';
UPDATE provider_event_inbox SET provider='google' WHERE provider='google_calendar';
UPDATE space_calendar_sources SET provider='google',updated_at=NOW() WHERE provider='google_calendar';
UPDATE space_calendar_events SET provider='google',updated_at=NOW() WHERE provider='google_calendar';

ALTER TABLE space_calendar_sources ADD CONSTRAINT space_calendar_sources_provider_check CHECK (provider='google');
ALTER TABLE space_calendar_events ADD CONSTRAINT space_calendar_events_provider_check CHECK (provider='google');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM provider_oauth_states WHERE provider='google';

ALTER TABLE space_calendar_sources DROP CONSTRAINT IF EXISTS space_calendar_sources_provider_check;
ALTER TABLE space_calendar_events DROP CONSTRAINT IF EXISTS space_calendar_events_provider_check;

UPDATE space_integrations SET provider='google_calendar',updated_at=NOW() WHERE provider='google';
UPDATE space_provider_credentials SET provider='google_calendar',updated_at=NOW() WHERE provider='google';
UPDATE provider_subscriptions SET provider='google_calendar',updated_at=NOW() WHERE provider='google';
UPDATE provider_event_inbox SET provider='google_calendar' WHERE provider='google';
UPDATE space_calendar_sources SET provider='google_calendar',updated_at=NOW() WHERE provider='google';
UPDATE space_calendar_events SET provider='google_calendar',updated_at=NOW() WHERE provider='google';

ALTER TABLE space_calendar_sources ADD CONSTRAINT space_calendar_sources_provider_check CHECK (provider='google_calendar');
ALTER TABLE space_calendar_events ADD CONSTRAINT space_calendar_events_provider_check CHECK (provider='google_calendar');
-- +goose StatementEnd
