-- +goose Up
ALTER TABLE provider_source_subscriptions ADD COLUMN watch_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE provider_source_subscriptions ADD COLUMN watch_attempts INT NOT NULL DEFAULT 0;
ALTER TABLE provider_source_subscriptions ADD COLUMN notification_received_at TIMESTAMPTZ;
CREATE TABLE provider_source_watches (
 channel_id UUID PRIMARY KEY,
 subscription_id TEXT NOT NULL REFERENCES provider_source_subscriptions(id) ON DELETE CASCADE,
 token_hash TEXT NOT NULL,
 resource_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK(state IN ('pending','active','failed')),
 expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_message NUMERIC(20,0) NOT NULL DEFAULT 0,
 last_received_at TIMESTAMPTZ
);
CREATE INDEX provider_source_watch_subscription ON provider_source_watches(subscription_id,expires_at);
ALTER TABLE provider_source_watches ENABLE ROW LEVEL SECURITY;
ALTER TABLE provider_source_watches FORCE ROW LEVEL SECURITY;
CREATE POLICY source_watch_service ON provider_source_watches FOR ALL USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  GRANT SELECT,INSERT,UPDATE,DELETE ON provider_source_watches TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Retain private delivery checkpoints during rollback; old workers can keep polling.
SELECT 1;
