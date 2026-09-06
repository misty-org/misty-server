-- +goose Up
CREATE TABLE payment_entitlement_inbox (
    event_id UUID PRIMARY KEY,
    user_id TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK(revision>0),
    payload_sha256 TEXT NOT NULL CHECK(length(payload_sha256)=64),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id,revision)
);
CREATE TABLE payment_entitlement_projections (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    license_id TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK(revision>0),
    event_id UUID NOT NULL REFERENCES payment_entitlement_inbox(event_id),
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE payment_entitlement_inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_entitlement_inbox FORCE ROW LEVEL SECURITY;
ALTER TABLE payment_entitlement_projections ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_entitlement_projections FORCE ROW LEVEL SECURITY;
CREATE POLICY payment_entitlement_inbox_service ON payment_entitlement_inbox
  USING(current_setting('app.rls_mode',true)='service')
  WITH CHECK(current_setting('app.rls_mode',true)='service');
CREATE POLICY payment_entitlement_projections_service ON payment_entitlement_projections
  USING(current_setting('app.rls_mode',true)='service')
  WITH CHECK(current_setting('app.rls_mode',true)='service');
CREATE POLICY payment_entitlement_projections_user_read ON payment_entitlement_projections FOR SELECT
  USING(current_setting('app.rls_mode',true)='user' AND user_id=current_setting('app.current_user_id',true));

-- +goose Down
DROP TABLE payment_entitlement_projections;
DROP TABLE payment_entitlement_inbox;
