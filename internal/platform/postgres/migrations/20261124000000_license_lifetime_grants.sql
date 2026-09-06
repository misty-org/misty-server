-- +goose Up
SELECT set_config('app.rls_mode','service',true);
CREATE TABLE license_lifetime_grants (
    id UUID PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    license_id TEXT NOT NULL REFERENCES licenses(id) ON DELETE CASCADE,
    source TEXT NOT NULL CHECK(source IN ('legacy','manual','purchase')),
    source_id TEXT NOT NULL,
    tier TEXT NOT NULL CHECK(tier IN ('basic','personal','pro','max')),
    revoked_at TIMESTAMPTZ,
    reversal_event_id UUID REFERENCES payment_entitlement_inbox(event_id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(source,source_id)
);
CREATE INDEX license_lifetime_grants_active ON license_lifetime_grants(license_id) WHERE revoked_at IS NULL;
-- Preserve the recorded tier without guessing whether it came from a purchase
-- or a manual grant. Purchase attribution requires the reviewed import step.
INSERT INTO license_lifetime_grants(id,user_id,license_id,source,source_id,tier)
SELECT gen_random_uuid(),user_id,id,'legacy',id,legacy_tier FROM licenses WHERE legacy_tier IS NOT NULL;
ALTER TABLE license_lifetime_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE license_lifetime_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY license_lifetime_grants_service ON license_lifetime_grants
  USING(current_setting('app.rls_mode',true)='service')
  WITH CHECK(current_setting('app.rls_mode',true)='service');

-- +goose Down
DROP TABLE license_lifetime_grants;
