-- +goose Up
SELECT set_config('app.rls_mode','service',true);
ALTER TABLE payment_entitlement_projections ADD COLUMN access_expires_at TIMESTAMPTZ;
UPDATE payment_entitlement_projections SET access_expires_at=CASE payload->'subscription'->>'status'
  WHEN 'active' THEN (payload->'subscription'->>'currentPeriodEnd')::timestamptz+INTERVAL '72 hours'
  WHEN 'trialing' THEN (payload->'subscription'->>'currentPeriodEnd')::timestamptz END;
CREATE INDEX payment_entitlement_projections_expiry ON payment_entitlement_projections(access_expires_at,user_id) WHERE access_expires_at IS NOT NULL;

-- +goose Down
DROP INDEX payment_entitlement_projections_expiry;
ALTER TABLE payment_entitlement_projections DROP COLUMN access_expires_at;
