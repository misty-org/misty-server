-- +goose Up
CREATE TABLE provider_source_previews (
 token_hash TEXT PRIMARY KEY,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
 payload JSONB NOT NULL CHECK(jsonb_typeof(payload)='object'),
 expires_at TIMESTAMPTZ NOT NULL,
 consumed_source_id TEXT
);
CREATE INDEX provider_source_previews_expiry ON provider_source_previews(expires_at);
CREATE TABLE space_source_items (
 id TEXT PRIMARY KEY,
 space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
 destination TEXT NOT NULL CHECK(destination IN ('library','journal','planner','chat')),
 provider TEXT NOT NULL,
 contributor_id TEXT NOT NULL REFERENCES users(id),
 resource JSONB NOT NULL CHECK(jsonb_typeof(resource)='object'),
 state TEXT NOT NULL DEFAULT 'snapshot',
 sync_mode TEXT NOT NULL DEFAULT 'none' CHECK(sync_mode IN ('none','push','poll','device')),
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
 last_success_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX space_source_items_space ON space_source_items(space_id,destination,updated_at DESC);
-- Private execution metadata must never be selected into the shared projection.
CREATE TABLE provider_source_subscriptions (
 id TEXT PRIMARY KEY,
 item_id TEXT NOT NULL REFERENCES space_source_items(id) ON DELETE CASCADE,
 owner_id TEXT NOT NULL REFERENCES users(id),
 space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
 destination TEXT NOT NULL,
 source_key TEXT NOT NULL,
 source JSONB NOT NULL CHECK(jsonb_typeof(source)='object'),
 executor TEXT NOT NULL CHECK(executor IN ('none','server','device')),
 device_id TEXT NOT NULL DEFAULT '',
 checkpoint TEXT NOT NULL DEFAULT '',
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 requested BIGINT NOT NULL DEFAULT 0,
 completed BIGINT NOT NULL DEFAULT 0,
 available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_id UUID,
 lease_until TIMESTAMPTZ,
 attempts INT NOT NULL DEFAULT 0,
 UNIQUE(space_id,destination,source_key)
);
CREATE INDEX provider_source_work_due ON provider_source_subscriptions(available_at) WHERE enabled;
ALTER TABLE provider_source_previews ENABLE ROW LEVEL SECURITY;
ALTER TABLE provider_source_previews FORCE ROW LEVEL SECURITY;
ALTER TABLE space_source_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE space_source_items FORCE ROW LEVEL SECURITY;
ALTER TABLE provider_source_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE provider_source_subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY source_preview_owner ON provider_source_previews FOR ALL USING(misty_rls_is_service() OR user_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR user_id=misty_rls_user_id());
CREATE POLICY source_item_service ON space_source_items FOR ALL USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
CREATE POLICY source_subscription_owner ON provider_source_subscriptions FOR ALL USING(misty_rls_is_service() OR owner_id=misty_rls_user_id()) WITH CHECK(misty_rls_is_service() OR owner_id=misty_rls_user_id());
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  GRANT SELECT,INSERT,UPDATE,DELETE ON provider_source_previews,space_source_items,provider_source_subscriptions TO misty_app;
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE provider_source_copies (
 request_id UUID NOT NULL,
 user_id TEXT NOT NULL REFERENCES users(id),
 source_id TEXT NOT NULL REFERENCES space_source_items(id) ON DELETE CASCADE,
 native_id TEXT NOT NULL,
 source_version BIGINT NOT NULL CHECK(source_version>0),
 PRIMARY KEY(user_id,request_id)
);
ALTER TABLE provider_source_copies ENABLE ROW LEVEL SECURITY;
ALTER TABLE provider_source_copies FORCE ROW LEVEL SECURITY;
CREATE POLICY source_copy_service ON provider_source_copies FOR ALL USING(misty_rls_is_service()) WITH CHECK(misty_rls_is_service());
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN GRANT SELECT,INSERT,UPDATE,DELETE ON provider_source_copies TO misty_app; END IF; END $$;
-- +goose StatementEnd
-- +goose Down
-- Preserve intentionally shared content and subscription history on rollback.
SELECT 1;
